package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/auth"
	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/policy"
)

// 2fa

func (s *Server) login2FA(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimit.Allow(clientIP(r)) {
		httpError(w, http.StatusTooManyRequests, "too many attempts, wait a minute")
		return
	}
	var body struct {
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	sess, err := s.Auth.CompleteLogin(body.Token, body.Code)
	if err != nil {
		s.Alerts.Raise("login-fail:"+clientIP(r), 10*time.Minute, alertLoginFail(clientIP(r)))
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.setSessionCookie(w, r, sess)
	s.Audit.Append(auditRecord(sess.Username, clientIP(r), "Signed in (two-factor)"))
	writeJSON(w, map[string]any{"user": sess.Username, "role": sess.Role})
}

func (s *Server) twoFactorStatus(w http.ResponseWriter, r *http.Request) {
	u := sessionFrom(r).Username
	enabled := false
	for _, x := range s.Auth.Users() {
		if x.Username == u {
			enabled = x.TwoFactor
		}
	}
	writeJSON(w, map[string]any{"enabled": enabled, "recoveryLeft": s.Auth.RecoveryLeft(u)})
}

func (s *Server) twoFactorSetup(w http.ResponseWriter, r *http.Request) {
	secret, uri, err := s.Auth.TOTPSetup(sessionFrom(r).Username)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"secret": secret, "uri": uri})
}

func (s *Server) twoFactorEnable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	codes, err := s.Auth.TOTPEnable(sessionFrom(r).Username, body.Code)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.renewSession(w, r)
	s.audit(r, "Enabled two-factor authentication", "")
	writeJSON(w, map[string]any{"recoveryCodes": codes})
}

func (s *Server) twoFactorDisable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Auth.TOTPDisable(sessionFrom(r).Username, body.Password); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.renewSession(w, r)
	s.audit(r, "Disabled two-factor authentication", "")
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) adminReset2FA(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.Auth.AdminResetTOTP(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "Reset two-factor for "+name, "")
	writeJSON(w, map[string]bool{"ok": true})
}

// api keys

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Auth.Keys()) }

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string    `json:"name"`
		Role auth.Role `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	plain, k, err := s.Auth.CreateKey(body.Name, body.Role)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, fmt.Sprintf("Created %s API key %q", body.Role, body.Name), "")
	writeJSON(w, map[string]any{"key": plain, "id": k.ID, "name": k.Name, "role": k.Role})
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.DeleteKey(r.PathValue("id")); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "Deleted API key "+r.PathValue("id"), "")
	writeJSON(w, map[string]bool{"ok": true})
}

// check a site

// the why is this blocked tool
func (s *Server) checkSite(w http.ResponseWriter, r *http.Request) {
	domain := config.NormDomain(r.URL.Query().Get("domain"))
	if !catalog.ValidDomain(domain) {
		httpError(w, http.StatusBadRequest, "enter a domain like example.com")
		return
	}
	now := time.Now()
	cfg := s.Config.Get()
	type row struct {
		Group    string `json:"group"`
		GroupID  string `json:"groupId"`
		Action   string `json:"action"`
		Reason   string `json:"reason"`
		Category string `json:"category,omitempty"`
	}
	rows := []row{}
	for _, g := range cfg.Groups {
		d := s.Policy.DecideForGroup(g.ID, domain, now)
		rows = append(rows, row{g.Name, g.ID, d.Action.String(), d.Reason, d.Category})
	}
	out := map[string]any{"domain": domain, "profiles": rows}
	if mac := r.URL.Query().Get("mac"); mac != "" {
		if hw, err := net.ParseMAC(mac); err == nil {
			d := s.Policy.Decide(hw.String(), domain, now)
			out["device"] = row{d.Group, d.GroupID, d.Action.String(), d.Reason, d.Category}
		}
	}
	// lists and apps that know the domain no matter the profile
	hit := s.Catalog.Set().Match(domain)
	cats := []string{}
	for _, id := range (hit & catalog.CategoryMask).IDs() {
		cats = append(cats, id)
	}
	out["categories"] = cats
	lists := []string{}
	custom := s.Catalog.Custom()
	for i := range custom {
		if hit&catalog.CustomBit(i) != 0 {
			lists = append(lists, custom[i].Name)
		}
	}
	out["lists"] = lists
	if ai := catalog.BuildAppSet().Lookup(domain); ai >= 0 {
		out["app"] = catalog.Apps[ai].Name
	}
	if s.DNS.Local != nil {
		if _, ok := s.DNS.Local.Lookup(domain); ok {
			out["local"] = true
		}
	}
	writeJSON(w, out)
}

// temporary allow

func (s *Server) addTempAllow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain  string `json:"domain"`
		Group   string `json:"group"`
		Minutes int    `json:"minutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Minutes < 1 || body.Minutes > 7*24*60 {
		httpError(w, http.StatusBadRequest, "duration must be between 1 minute and 7 days")
		return
	}
	domain := config.NormDomain(body.Domain)
	until := time.Now().Add(time.Duration(body.Minutes) * time.Minute)
	scope := "everyone"
	if body.Group != "" {
		if g := s.Config.Get().Group(body.Group); g != nil {
			scope = g.Name
		}
	}
	if s.update(w, r, fmt.Sprintf("Allowed %s for %s until %s", domain, scope, until.Format("15:04")), func(c *config.Config) error {
		c.TempAllow = slices.DeleteFunc(c.TempAllow, func(t config.TempAllow) bool { return t.Domain == domain && t.Group == body.Group })
		c.TempAllow = append(c.TempAllow, config.TempAllow{Domain: domain, Group: body.Group, Until: until, By: s.actor(r)})
		return nil
	}) {
		s.DNS.FlushCache() // the block answer can be cached for up to a minute
		writeJSON(w, map[string]any{"until": until})
	}
}

func (s *Server) deleteTempAllow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
		Group  string `json:"group"`
	}
	if !decode(w, r, &body) {
		return
	}
	domain := config.NormDomain(body.Domain)
	if s.update(w, r, "Ended temporary allow for "+domain, func(c *config.Config) error {
		c.TempAllow = slices.DeleteFunc(c.TempAllow, func(t config.TempAllow) bool { return t.Domain == domain && t.Group == body.Group })
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// protection pause

func (s *Server) pauseProtection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Minutes < 0 || body.Minutes > 24*60 {
		httpError(w, http.StatusBadRequest, "pause protection for up to 24 hours")
		return
	}
	var until time.Time
	summary := "Resumed protection"
	if body.Minutes > 0 {
		until = time.Now().Add(time.Duration(body.Minutes) * time.Minute)
		summary = fmt.Sprintf("Paused protection for %d min", body.Minutes)
	}
	if s.update(w, r, summary, func(c *config.Config) error {
		c.Settings.PausedTill = until
		return nil
	}) {
		s.DNS.FlushCache()
		writeJSON(w, map[string]any{"pausedUntil": until})
	}
}

// custom blocklists

func (s *Server) listLists(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	status := map[string]catalog.Status{}
	for _, st := range s.Catalog.Status() {
		status[st.ID] = st
	}
	type view struct {
		config.CustomList
		Domains int       `json:"domains"`
		Updated time.Time `json:"updated"`
		Error   string    `json:"error,omitempty"`
	}
	out := []view{}
	for _, l := range cfg.Lists {
		v := view{CustomList: l}
		if st, ok := status["list:"+l.ID]; ok {
			v.Domains, v.Updated, v.Error = st.Domains, st.Updated, st.Error
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (s *Server) saveList(w http.ResponseWriter, r *http.Request) {
	var l config.CustomList
	if !decode(w, r, &l) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		l.ID, l.Enabled = config.NewID(), true
	} else {
		l.ID = id
	}
	if !s.update(w, r, fmt.Sprintf("Saved blocklist %q", l.Name), func(c *config.Config) error {
		for i := range c.Lists {
			if c.Lists[i].ID == l.ID {
				c.Lists[i] = l
				return nil
			}
		}
		if id != "" {
			return errors.New("unknown list")
		}
		c.Lists = append(c.Lists, l)
		return nil
	}) {
		return
	}
	if id == "" {
		s.startWorker(func(parent context.Context) {
			ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
			defer cancel()
			s.Catalog.UpdateCustom(ctx, l.URL)
		})
	}
	writeJSON(w, l)
}

func (s *Server) deleteList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.update(w, r, "Removed blocklist "+id, func(c *config.Config) error {
		n := len(c.Lists)
		c.Lists = slices.DeleteFunc(c.Lists, func(l config.CustomList) bool { return l.ID == id })
		if len(c.Lists) == n {
			return errors.New("unknown list")
		}
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (s *Server) updateList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var url string
	for _, l := range s.Config.Get().Lists {
		if l.ID == id {
			url = l.URL
		}
	}
	if url == "" {
		httpError(w, http.StatusNotFound, "unknown list")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.Catalog.UpdateCustom(ctx, url); err != nil {
		httpError(w, http.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	s.listLists(w, r)
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"groups": catalog.AppGroups, "apps": catalog.Apps})
}

// local dns

func (s *Server) dnsInfo(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	type name struct {
		Name string   `json:"name"`
		MACs []string `json:"macs"`
		IPs  []string `json:"ips"`
	}
	names := []name{}
	if s.DNS.Local != nil {
		for n, macs := range s.DNS.Local.Names() {
			ips := []string{}
			for _, m := range macs {
				ips = append(ips, s.Devices.IPsOf(m)...)
			}
			names = append(names, name{n, macs, ips})
		}
	}
	slices.SortFunc(names, func(a, b name) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, map[string]any{
		"zone": cfg.Settings.LocalZone, "records": cfg.Records, "names": names,
		"upstreams": cfg.Settings.Upstreams, "dnssec": cfg.Settings.DNSSEC,
	})
}

func (s *Server) addRecord(w http.ResponseWriter, r *http.Request) {
	var rec config.DNSRecord
	if !decode(w, r, &rec) {
		return
	}
	if s.update(w, r, fmt.Sprintf("Added DNS record %s %s %s", rec.Name, rec.Type, rec.Value), func(c *config.Config) error {
		c.Records = append(c.Records, rec)
		return nil
	}) {
		s.DNS.FlushCache()
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	var rec config.DNSRecord
	if !decode(w, r, &rec) {
		return
	}
	name, typ := config.NormDomain(rec.Name), strings.ToUpper(rec.Type)
	if s.update(w, r, fmt.Sprintf("Removed DNS record %s %s", name, typ), func(c *config.Config) error {
		n := len(c.Records)
		c.Records = slices.DeleteFunc(c.Records, func(x config.DNSRecord) bool { return x.Name == name && x.Type == typ })
		if len(c.Records) == n {
			return errors.New("no such record")
		}
		return nil
	}) {
		s.DNS.FlushCache()
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// notification channels

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	status := s.Notifier.Status()
	type view struct {
		config.Channel
		LastSent  time.Time `json:"lastSent,omitzero"`
		LastError string    `json:"lastError,omitempty"`
		Sent      int       `json:"sent"`
	}
	out := []view{}
	for _, ch := range cfg.Channels {
		v := view{Channel: ch}
		if ch.Type == "telegram" {
			v.Token = "" // never echo secrets back
		}
		if st, ok := status[ch.ID]; ok {
			v.LastSent, v.LastError, v.Sent = st.LastSent, st.LastError, st.Sent
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request) {
	var ch config.Channel
	if !decode(w, r, &ch) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		ch.ID = config.NewID()
	} else {
		ch.ID = id
	}
	if s.update(w, r, fmt.Sprintf("Saved notification channel %q", ch.Name), func(c *config.Config) error {
		for i := range c.Channels {
			if c.Channels[i].ID == ch.ID {
				if ch.Token == "" {
					ch.Token = c.Channels[i].Token // keep the secret if the form didnt resend it
				}
				c.Channels[i] = ch
				return nil
			}
		}
		if id != "" {
			return errors.New("unknown channel")
		}
		c.Channels = append(c.Channels, ch)
		return nil
	}) {
		ch.Token = ""
		writeJSON(w, ch)
	}
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.update(w, r, "Removed notification channel "+id, func(c *config.Config) error {
		n := len(c.Channels)
		c.Channels = slices.DeleteFunc(c.Channels, func(ch config.Channel) bool { return ch.ID == id })
		if len(c.Channels) == n {
			return errors.New("unknown channel")
		}
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, ch := range s.Config.Get().Channels {
		if ch.ID == id {
			err := s.Notifier.Send(ch, alerts.Alert{
				Time: time.Now(), Kind: "test", Severity: alerts.Info,
				Title: "Test notification", Detail: "If you can read this, " + ch.Name + " is working.",
			})
			if err != nil {
				httpError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
	}
	httpError(w, http.StatusNotFound, "unknown channel")
}

// history series

func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 31 {
		days = 7
	}
	writeJSON(w, map[string]any{"days": days, "hours": s.Log.Series(days)})
}

// wake on lan

func (s *Server) wakeDevice(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := devices.Wake(mac); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "Sent wake-up packet to "+mac, "")
	writeJSON(w, map[string]bool{"ok": true})
}

func decisionJSON(d policy.Decision) map[string]any {
	return map[string]any{"action": d.Action.String(), "reason": d.Reason, "category": d.Category, "group": d.Group}
}

func (s *Server) renewSession(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(sessionCookie); err != nil {
		return
	}
	if sess, err := s.Auth.RenewSession(sessionFrom(r).Username); err == nil {
		s.setSessionCookie(w, r, sess)
	}
}
