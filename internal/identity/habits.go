package identity

import (
	"encoding/json"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

// Habits keeps the sites each device looked up lately, by site not full name
type Habits struct {
	KV *store.KV

	mu    sync.Mutex
	byMAC map[string]map[string]int64 // site to unix minute last looked up
	dirty bool
}

const (
	perDevice  = 400
	maxTracked = 2048
	keepHabits = 14 * 24 * time.Hour
)

func NewHabits(kv *store.KV) *Habits {
	h := &Habits{KV: kv, byMAC: map[string]map[string]int64{}}
	if kv != nil {
		kv.Each(func(mac string, v []byte) error {
			m := map[string]int64{}
			if json.Unmarshal(v, &m) == nil {
				h.byMAC[mac] = m
			}
			return nil
		})
	}
	return h
}

// Note is on the dns hot path so it only touches a map
func (h *Habits) Note(mac, domain string, now time.Time) {
	site := Site(domain)
	if mac == "" || site == "" {
		return
	}
	min := now.Unix() / 60
	h.mu.Lock()
	defer h.mu.Unlock()
	m := h.byMAC[mac]
	if m == nil {
		if len(h.byMAC) >= maxTracked {
			return
		}
		m = map[string]int64{}
		h.byMAC[mac] = m
	}
	if m[site] == min {
		return
	}
	m[site] = min
	h.dirty = true
	if len(m) > perDevice+perDevice/4 {
		trim(m, perDevice)
	}
}

// drops the sites not looked up for the longest
func trim(m map[string]int64, keep int) {
	for len(m) > keep {
		var oldest string
		var at int64 = 1 << 62
		for s, t := range m {
			if t < at {
				oldest, at = s, t
			}
		}
		delete(m, oldest)
	}
}

// Domains is every site these addresses looked up
func (h *Habits) Domains(macs ...string) map[string]bool {
	out := map[string]bool{}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, mac := range macs {
		for s := range h.byMAC[mac] {
			out[s] = true
		}
	}
	return out
}

// DocFreq counts how many devices looked up each site, owner groups a device's addresses
func (h *Habits) DocFreq(owner func(mac string) string) (map[string]int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]map[string]bool{}
	for mac, m := range h.byMAC {
		who := owner(mac)
		for s := range m {
			if seen[s] == nil {
				seen[s] = map[string]bool{}
			}
			seen[s][who] = true
		}
	}
	owners := map[string]bool{}
	for mac := range h.byMAC {
		owners[owner(mac)] = true
	}
	df := make(map[string]int, len(seen))
	for s, w := range seen {
		df[s] = len(w)
	}
	return df, len(owners)
}

// Save writes what changed and forgets old sites and quiet devices
func (h *Habits) Save(now time.Time) {
	h.mu.Lock()
	if h.KV == nil || !h.dirty {
		h.mu.Unlock()
		return
	}
	cut := now.Add(-keepHabits).Unix() / 60
	vals := map[string]any{}
	for mac, m := range h.byMAC {
		for s, t := range m {
			if t < cut {
				delete(m, s)
			}
		}
		if len(m) == 0 {
			delete(h.byMAC, mac)
			continue
		}
		vals[mac] = maps.Clone(m)
	}
	h.dirty = false
	h.mu.Unlock()
	// flash can be slow so write without holding up dns
	if h.KV.ReplacePrefix("", vals) != nil {
		h.mu.Lock()
		h.dirty = true
		h.mu.Unlock()
	}
}

// Site cuts a name down to the part a company registers, eg api.tiktokv.com is tiktokv.com
func Site(domain string) string {
	d := strings.TrimSuffix(strings.ToLower(domain), ".")
	if d == "" || !strings.Contains(d, ".") || strings.HasSuffix(d, ".arpa") || strings.HasSuffix(d, ".lan") ||
		strings.HasSuffix(d, ".local") || strings.HasSuffix(d, ".home.arpa") {
		return ""
	}
	// walk back over dots, no split since this runs on every lookup
	last := strings.LastIndexByte(d, '.')
	second := strings.LastIndexByte(d[:last], '.')
	if second < 0 {
		return d
	}
	if len(d)-last-1 == 2 {
		switch d[second+1 : last] {
		case "co", "com", "org", "net", "ac", "gov", "edu", "ne", "or":
			return d[strings.LastIndexByte(d[:second], '.')+1:]
		}
	}
	return d[second+1:]
}
