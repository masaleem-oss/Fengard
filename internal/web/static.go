package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
)

// ye etags and pre gzipped so a repeat visit is just a 304
type assetServer struct {
	fs fs.FS

	mu    sync.Mutex
	cache map[string]*asset
}

type asset struct {
	body, gz []byte
	etag     string
	ctype    string
	cacheCtl string
}

func newAssetServer(f fs.FS) *assetServer {
	return &assetServer{fs: f, cache: map[string]*asset{}}
}

var compressible = map[string]bool{".js": true, ".css": true, ".svg": true, ".html": true, ".json": true, ".txt": true, ".mjs": true}

func (a *assetServer) load(name string) (*asset, error) {
	a.mu.Lock()
	if as, ok := a.cache[name]; ok {
		a.mu.Unlock()
		return as, nil
	}
	a.mu.Unlock()

	f, err := a.fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.IsDir() {
		return nil, fs.ErrNotExist
	}
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	ext := path.Ext(name)
	as := &asset{body: body, ctype: mime.TypeByExtension(ext)}
	if as.ctype == "" {
		as.ctype = http.DetectContentType(body)
	}
	if ext == ".js" || ext == ".mjs" {
		as.ctype = "text/javascript; charset=utf-8"
	}
	sum := sha256.Sum256(body)
	as.etag = `"` + hex.EncodeToString(sum[:8]) + `"`
	switch {
	case strings.HasPrefix(name, "fonts/"), strings.HasPrefix(name, "img/"):
		// fonts icons and logo only change with the binary
		as.cacheCtl = "public, max-age=31536000, immutable"
	default:
		// code gets revalidated every load unchanged files are a 304
		as.cacheCtl = "no-cache"
	}
	if compressible[ext] && len(body) > 512 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		zw.Write(body)
		zw.Close()
		if buf.Len() < len(body) {
			as.gz = buf.Bytes()
		}
	}
	a.mu.Lock()
	a.cache[name] = as
	a.mu.Unlock()
	return as, nil
}

func (a *assetServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || strings.HasSuffix(r.URL.Path, "/") {
		name = "index.html"
	}
	if strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	as, err := a.load(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", as.ctype)
	h.Set("Cache-Control", as.cacheCtl)
	h.Set("ETag", as.etag)
	h.Set("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, as.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := as.body
	if as.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = as.gz
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Write(body)
}
