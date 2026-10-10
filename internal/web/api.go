package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/auth"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/querylog"
	"github.com/masaleem-oss/Fengard/internal/store"
	"github.com/masaleem-oss/Fengard/internal/sysinfo"
)

func contextWith(r *http.Request, s *auth.Session) context.Context {
	return context.WithValue(r.Context(), ctxKey{}, s)
}

func sessionFrom(r *http.Request) *auth.Session {
	s, _ := r.Context().Value(ctxKey{}).(*auth.Session)
	return s
}

// session

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"setupNeeded": s.Auth.NeedsSetup(),
		"boxName":     s.Config.Get().Settings.BoxName,
		"version":     s.Version,
	}
	if sess := s.currentSession(r); sess != nil {
		out["user"], out["role"] = sess.Username, sess.Role
	}
	writeJSON(w, out)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	if err := s.Auth.Setup(c.Username, c.Password); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.startSession(w, r, c)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimit.Allow(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	s.startSession(w, r, c)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, c credentials) {
	sess, token, err := s.Auth.BeginLogin(c.Username, c.Password)
	if err == nil && token != "" {
		writeJSON(w, map[string]any{"twoFactor": true, "token": token})
		return
	}
	if err != nil {
		s.Alerts.Raise("login-fail:"+clientIP(r), 10*time.Minute, alertLoginFail(clientIP(r)))
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.setSessionCookie(w, r, sess)
	s.Audit.Append(auditRecord(sess.Username, clientIP(r), "Signed in"))
	writeJSON(w, map[string]any{"user": sess.Username, "role": sess.Role})
}

// session cookie the server handles idle and absolute timeouts
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sess.Token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.Auth.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &body) {
		return
	}
	sess := sessionFrom(r)
	if err := s.Auth.ChangePassword(sess.Username, body.Current, body.New); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "Changed password", "")
	writeJSON(w, map[string]bool{"ok": true})
}

// overview

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	cfg := s.Config.Get()
	seen := s.Devices.Seen()
	online := 0
	for _, d := range seen {
		if time.Since(d.LastSeen) < 10*time.Minute {
			online++
		}
	}
	pending := 0
	for _, d := range cfg.Devices {
		if !d.Approved {
			pending++
		}
	}
	c := &s.DNS.Counters
	fw := s.Firewall.Status()
	pendingList := []map[string]any{}
	for _, d := range cfg.Devices {
		if !d.Approved && len(pendingList) < 5 {
			pendingList = append(pendingList, map[string]any{
				"mac": d.MAC, "hostname": d.Hostname, "vendor": devices.Vendor(d.MAC), "firstSeen": d.FirstSeen,
			})
		}
	}
	cpu := 0.0
	if s.CPU != nil {
		cpu = s.CPU.CPUPercent()
	}
	writeJSON(w, map[string]any{
		"alertDrops":     s.Alerts.Dropped(),
		"pendingDevices": pendingList,
		"recentAlerts":   s.Alerts.List(5, time.Time{}),
		"protection": map[string]any{
			"paused": s.Policy.Paused(time.Now()), "pausedUntil": cfg.Settings.PausedTill,
			"tempAllows": len(cfg.TempAllow),
		},
		"stats": s.Log.Stats(),
		"dns": map[string]any{
			"queries": c.Queries.Load(), "rateLimited": c.RateLimited.Load(), "refused": c.Refused.Load(),
			"cacheHits": c.CacheHits.Load(), "cacheSize": s.DNS.CacheLen(), "upstream": c.Upstream.Load(),
			"upstreamErrors": c.UpstreamErr.Load(), "servedStale": c.Stale.Load(), "overloaded": c.Overloaded.Load(),
		},
		"devices":   map[string]int{"known": len(cfg.Devices), "online": online, "pending": pending},
		"blocklist": s.Catalog.Set().Len(),
		"firewall":  map[string]any{"enabled": fw.Enabled, "applied": fw.Applied, "error": fw.Error},
		"system": map[string]any{
			"cpuPercent": cpu,
			"cpuSeconds": sysinfo.CPUTotal().Seconds(),
			"cores":      sysinfo.Cores(),
			"platform":   runtime.GOOS + "/" + runtime.GOARCH,
			"boxName":    cfg.Settings.BoxName,
			"appVersion": s.Version,
			"uptimeSec":  int(time.Since(s.Started).Seconds()),
			"memoryMB":   float64(mem.Sys) / (1 << 20),
			"heapMB":     float64(mem.HeapAlloc) / (1 << 20),
			"goroutines": runtime.NumGoroutine(),
			"version":    cfg.Version,
		},
	})
}

func (s *Server) queries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := querylog.Filter{
		MAC: q.Get("mac"), Client: q.Get("client"), Search: q.Get("search"),
		Blocked: q.Get("blocked") == "1", Limit: limit,
	}
	if b := q.Get("before"); b != "" {
		if t, err := time.Parse(time.RFC3339Nano, b); err == nil {
			f.Before = t
		}
	}
	if q.Get("source") == "recent" {
		n := limit
		if n <= 0 || n > 1000 {
			n = 200
		}
		rows := s.Log.Recent(n)
		out := rows[:0]
		for _, e := range rows {
			if (!f.Blocked || e.Blocked()) && (f.MAC == "" || e.MAC == f.MAC) &&
				(f.Search == "" || strings.Contains(e.Domain, strings.ToLower(f.Search))) {
				out = append(out, e)
			}
		}
		writeJSON(w, out)
		return
	}
	writeJSON(w, s.Log.History(f))
}

// devices

type deviceView struct {
	config.Device
	Vendor     string    `json:"vendor,omitempty"`
	Randomized bool      `json:"randomized"` // private random mac
	IPs        []string  `json:"ips"`
	LastSeen   time.Time `json:"lastSeen,omitzero"`
	Online     bool      `json:"online"`
	Status     string    `json:"status"` // allowed paused or quarantined
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	seen := s.Devices.Seen()
	now := time.Now()
	out := make([]deviceView, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		v := deviceView{
			Device: d, Vendor: devices.Vendor(d.MAC), Randomized: devices.IsRandomized(d.MAC),
			Status: s.Policy.Decide(d.MAC, "fengard.check", now).Action.String(),
		}
		if sn, ok := seen[d.MAC]; ok {
			v.IPs, v.LastSeen = sn.IPs, sn.LastSeen
			v.Online = !sn.LastSeen.IsZero() && now.Sub(sn.LastSeen) < 10*time.Minute
			// tunnel clients get the tunnel name it knows better than whatever got recorded first
			if v.Hostname == "" || (strings.HasPrefix(d.MAC, "ts:") && sn.Hostname != "") {
				v.Hostname = sn.Hostname
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Approved != out[j].Approved {
			return !out[i].Approved // pending first
		}
		return strings.ToLower(displayName(out[i].Device)) < strings.ToLower(displayName(out[j].Device))
	})
	writeJSON(w, out)
}

func displayName(d config.Device) string { return firstNonEmpty(d.Name, d.Hostname, d.MAC) }

func pathMAC(r *http.Request) (string, error) {
	id, err := config.NormIdentity(r.PathValue("mac"))
	if err != nil {
		return "", errors.New("invalid device address")
	}
	return id, nil
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Name     *string `json:"name"`
		Group    *string `json:"group"`
		Approved *bool   `json:"approved"`
	}
	if !decode(w, r, &body) {
		return
	}
	ok := s.update(w, r, "Updated device "+mac, func(c *config.Config) error {
		d := c.Device(mac)
		if d == nil {
			return errors.New("unknown device")
		}
		if body.Name != nil {
			d.Name = *body.Name
		}
		if body.Group != nil {
			d.Group = *body.Group
		}
		if body.Approved != nil {
			d.Approved = *body.Approved
		}
		return nil
	})
	if ok {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

type pauseBody struct {
	Minutes int `json:"minutes"` // 0 means resume
}

func pauseUntil(minutes int) (time.Time, error) {
	if minutes < 0 || minutes > 7*24*60 {
		return time.Time{}, errors.New("pause must be between 0 minutes and 7 days")
	}
	if minutes == 0 {
		return time.Time{}, nil
	}
	return time.Now().Add(time.Duration(minutes) * time.Minute), nil
}

func (s *Server) pauseDevice(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body pauseBody
	if !decode(w, r, &body) {
		return
	}
	until, err := pauseUntil(body.Minutes)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	summary := fmt.Sprintf("Paused device %s for %d min", mac, body.Minutes)
	if body.Minutes == 0 {
		summary = "Resumed device " + mac
	}
	if s.update(w, r, summary, func(c *config.Config) error {
		d := c.Device(mac)
		if d == nil {
			return errors.New("unknown device")
		}
		d.PausedTill = until
		return nil
	}) {
		writeJSON(w, map[string]any{"pausedUntil": until})
	}
}

func (s *Server) forgetDevice(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.update(w, r, "Removed device "+mac, func(c *config.Config) error {
		c.Devices = slices.DeleteFunc(c.Devices, func(d config.Device) bool { return d.MAC == mac })
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// groups

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	type groupView struct {
		config.Group
		Devices int  `json:"devices"`
		Default bool `json:"default"`
	}
	out := make([]groupView, 0, len(cfg.Groups))
	for _, g := range cfg.Groups {
		n := 0
		for _, d := range cfg.Devices {
			if d.Group == g.ID {
				n++
			}
		}
		out = append(out, groupView{Group: g, Devices: n, Default: g.ID == cfg.Settings.DefaultGroup})
	}
	writeJSON(w, out)
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request) {
	var g config.Group
	if !decode(w, r, &g) {
		return
	}
	id := r.PathValue("id")
	creating := id == ""
	if creating {
		g.ID = slug(g.Name)
	} else {
		g.ID = id
	}
	if g.Categories == nil {
		g.Categories = []string{}
	}
	verb := "Updated"
	if creating {
		verb = "Created"
	}
	if s.update(w, r, fmt.Sprintf("%s group %q", verb, g.Name), func(c *config.Config) error {
		existing := c.Group(g.ID)
		switch {
		case creating && existing != nil:
			return fmt.Errorf("a group with id %q already exists", g.ID)
		case !creating && existing == nil:
			return errors.New("unknown group")
		case creating:
			c.Groups = append(c.Groups, g)
		default:
			g.PausedTill = existing.PausedTill // pausing has its own endpoint
			*existing = g
		}
		return nil
	}) {
		writeJSON(w, g)
	}
}

func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 32 {
		s = s[:32]
	}
	if s == "" {
		s = "group-" + config.NewID()[:4]
	}
	return s
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.update(w, r, "Deleted group "+id, func(c *config.Config) error {
		if id == c.Settings.DefaultGroup {
			return errors.New("can't delete the default group")
		}
		if c.Group(id) == nil {
			return errors.New("unknown group")
		}
		for i := range c.Devices {
			if c.Devices[i].Group == id {
				c.Devices[i].Group = c.Settings.DefaultGroup
			}
		}
		c.Groups = slices.DeleteFunc(c.Groups, func(g config.Group) bool { return g.ID == id })
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (s *Server) pauseGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body pauseBody
	if !decode(w, r, &body) {
		return
	}
	until, err := pauseUntil(body.Minutes)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	summary := fmt.Sprintf("Paused group %s for %d min", id, body.Minutes)
	if body.Minutes == 0 {
		summary = "Resumed group " + id
	}
	if s.update(w, r, summary, func(c *config.Config) error {
		g := c.Group(id)
		if g == nil {
			return errors.New("unknown group")
		}
		g.PausedTill = until
		return nil
	}) {
		writeJSON(w, map[string]any{"pausedUntil": until})
	}
}

// categories and rules

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Catalog.Status())
}

func (s *Server) updateCategories(w http.ResponseWriter, r *http.Request) {
	if !s.startWorker(func(parent context.Context) {
		ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
		defer cancel()
		s.Catalog.Update(ctx)
		if ctx.Err() == nil {
			s.Config.Reconcile()
		}
	}) {
		httpError(w, http.StatusServiceUnavailable, "Fengard is shutting down")
		return
	}
	s.audit(r, "Started blocklist update", "")
	writeJSON(w, map[string]bool{"started": true})
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	writeJSON(w, map[string]any{"block": cfg.Block, "allow": cfg.Allow, "temp": cfg.TempAllow})
}

func (s *Server) setRule(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Kind   string `json:"kind"`
			Domain string `json:"domain"`
		}
		if !decode(w, r, &body) {
			return
		}
		domain := config.NormDomain(body.Domain)
		verb := map[bool]string{true: "Added", false: "Removed"}[on]
		if !s.update(w, r, fmt.Sprintf("%s %s rule for %s", verb, body.Kind, domain), func(c *config.Config) error {
			var list *[]string
			switch body.Kind {
			case "block":
				list = &c.Block
			case "allow":
				list = &c.Allow
			default:
				return errors.New("kind must be block or allow")
			}
			*list = slices.DeleteFunc(*list, func(d string) bool { return d == domain })
			if on {
				*list = append(*list, domain)
			}
			return nil
		}) {
			return
		}
		s.listRules(w, r)
	}
}

// port forwards

func (s *Server) listForwards(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Config.Get().PortForwards)
}

func (s *Server) saveForward(w http.ResponseWriter, r *http.Request) {
	var f config.PortForward
	if !decode(w, r, &f) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		f.ID = config.NewID()
	} else {
		f.ID = id
	}
	if s.update(w, r, fmt.Sprintf("Saved port forward %q (%s %d -> %s:%d)", f.Name, f.Proto, f.ExtPort, f.DestIP, f.DestPort), func(c *config.Config) error {
		for i := range c.PortForwards {
			if c.PortForwards[i].ID == f.ID {
				c.PortForwards[i] = f
				return nil
			}
		}
		if id != "" {
			return errors.New("unknown port forward")
		}
		c.PortForwards = append(c.PortForwards, f)
		return nil
	}) {
		writeJSON(w, f)
	}
}

func (s *Server) deleteForward(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.update(w, r, "Deleted port forward "+id, func(c *config.Config) error {
		n := len(c.PortForwards)
		c.PortForwards = slices.DeleteFunc(c.PortForwards, func(f config.PortForward) bool { return f.ID == id })
		if len(c.PortForwards) == n {
			return errors.New("unknown port forward")
		}
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// settings history backup

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Config.Get().Settings)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var set config.Settings
	if !decode(w, r, &set) {
		return
	}
	// turning it on from plain http would lock this browser out straight away
	if set.RequireHTTPS && !s.Config.Get().Settings.RequireHTTPS && r.TLS == nil && !loopbackClient(r) {
		httpError(w, http.StatusBadRequest, "open the dashboard over HTTPS first, then turn this on")
		return
	}
	before := strings.Join(s.Config.Get().Settings.Upstreams, ",")
	if s.update(w, r, "Changed settings", func(c *config.Config) error {
		c.Settings = set
		return nil
	}) {
		if strings.Join(s.Config.Get().Settings.Upstreams, ",") != before {
			s.DNS.FlushCache() // cached answers came from the old servers
		}
		writeJSON(w, s.Config.Get().Settings)
	}
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Config.History())
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int `json:"version"`
	}
	if !decode(w, r, &body) {
		return
	}
	if _, err := s.Config.Rollback(s.actor(r), body.Version); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, fmt.Sprintf("Rolled back to version %d", body.Version), "")
	writeJSON(w, map[string]bool{"ok": true})
}

// config plus the ca so a restore on a new box keeps the trust already on devices
type backupFile struct {
	*config.Config
	CABundle string `json:"caBundle,omitempty"`
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fengard-backup-%s.json"`, time.Now().Format("2006-01-02")))
	s.audit(r, "Exported configuration backup", "")
	writeJSON(w, backupFile{Config: s.Config.Get(), CABundle: string(s.CA.Export())})
}

func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	b := backupFile{Config: &config.Config{}}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		httpError(w, http.StatusBadRequest, "not a Fengard backup: "+err.Error())
		return
	}
	if _, err := s.Config.Replace(s.actor(r), "Restored configuration from backup", b.Config); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	note := ""
	if b.CABundle != "" {
		if err := s.CA.Import([]byte(b.CABundle)); err != nil {
			note = "configuration restored, but the certificate in the backup was not usable: " + err.Error()
		} else {
			note = "certificate restored too"
		}
	}
	s.audit(r, "Restored configuration from backup", note)
	writeJSON(w, map[string]any{"ok": true, "note": note})
}

func (s *Server) certificateExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="fengard-ca-with-key.pem"`)
	s.audit(r, "Exported the certificate authority with its key", "")
	w.Write(s.CA.Export())
}

func (s *Server) certificateImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.CA.Import(body); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("CA SHA-256: %s", s.CA.Fingerprint())
	s.audit(r, "Replaced the certificate authority", s.CA.Name())
	s.certificateInfo(w, r)
}

// logs

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	out := []AuditEntry{}
	s.Audit.Scan(time.Time{}, 2000, func(_ time.Time, v []byte) bool {
		var e AuditEntry
		if json.Unmarshal(v, &e) == nil {
			out = append(out, e)
		}
		return len(out) < 200
	})
	writeJSON(w, out)
}

type prefs struct {
	AlertsReadAt time.Time `json:"alertsReadAt"`
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	list := s.Alerts.List(200, time.Time{})
	var p prefs
	s.Prefs.Get(sessionFrom(r).Username, &p)
	unread := 0
	for _, a := range list {
		if a.Time.After(p.AlertsReadAt) {
			unread++
		}
	}
	writeJSON(w, map[string]any{"alerts": list, "unread": unread, "readAt": p.AlertsReadAt, "dropped": s.Alerts.Dropped()})
}

func (s *Server) markAlertsRead(w http.ResponseWriter, r *http.Request) {
	s.Prefs.Put(sessionFrom(r).Username, prefs{AlertsReadAt: time.Now()})
	writeJSON(w, map[string]bool{"ok": true})
}

// last 24h from up to 1000 recent queries
func (s *Server) deviceSummary(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows := s.Log.History(querylog.Filter{MAC: mac, Limit: 1000})
	since := time.Now().Add(-24 * time.Hour)
	domains, blocked := map[string]int{}, map[string]int{}
	total, nBlocked := 0, 0
	for _, e := range rows {
		if e.Time.Before(since) {
			break
		}
		total++
		domains[e.Domain]++
		if e.Blocked() {
			nBlocked++
			blocked[e.Domain]++
		}
	}
	writeJSON(w, map[string]any{
		"total": total, "blocked": nBlocked,
		"topDomains": topCounts(domains, 8), "topBlocked": topCounts(blocked, 8),
		"recent": rows[:min(25, len(rows))], "sampled": len(rows) == 1000,
	})
}

func topCounts(m map[string]int, n int) []querylog.Count {
	out := make([]querylog.Count, 0, len(m))
	for k, v := range m {
		out = append(out, querylog.Count{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out[:min(n, len(out))]
}

func (s *Server) certificateInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"name": s.CA.Name(), "fingerprint": s.CA.Fingerprint(), "expires": s.CA.Expires(), "probe": s.ProbeURL,
	})
}

func (s *Server) caCertDER(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="fengard-ca.der"`)
	w.Write(s.CA.CertDER())
}

func (s *Server) caMobileConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="fengard.mobileconfig"`)
	w.Write(s.CA.MobileConfig(s.Config.Get().Settings.BoxName))
}

// unauthenticated so its rate limited per device and only ever makes an alert
func (s *Server) accessRequest(w http.ResponseWriter, r *http.Request) {
	client := clientIP(r)
	var body struct {
		Domain string `json:"domain"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	domain := config.NormDomain(body.Domain)
	if domain == "" || len(domain) > 253 {
		httpError(w, http.StatusBadRequest, "invalid site")
		return
	}
	if !s.requestLimit.Allow(client) {
		httpError(w, http.StatusTooManyRequests, "You've already sent a few requests. Please wait a few minutes.")
		return
	}
	note := strings.TrimSpace(body.Note)
	if len(note) > 200 {
		note = note[:200]
	}
	info, _ := s.Devices.Lookup(client)
	dec := s.Policy.Decide(info.MAC, domain, time.Now())
	detail := firstNonEmpty(dec.Device, info.Hostname, client) + " asked to unblock " + domain
	if note != "" {
		detail += ": “" + note + "”"
	}
	s.Alerts.Raise("request:"+client+":"+domain, 10*time.Minute, alerts.Alert{
		Kind: "access_request", Severity: alerts.Info, MAC: info.MAC, Domain: domain,
		Title: "Access request: " + domain, Detail: detail,
	})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) firewallStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Firewall.Status())
}

// users

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Auth.Users())
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		credentials
		Role auth.Role `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Auth.CreateUser(body.Username, body.Password, body.Role); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, fmt.Sprintf("Created %s account %s", body.Role, body.Username), "")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if sessionFrom(r).Username == name {
		httpError(w, http.StatusBadRequest, "you can't delete your own account")
		return
	}
	if err := s.Auth.DeleteUser(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "Deleted account "+name, "")
	writeJSON(w, map[string]bool{"ok": true})
}

func alertLoginFail(ip string) alerts.Alert {
	return alerts.Alert{
		Kind: "login_failed", Severity: alerts.Warning,
		Title: "Failed dashboard sign-in", Detail: "From " + ip,
	}
}

func auditRecord(user, ip, action string) store.Record {
	now := time.Now()
	return store.Record{Time: now, Value: AuditEntry{Time: now, User: user, IP: ip, Action: action}}
}
