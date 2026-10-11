package identity

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/store"
)

// Recognizer watches new private addresses for a while since habits take a few minutes to show
type Recognizer struct {
	Habits  *Habits
	Prints  *Prints
	Config  func() *config.Config
	Seen    func() map[string]devices.Seen
	Merge   func(newMAC string, m Match) // writes the config and raises the alert
	Suggest func(newMAC string, m Match) // tells the admin once
	KV      *store.KV                    // suggestions and "not the same" answers

	mu      sync.Mutex
	watch   map[string]time.Time
	state   map[string]*record
	started bool
}

type record struct {
	Suggest *Match   `json:"suggest,omitempty"`
	NotSame []string `json:"notSame,omitempty"`
}

const watchFor = 2 * time.Hour

func (r *Recognizer) init() {
	if r.started {
		return
	}
	r.started = true
	r.watch, r.state = map[string]time.Time{}, map[string]*record{}
	if r.KV != nil {
		r.KV.Each(func(mac string, v []byte) error {
			var rec record
			if json.Unmarshal(v, &rec) == nil {
				r.state[mac] = &rec
			}
			return nil
		})
	}
}

func mode(c *config.Config) string {
	if c.Settings.Recognize == "" {
		return "auto"
	}
	return c.Settings.Recognize
}

// Check scores a newcomer against every known device right now
func (r *Recognizer) Check(mac, hostname string, firstSeen time.Time) (Match, bool) {
	c := r.Config()
	if mode(c) == "off" || !Private(mac) {
		return Match{}, false
	}
	seen := r.Seen()
	owner := map[string]string{}
	for _, d := range c.Devices {
		owner[d.MAC] = d.MAC
		for _, o := range d.OldMACs {
			owner[o] = d.MAC
		}
	}
	r.mu.Lock()
	r.init()
	var notSame []string
	if rec := r.state[mac]; rec != nil {
		notSame = rec.NotSame
	}
	r.mu.Unlock()

	n := Newcomer{MAC: mac, Hostname: hostname, FirstSeen: firstSeen, Domains: r.Habits.Domains(mac)}
	if p, ok := r.Prints.Get(mac); ok {
		n.Print = p
		if n.Hostname == "" {
			n.Hostname = p.Hostname
		}
	}
	if n.Hostname == "" {
		n.Hostname = seen[mac].Hostname
	}
	var cands []Candidate
	for _, d := range c.Devices {
		if d.MAC == mac || slices.Contains(notSame, d.MAC) {
			continue
		}
		if _, err := net.ParseMAC(d.MAC); err != nil {
			continue // vpn peers have no mac
		}
		cd := Candidate{MAC: d.MAC, Name: d.Name, FirstSeen: d.FirstSeen, Hostnames: []string{d.Hostname}}
		if cd.Name == "" {
			cd.Name = d.Hostname
		}
		all := append([]string{d.MAC}, d.OldMACs...)
		for _, m := range all {
			s := seen[m]
			cd.Hostnames = append(cd.Hostnames, s.Hostname)
			if s.LastSeen.After(cd.LastSeen) {
				cd.LastSeen = s.LastSeen
			}
			if p, ok := r.Prints.Get(m); ok {
				cd.Hostnames = append(cd.Hostnames, p.Hostname)
				if p.At.After(cd.Print.At) {
					cd.Print = p
				}
			}
		}
		cd.Domains = r.Habits.Domains(all...)
		cands = append(cands, cd)
	}
	df, devices := r.Habits.DocFreq(func(m string) string {
		if o := owner[m]; o != "" {
			return o
		}
		return m
	})
	m, ok := Best(n, cands, df, devices)
	if ok && mode(c) != "auto" {
		m.Sure = false
	}
	return m, ok
}

// Watch keeps checking a newcomer that wasnt recognized straight away
func (r *Recognizer) Watch(mac string, firstSeen time.Time) {
	if !Private(mac) {
		return
	}
	r.mu.Lock()
	r.init()
	r.watch[mac] = firstSeen
	r.mu.Unlock()
}

func (r *Recognizer) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	save := time.NewTicker(10 * time.Minute)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			r.Habits.Save(time.Now())
			return
		case <-save.C:
			r.Habits.Save(time.Now())
		case <-tick.C:
			r.recheck(time.Now())
		}
	}
}

func (r *Recognizer) recheck(now time.Time) {
	r.mu.Lock()
	r.init()
	todo := map[string]time.Time{}
	for mac, at := range r.watch {
		if now.Sub(at) > watchFor {
			delete(r.watch, mac)
			continue
		}
		todo[mac] = at
	}
	r.mu.Unlock()
	c := r.Config()
	for mac, at := range todo {
		d := c.Device(mac)
		if d == nil || d.MAC != mac {
			r.Forget(mac) // removed, or already merged into another device
			continue
		}
		m, ok := r.Check(mac, d.Hostname, at)
		if !ok {
			continue
		}
		if m.Sure {
			r.Forget(mac)
			r.Merge(mac, m)
			continue
		}
		r.mu.Lock()
		rec := r.rec(mac)
		fresh := rec.Suggest == nil || rec.Suggest.MAC != m.MAC
		rec.Suggest = &m
		r.saveLocked(mac)
		r.mu.Unlock()
		if fresh && r.Suggest != nil {
			r.Suggest(mac, m)
		}
	}
}

func (r *Recognizer) rec(mac string) *record {
	rec := r.state[mac]
	if rec == nil {
		rec = &record{}
		r.state[mac] = rec
	}
	return rec
}

func (r *Recognizer) saveLocked(mac string) {
	if r.KV == nil {
		return
	}
	rec := r.state[mac]
	if rec == nil || (rec.Suggest == nil && len(rec.NotSame) == 0) {
		delete(r.state, mac)
		r.KV.Delete(mac)
		return
	}
	r.KV.Put(mac, rec)
}

// Suggestion is the device this address probably is, if theres a good guess
func (r *Recognizer) Suggestion(mac string) (Match, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if rec := r.state[mac]; rec != nil && rec.Suggest != nil {
		return *rec.Suggest, true
	}
	return Match{}, false
}

// NotSame remembers the admin said these are different devices
func (r *Recognizer) NotSame(mac, other string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	rec := r.rec(mac)
	if !slices.Contains(rec.NotSame, other) {
		rec.NotSame = append(rec.NotSame, other)
	}
	if rec.Suggest != nil && rec.Suggest.MAC == other {
		rec.Suggest = nil
	}
	r.saveLocked(mac)
}

// Forget drops everything about an address once its merged or removed
func (r *Recognizer) Forget(mac string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	delete(r.watch, mac)
	if rec := r.state[mac]; rec != nil {
		rec.Suggest = nil
		r.saveLocked(mac)
	}
}
