package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/internet"
	"github.com/masaleem-oss/Fengard/internal/netscan"
	"github.com/masaleem-oss/Fengard/internal/traffic"
)

// internet, live activity, whos home and the risky device check

type named struct {
	MAC    string `json:"mac"`
	Name   string `json:"name"`
	Vendor string `json:"vendor,omitempty"`
}

func (s *Server) namedDevice(c *config.Config, mac string) named {
	n := named{MAC: mac, Vendor: devices.Vendor(mac)}
	if d := c.Device(mac); d != nil {
		n.Name = firstNonEmpty(d.Name, d.Hostname)
	}
	if n.Name == "" && s.Devices != nil {
		if seen, ok := s.Devices.Seen()[mac]; ok {
			n.Name = seen.Hostname
		}
	}
	return n
}

func (s *Server) internetStatus(w http.ResponseWriter, r *http.Request) {
	if s.Internet == nil {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	writeJSON(w, struct {
		internet.Status
		Available bool `json:"available"`
		Daily     bool `json:"daily"`
	}{s.Internet.Status(), true, !s.Config.Get().Settings.SpeedTestOff})
}

func (s *Server) speedTest(w http.ResponseWriter, r *http.Request) {
	if s.Internet == nil {
		httpError(w, http.StatusNotFound, "speed tests aren't available here")
		return
	}
	if err := s.Internet.Start(s.startWorker); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, internet.ErrBusy) {
			status = http.StatusConflict
		}
		httpError(w, status, err.Error())
		return
	}
	s.audit(r, "Ran a speed test", "")
	writeJSON(w, map[string]bool{"started": true})
}

type trafficDevice struct {
	traffic.Device
	Name   string `json:"name"`
	Vendor string `json:"vendor,omitempty"`
}

func (s *Server) trafficNow(w http.ResponseWriter, r *http.Request) {
	if s.Traffic == nil {
		writeJSON(w, map[string]any{"available": false, "devices": []any{}})
		return
	}
	snap := s.Traffic.Snapshot()
	c := s.Config.Get()
	out := make([]trafficDevice, 0, len(snap.Devices))
	for _, d := range snap.Devices {
		n := s.namedDevice(c, d.MAC)
		out = append(out, trafficDevice{Device: d, Name: n.Name, Vendor: n.Vendor})
	}
	writeJSON(w, map[string]any{
		"available": snap.Available, "downRate": snap.DownRate, "upRate": snap.UpRate, "today": snap.Today, "devices": out,
	})
}

func (s *Server) trafficHistory(w http.ResponseWriter, r *http.Request) {
	if s.Traffic == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, s.Traffic.History(r.PathValue("mac"), 30))
}

func (s *Server) presenceNow(w http.ResponseWriter, r *http.Request) {
	type person struct {
		named
		Home  bool      `json:"home"`
		Since time.Time `json:"since,omitzero"`
	}
	out := []person{}
	if s.Presence == nil {
		writeJSON(w, map[string]any{"available": false, "devices": out})
		return
	}
	c := s.Config.Get()
	for _, d := range s.Presence.Devices() {
		out = append(out, person{named: s.namedDevice(c, d.MAC), Home: d.Home, Since: d.Since})
	}
	writeJSON(w, map[string]any{"available": s.Presence.Available(), "devices": out})
}

func (s *Server) scanReport(w http.ResponseWriter, r *http.Request) {
	if s.Scanner == nil {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	type device struct {
		named
		IP       string            `json:"ip"`
		Findings []netscan.Finding `json:"findings"`
	}
	rep := s.Scanner.Last()
	c := s.Config.Get()
	out := make([]device, 0, len(rep.Devices))
	for _, d := range rep.Devices {
		out = append(out, device{named: s.namedDevice(c, d.MAC), IP: d.IP, Findings: d.Findings})
	}
	writeJSON(w, map[string]any{
		"available": true, "running": s.Scanner.Running(), "time": rep.Time, "checked": rep.Checked, "devices": out,
	})
}

func (s *Server) scanNow(w http.ResponseWriter, r *http.Request) {
	if s.Scanner == nil {
		httpError(w, http.StatusNotFound, "device checks aren't available here")
		return
	}
	if !s.Scanner.Trigger() {
		httpError(w, http.StatusConflict, "a check is already running")
		return
	}
	s.audit(r, "Checked devices for risky settings", "")
	writeJSON(w, map[string]bool{"started": true})
}
