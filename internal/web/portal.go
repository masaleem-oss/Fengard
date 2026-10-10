package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/policy"
)

// screen time

type limitView struct {
	Target string `json:"target"`
	Name   string `json:"name"`
	Used   int    `json:"used"`
	Limit  int    `json:"limit"` // 0 means none
	Left   int    `json:"left"`  // -1 means no limit
	Pct    int    `json:"pct"`
}

type personDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Used int    `json:"used"`
}

type personView struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Person     bool           `json:"person"`
	Status     string         `json:"status"` // on paused timeup pending
	StatusText string         `json:"statusText"`
	Until      time.Time      `json:"until,omitzero"`
	Used       int            `json:"used"`
	Limit      int            `json:"limit"`
	Bonus      int            `json:"bonus"`
	Left       int            `json:"left"`
	Pct        int            `json:"pct"`
	Limits     []limitView    `json:"limits"`
	Activity   []limitView    `json:"activity"` // today most first
	Devices    []personDevice `json:"devices"`
}

func (s *Server) personSummary(c *config.Config, g config.Group, now time.Time) personView {
	v := personView{ID: g.ID, Name: g.Name, Person: g.Person, Status: "on", StatusText: "Internet is on", Left: -1, Limits: []limitView{}, Activity: []limitView{}, Devices: []personDevice{}}
	if s.Screen == nil {
		return v
	}
	v.Used, v.Bonus, v.Limit = s.Screen.Used(g.ID, ""), s.Screen.Bonus(g.ID), g.DailyLimit
	if g.DailyLimit > 0 {
		v.Left = max(0, g.DailyLimit+v.Bonus-v.Used)
		v.Pct = min(100, v.Used*100/(g.DailyLimit+v.Bonus))
	}
	switch {
	case now.Before(g.PausedTill):
		v.Status, v.StatusText, v.Until = "paused", "Internet is paused until "+g.PausedTill.In(s.loc(c)).Format("15:04"), g.PausedTill
	case g.DailyLimit > 0 && v.Left == 0:
		v.Status, v.StatusText = "timeup", "Screen time is used up for today"
	}
	used := s.Screen.Targets(g.ID)
	for _, l := range g.AppLimits {
		u := used[l.Target]
		v.Limits = append(v.Limits, limitView{Target: l.Target, Name: config.LimitName(l.Target), Used: u, Limit: l.Minutes,
			Left: max(0, l.Minutes-u), Pct: min(100, u*100/l.Minutes)})
	}
	for target, u := range used {
		if name := config.LimitName(target); name != "" && u > 0 && strings.HasPrefix(target, "app:") {
			v.Activity = append(v.Activity, limitView{Target: target, Name: name, Used: u, Left: -1})
		}
	}
	sort.Slice(v.Activity, func(i, j int) bool { return v.Activity[i].Used > v.Activity[j].Used })
	if len(v.Activity) > 6 {
		v.Activity = v.Activity[:6]
	}
	for id, m := range s.Screen.DeviceMinutes(g.ID) {
		name := id
		if d := c.Device(id); d != nil {
			name = displayName(*d)
		}
		v.Devices = append(v.Devices, personDevice{ID: id, Name: name, Used: m})
	}
	for _, d := range c.Devices {
		if d.Group == g.ID {
			found := false
			for _, pd := range v.Devices {
				if pd.ID == d.MAC {
					found = true
				}
			}
			if !found {
				v.Devices = append(v.Devices, personDevice{ID: d.MAC, Name: displayName(d)})
			}
		}
	}
	sort.Slice(v.Devices, func(i, j int) bool { return v.Devices[i].Used > v.Devices[j].Used })
	return v
}

func (s *Server) loc(c *config.Config) *time.Location {
	if l, err := time.LoadLocation(c.Settings.Timezone); err == nil {
		return l
	}
	return time.Local
}

func (s *Server) screenTime(w http.ResponseWriter, r *http.Request) {
	c := s.Config.Get()
	now := time.Now()
	out := []personView{}
	for _, g := range c.Groups {
		if g.Person {
			out = append(out, s.personSummary(c, g, now))
		}
	}
	writeJSON(w, out)
}

func (s *Server) groupBonus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &body) || body.Minutes < -24*60 || body.Minutes > 24*60 {
		httpError(w, http.StatusBadRequest, "minutes must be within a day")
		return
	}
	c := s.Config.Get()
	g := c.Group(id)
	if g == nil || s.Screen == nil {
		httpError(w, http.StatusNotFound, "unknown profile")
		return
	}
	total := s.Screen.AddBonus(id, body.Minutes)
	s.audit(r, fmt.Sprintf("Gave %s %d extra minutes today", g.Name, body.Minutes), fmt.Sprintf("total bonus %d", total))
	s.Config.Reconcile()
	writeJSON(w, s.personSummary(c, *g, time.Now()))
}

func (s *Server) portalFor(r *http.Request) (c *config.Config, g *config.Group, device string, dec policy.Decision) {
	c = s.Config.Get()
	client := clientIP(r)
	info, _ := s.Devices.Lookup(client)
	dec = s.Policy.Decide(info.MAC, "fengard.check", time.Now())
	device = firstNonEmpty(dec.Device, info.Hostname, client)
	if dec.GroupID != "" {
		g = c.Group(dec.GroupID)
	}
	return
}

type portalData struct {
	Network string
	Device  string
	Known   bool // device known and in a person profile
	Pending bool
	View    personView
	Hours   func(int) string
	Dash    string // ring stroke offset full circle is 263.9
}

func hm(m int) string {
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d h", m/60)
	}
	return fmt.Sprintf("%d h %02d min", m/60, m%60)
}

func (s *Server) portal(w http.ResponseWriter, r *http.Request) {
	c, g, device, dec := s.portalFor(r)
	d := portalData{Network: c.Settings.BoxName, Device: device, Hours: hm, Pending: dec.Action == policy.Quarantine, Dash: "0"}
	if g != nil && g.Person {
		d.Known = true
		d.View = s.personSummary(c, *g, time.Now())
		if d.View.Limit > 0 {
			d.Dash = fmt.Sprintf("%.1f", 263.9-2.639*float64(d.View.Pct))
		}
		if dec.Action == policy.Paused && d.View.Status == "on" {
			d.View.Status, d.View.StatusText = "paused", "Internet is paused for this device"
		}
	} else if g != nil {
		d.View = personView{Name: g.Name, Left: -1}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	portalTmpl.Execute(w, d)
}

func (s *Server) portalJSON(w http.ResponseWriter, r *http.Request) {
	c, g, device, dec := s.portalFor(r)
	out := map[string]any{"device": device, "known": false, "pending": dec.Action == policy.Quarantine}
	if g != nil && g.Person {
		out["known"] = true
		out["view"] = s.personSummary(c, *g, time.Now())
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

// unauthenticated so rate limited per device and only ever makes an alert
func (s *Server) timeRequest(w http.ResponseWriter, r *http.Request) {
	client := clientIP(r)
	var body struct {
		Minutes int    `json:"minutes"`
		Note    string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Minutes < 5 || body.Minutes > 240 {
		httpError(w, http.StatusBadRequest, "ask for 5 minutes to 4 hours")
		return
	}
	if !s.requestLimit.Allow(client) {
		httpError(w, http.StatusTooManyRequests, "You already asked recently. Give the admin a moment.")
		return
	}
	c, g, device, _ := s.portalFor(r)
	if g == nil || !g.Person {
		httpError(w, http.StatusBadRequest, "This device isn't in a person's profile.")
		return
	}
	info, _ := s.Devices.Lookup(client)
	note := strings.TrimSpace(body.Note)
	if len(note) > 200 {
		note = note[:200]
	}
	detail := fmt.Sprintf("From %s · used %s today", device, hm(s.Screen.Used(g.ID, "")))
	if note != "" {
		detail += " · “" + note + "”"
	}
	s.Alerts.Raise("time:"+g.ID, 5*time.Minute, alerts.Alert{
		Kind: "time_request", Severity: alerts.Info, MAC: info.MAC,
		Title:  fmt.Sprintf("%s asks for %s more", g.Name, hm(body.Minutes)),
		Detail: detail,
	})
	_ = c
	writeJSON(w, map[string]bool{"ok": true})
}
