package localdns

import (
	"net"
	"net/netip"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
)

type Answer struct {
	A     []net.IP
	AAAA  []net.IP
	CNAME string
}

type Resolver struct {
	tracker *devices.Tracker
	cur     atomic.Pointer[table]
}

type table struct {
	zone    string
	records map[string]Answer   // fqdn to record
	names   map[string][]string // device slug to macs
	macName map[string]string   // mac to slug for ptr
}

func New(t *devices.Tracker) *Resolver {
	r := &Resolver{tracker: t}
	r.cur.Store(&table{zone: "lan", records: map[string]Answer{}, names: map[string][]string{}, macName: map[string]string{}})
	return r
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// leos ipad becomes leos-ipad
func Slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "'", "")
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func (r *Resolver) Zone() string { return r.cur.Load().zone }

func (r *Resolver) Rebuild(cfg *config.Config) {
	t := &table{zone: cfg.Settings.LocalZone, records: map[string]Answer{}, names: map[string][]string{}, macName: map[string]string{}}
	for _, rec := range cfg.Records {
		a := t.records[rec.Name]
		switch rec.Type {
		case "A":
			a.A = append(a.A, net.ParseIP(rec.Value).To4())
		case "AAAA":
			a.AAAA = append(a.AAAA, net.ParseIP(rec.Value))
		case "CNAME":
			a.CNAME = rec.Value
		}
		t.records[rec.Name] = a
	}
	for _, d := range cfg.Devices {
		seen := map[string]bool{}
		for _, n := range []string{d.Name, d.Hostname} {
			s := Slug(n)
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			t.names[s] = append(t.names[s], d.MAC)
			if _, ok := t.macName[d.MAC]; !ok {
				t.macName[d.MAC] = s
			}
		}
	}
	r.cur.Store(t)
}

func (r *Resolver) Lookup(domain string) (Answer, bool) {
	t := r.cur.Load()
	if a, ok := t.records[domain]; ok {
		return a, true
	}
	label, ok := strings.CutSuffix(domain, "."+t.zone)
	if !ok || label == "" || strings.Contains(label, ".") {
		return Answer{}, false
	}
	macs := t.names[label]
	if len(macs) == 0 {
		// hostnames from dhcp that arent saved in the config yet
		for mac, s := range r.tracker.Seen() {
			if Slug(s.Hostname) == label {
				macs = append(macs, mac)
			}
		}
	}
	if len(macs) == 0 {
		return Answer{}, false
	}
	var a Answer
	for _, mac := range macs {
		for _, ip := range r.tracker.IPsOf(mac) {
			if p, err := netip.ParseAddr(ip); err == nil {
				if p.Is4() {
					a.A = append(a.A, net.IP(p.AsSlice()))
				} else if !p.IsLinkLocalUnicast() {
					a.AAAA = append(a.AAAA, net.IP(p.AsSlice()))
				}
			}
		}
	}
	return a, len(a.A)+len(a.AAAA) > 0
}

func (r *Resolver) Reverse(ip string) (string, bool) {
	t := r.cur.Load()
	for name, a := range t.records {
		for _, rec := range append(a.A, a.AAAA...) {
			if rec.String() == ip {
				return name, true
			}
		}
	}
	info, ok := r.tracker.Lookup(ip)
	if !ok {
		return "", false
	}
	if s, ok := t.macName[info.MAC]; ok {
		return s + "." + t.zone, true
	}
	if s := Slug(info.Hostname); s != "" {
		return s + "." + t.zone, true
	}
	return "", false
}

func (r *Resolver) Names() map[string][]string {
	t := r.cur.Load()
	out := make(map[string][]string, len(t.names))
	for s, macs := range t.names {
		out[s+"."+t.zone] = macs
	}
	return out
}
