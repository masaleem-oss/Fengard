package web

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/masaleem-oss/Fengard/internal/auth"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/guest"
)

// GuestWiFi is the router side of guest wi-fi, tests fake it
type GuestWiFi interface {
	Status() guest.Status
	Disconnect(mac string) bool
	IsGuest(ip string) bool
}

const guestGroupID = "guests"

type guestView struct {
	MAC       string    `json:"mac"`
	Name      string    `json:"name"`
	Hostname  string    `json:"hostname,omitempty"`
	Vendor    string    `json:"vendor,omitempty"`
	IPs       []string  `json:"ips"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen,omitzero"`
	Online    bool      `json:"online"`
}

func (s *Server) guestStatus() guest.Status {
	if s.Guest == nil {
		return guest.Status{State: "off"}
	}
	return s.Guest.Status()
}

// a guest's address is on the guest network, they get the block page instead of the dashboard
func (s *Server) fromGuest(r *http.Request) bool {
	return s.Guest != nil && s.Guest.IsGuest(clientIP(r))
}

func (s *Server) guestInfo(w http.ResponseWriter, r *http.Request) {
	c := s.Config.Get()
	g := c.Guest
	seen := s.Devices.Seen()
	now := time.Now()
	waiting, approved, denied := []guestView{}, []guestView{}, []guestView{}
	for _, d := range c.Devices {
		if !d.Guest {
			continue
		}
		v := guestView{MAC: d.MAC, Name: d.Name, Hostname: d.Hostname, Vendor: devices.Vendor(d.MAC), IPs: []string{}, FirstSeen: d.FirstSeen}
		if sn, ok := seen[d.MAC]; ok {
			v.IPs, v.LastSeen = sn.IPs, sn.LastSeen
			v.Online = !sn.LastSeen.IsZero() && now.Sub(sn.LastSeen) < 10*time.Minute
			if v.Hostname == "" {
				v.Hostname = sn.Hostname
			}
		}
		switch {
		case d.Approved:
			approved = append(approved, v)
		case d.Denied:
			denied = append(denied, v)
		default:
			waiting = append(waiting, v)
		}
	}
	// newest first, the one that just joined is at the top
	for _, l := range [][]guestView{waiting, approved, denied} {
		sort.Slice(l, func(i, j int) bool { return l[i].FirstSeen.After(l[j].FirstSeen) })
	}
	out := map[string]any{
		"enabled": g.Enabled, "ssid": g.SSID, "group": g.Group, "groupName": groupName(c, g.Group),
		"status": s.guestStatus(), "waiting": waiting, "guests": approved, "denied": denied,
	}
	// the password only goes to admins, viewers see the page without it
	if sess := sessionFrom(r); sess != nil && sess.Role == auth.Admin && g.Password != "" {
		out["password"], out["qr"] = g.Password, guest.QR(g.SSID, g.Password)
	}
	writeJSON(w, out)
}

func (s *Server) guestSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool   `json:"enabled"`
		SSID    string `json:"ssid"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled && !s.guestStatus().Router {
		httpError(w, http.StatusBadRequest, guest.ErrNotRouter.Error())
		return
	}
	summary := "Turned guest Wi-Fi off"
	if body.Enabled {
		summary = "Turned guest Wi-Fi on"
	}
	if s.update(w, r, summary, func(c *config.Config) error {
		g := &c.Guest
		g.Enabled = body.Enabled
		if ssid := strings.TrimSpace(body.SSID); ssid != "" {
			g.SSID = ssid
		}
		if g.SSID == "" {
			g.SSID = defaultSSID(c.Settings.BoxName)
		}
		if g.Password == "" {
			g.Password = guest.NewPassword()
		}
		if g.Enabled {
			guestGroup(c)
		}
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func defaultSSID(box string) string {
	// wi-fi names are 32 bytes, cut on a whole character
	n := strings.TrimSpace(box)
	for len(n) > 26 {
		_, size := utf8.DecodeLastRuneInString(n)
		n = strings.TrimSpace(n[:len(n)-size])
	}
	if n == "" {
		n = "Fengard"
	}
	return n + " Guest"
}

// guests get their own profile, made when guest wi-fi is first turned on or if it was deleted
func guestGroup(c *config.Config) {
	if c.Guest.Group != "" && c.Group(c.Guest.Group) != nil {
		return
	}
	if c.Group(guestGroupID) == nil {
		c.Groups = append(c.Groups, config.Group{ID: guestGroupID, Name: "Guests", Categories: []string{"malware", "adult"}})
	}
	c.Guest.Group = guestGroupID
}

// guestPassword sets the password the owner typed, or makes a random one when none was given
func (s *Server) guestPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	// no body at all asks for a random one, like before
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	pw := body.Password
	if pw == "" {
		pw = guest.NewPassword()
	} else if err := guest.CheckPassword(pw); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.update(w, r, "New guest Wi-Fi password", func(c *config.Config) error {
		if c.Guest.SSID == "" {
			c.Guest.SSID = defaultSSID(c.Settings.BoxName)
		}
		c.Guest.Password = pw
		return nil
	}) {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (s *Server) guestAction(w http.ResponseWriter, r *http.Request) {
	mac, err := pathMAC(r)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := r.PathValue("action")
	summary := map[string]string{"approve": "Approved guest ", "deny": "Denied guest ", "kick": "Kicked guest "}[action]
	if summary == "" {
		httpError(w, http.StatusNotFound, "unknown action")
		return
	}
	if !s.update(w, r, summary+mac, func(c *config.Config) error {
		d := c.Device(mac)
		if d == nil || !d.Guest {
			return errors.New("not a guest on the guest Wi-Fi")
		}
		d.Approved, d.Denied = action == "approve", action != "approve"
		return nil
	}) {
		return
	}
	out := map[string]bool{"ok": true}
	if action == "kick" && s.Guest != nil {
		// the firewall blocks it from the next sync whatever happens here
		out["disconnected"] = s.Guest.Disconnect(mac)
	}
	writeJSON(w, out)
}
