package web

import (
	"errors"
	"net/http"
	"sort"
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

type usageDevice struct {
	traffic.Usage
	MAC    string `json:"mac"`
	Name   string `json:"name"`
	Vendor string `json:"vendor,omitempty"`
}

// totals per device and per day between two dates, both included
func (s *Server) trafficUsage(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	for _, d := range []string{from, to} {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			httpError(w, http.StatusBadRequest, "dates must look like 2026-10-01")
			return
		}
	}
	days, devs := map[string]*traffic.Usage{}, map[string]*traffic.Usage{}
	add := func(m map[string]*traffic.Usage, k string, u traffic.Usage) {
		if m[k] == nil {
			m[k] = &traffic.Usage{}
		}
		m[k].Down += u.Down
		m[k].Up += u.Up
	}
	if s.Traffic != nil {
		for _, h := range s.Traffic.History("", 0) {
			if h.Day >= from && h.Day <= to {
				add(days, h.Day, h.Usage)
				add(devs, h.MAC, h.Usage)
			}
		}
	}
	type day struct {
		Day string `json:"day"`
		traffic.Usage
	}
	outDays := []day{}
	for d, u := range days {
		outDays = append(outDays, day{d, *u})
	}
	sort.Slice(outDays, func(i, j int) bool { return outDays[i].Day < outDays[j].Day })
	c := s.Config.Get()
	outDevs := []usageDevice{}
	for mac, u := range devs {
		n := s.namedDevice(c, mac)
		outDevs = append(outDevs, usageDevice{Usage: *u, MAC: mac, Name: n.Name, Vendor: n.Vendor})
	}
	sort.Slice(outDevs, func(i, j int) bool { return outDevs[i].Down+outDevs[i].Up > outDevs[j].Down+outDevs[j].Up })
	writeJSON(w, map[string]any{"days": outDays, "devices": outDevs})
}
