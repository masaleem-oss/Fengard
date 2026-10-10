// package welcome decides which new phones get the welcome page when they join
package welcome

import (
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/store"
)

// iphones ask this when they join wifi and show whatever comes back if its not apples success page
const ProbeHost = "captive.apple.com"

// a device that never taps join gets let through after this so nothing stays stuck
const giveUp = 10 * time.Minute

type Gate struct {
	cfg   *config.Store
	kv    *store.KV
	mu    sync.Mutex
	done  map[string]bool
	shown map[string]time.Time
	now   func() time.Time
}

type record struct {
	At time.Time `json:"at"`
}

func New(cfg *config.Store, kv *store.KV) *Gate {
	g := &Gate{cfg: cfg, kv: kv, done: map[string]bool{}, shown: map[string]time.Time{}, now: time.Now}
	if kv != nil {
		kv.Each(func(mac string, _ []byte) error { g.done[mac] = true; return nil })
	}
	return g
}

// Want is true for an iphone or ipad that turned up after the page was switched on
// and that nobody has named or put in a profile yet
func (g *Gate) Want(mac, hostname string) bool {
	if g == nil || mac == "" {
		return false
	}
	c := g.cfg.Get()
	s := c.Settings
	if !s.WelcomePage {
		return false
	}
	if d := c.Device(mac); d != nil {
		if d.Name != "" || (d.Group != "" && d.Group != s.DefaultGroup) || d.FirstSeen.Before(s.WelcomeSince) {
			return false
		}
		if hostname == "" {
			hostname = d.Hostname
		}
	}
	if !looksLikePhone(hostname, mac) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done[mac] {
		return false
	}
	first, ok := g.shown[mac]
	if !ok {
		g.shown[mac] = g.now()
		return true
	}
	if g.now().Sub(first) > giveUp {
		g.done[mac] = true
		delete(g.shown, mac)
		return false
	}
	return true
}

// only apple gear asks captive.apple.com so this just has to tell phones from macs tvs and homepods
// iphones on a rotating private address send no name at all
func looksLikePhone(hostname, mac string) bool {
	h := strings.ToLower(hostname)
	if strings.Contains(h, "iphone") || strings.Contains(h, "ipad") {
		return true
	}
	return h == "" && devices.IsRandomized(mac)
}

func (g *Gate) Accept(mac string) error {
	g.mu.Lock()
	g.done[mac] = true
	delete(g.shown, mac)
	g.mu.Unlock()
	if g.kv == nil {
		return nil
	}
	return g.kv.Put(mac, record{At: time.Now()})
}
