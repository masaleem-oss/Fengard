package web

import (
	"html/template"
	"mime"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/features"
)

func (s *Server) listFeatures(w http.ResponseWriter, r *http.Request) {
	if s.Features == nil {
		writeJSON(w, []features.Info{})
		return
	}
	writeJSON(w, s.Features.List())
}

func (s *Server) setFeature(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !slices.Contains(config.Features, id) {
			httpError(w, http.StatusNotFound, "no add-on called "+id)
			return
		}
		verb := "Removed"
		if on {
			verb = "Installed"
		}
		if s.update(w, r, verb+" the "+id+" add-on", func(c *config.Config) error {
			f := slices.DeleteFunc(c.Settings.Features, func(x string) bool { return x == id })
			if on {
				f = append(f, id)
			}
			c.Settings.Features = f
			return nil
		}) {
			s.listFeatures(w, r)
		}
	}
}

// addon wraps the api of an add-on so it answers only while installed
func (s *Server) addon(id string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.Config.Get().Settings.Has(id) {
			httpError(w, http.StatusNotFound, "that add-on isn't installed")
			return
		}
		h(w, r)
	}
}

// the add-on's own files, checked against the manifest when they were installed
func (s *Server) featureFile(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("file")
	p := features.Find(id)
	if p == nil || s.Features == nil || !s.Features.Ready(id) {
		http.NotFound(w, r)
		return
	}
	i := slices.IndexFunc(p.Files, func(f features.File) bool { return f.Path == name })
	if i < 0 || strings.HasSuffix(name, ".gz") {
		http.NotFound(w, r)
		return
	}
	etag := `"` + p.Files[i].SHA256[:16] + `"`
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", etag)
	if strings.Contains(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	b, err := os.ReadFile(s.Features.Path(id, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if path.Ext(name) == ".js" {
		ct = "text/javascript; charset=utf-8"
	}
	h.Set("Content-Type", ct)
	w.Write(b)
}

var (
	welcomeMu   sync.Mutex
	welcomeSum  string
	welcomeTmpl *template.Template
)

// the captive portal page comes with its add-on, parsed again only when a new one is installed
func (s *Server) welcomePage() *template.Template {
	p := features.Find("welcome")
	if s.Features == nil || p == nil || !s.Features.Ready("welcome") {
		return nil
	}
	welcomeMu.Lock()
	defer welcomeMu.Unlock()
	if welcomeTmpl != nil && welcomeSum == p.Files[0].SHA256 {
		return welcomeTmpl
	}
	b, err := os.ReadFile(s.Features.Path("welcome", "welcome.html"))
	if err != nil {
		return nil
	}
	t, err := template.New("welcome").Parse(string(b))
	if err != nil {
		return nil
	}
	welcomeTmpl, welcomeSum = t, p.Files[0].SHA256
	return t
}
