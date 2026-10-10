package web

import (
	_ "embed"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/config"
)

//go:embed welcome.html
var welcomeHTML string

var welcomeTmpl = template.Must(template.New("welcome").Parse(welcomeHTML))

// exactly what apple serves, anything else makes the phone show a login sheet
const appleSuccess = "<HTML><HEAD><TITLE>Success</TITLE></HEAD><BODY>Success</BODY></HTML>"

var shieldLabels = map[string]string{
	"ads":      "Ads and trackers",
	"malware":  "Scams, phishing and malware",
	"adult":    "Adult sites",
	"gambling": "Gambling",
	"social":   "Social media",
	"bypass":   "VPNs and proxies that get around the filter",
	"tor":      "Tor",
}

type welcomeData struct {
	Network string
	Blocks  []string
	Joined  bool
	// the admins own page, scripts are stopped by the content security policy
	Custom template.HTML
}

func (s *Server) welcomeData(c *config.Config) welcomeData {
	d := welcomeData{Network: c.Settings.BoxName, Custom: template.HTML(c.Settings.PortalHTML)}
	if d.Network == "" {
		d.Network = "Fengard"
	}
	if g := c.Group(c.Settings.DefaultGroup); g != nil {
		for _, cat := range g.Categories {
			if l, ok := shieldLabels[cat]; ok {
				d.Blocks = append(d.Blocks, l)
			}
		}
	}
	return d
}

// the phones captive check lands here because dns pointed captive.apple.com at us
func (s *Server) captiveCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	info, _ := s.Devices.Lookup(clientIP(r))
	// a mac on a private address looks like a phone until its browser says otherwise
	if s.Welcome != nil && strings.Contains(r.UserAgent(), "Macintosh") && s.Welcome.Want(info.MAC, info.Hostname) {
		s.Welcome.Accept(info.MAC)
	}
	if s.Welcome == nil || !s.Welcome.Want(info.MAC, info.Hostname) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(appleSuccess))
		return
	}
	renderPortal(w, s.welcomeData(s.Config.Get()))
}

func (s *Server) welcomeJoin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	info, _ := s.Devices.Lookup(clientIP(r))
	if info.MAC == "" || s.Welcome == nil {
		http.Error(w, "this device isn't on the network", http.StatusBadRequest)
		return
	}
	// anything not being welcomed just gets the done page
	if s.Welcome.Want(info.MAC, info.Hostname) {
		if err := s.Welcome.Accept(info.MAC); err != nil {
			http.Error(w, "couldn't save that, try again", http.StatusInternalServerError)
			return
		}
		if s.Alerts != nil {
			s.Alerts.Raise("welcome:"+info.MAC, time.Hour, alerts.Alert{Kind: "guest_joined", Severity: alerts.Info,
				Title: "Guest joined the Wi-Fi", Detail: firstNonEmpty(info.Hostname, "A new iPhone") + " joined through the captive portal", MAC: info.MAC})
		}
	}
	d := s.welcomeData(s.Config.Get())
	d.Joined = true
	renderPortal(w, d)
}

// the admins html can style the page but never run anything
const portalCSP = "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; form-action 'self'; frame-ancestors 'none'"

func renderPortal(w http.ResponseWriter, d welcomeData) {
	w.Header().Set("Content-Security-Policy", portalCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	welcomeTmpl.Execute(w, d)
}

// shows the portal in a dashboard tab so the admin can see their html
func (s *Server) portalPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	renderPortal(w, s.welcomeData(s.Config.Get()))
}
