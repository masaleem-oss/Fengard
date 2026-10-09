package dnsserver

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// bounded cache that can serve stale answers when every upstream is down
type cache struct {
	mu      sync.Mutex
	max     int
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	packed  []byte
	stored  time.Time
	expires time.Time
}

const (
	maxCacheTTL = time.Hour
	minCacheTTL = 5 * time.Second
	negTTL      = 30 * time.Second
	staleFor    = 24 * time.Hour
)

func newCache(max int) *cache {
	return &cache{max: max, entries: make(map[string]*cacheEntry, max/4)}
}

func cacheKey(q dns.Question, do bool) string {
	var b strings.Builder
	b.Grow(len(q.Name) + 12)
	b.WriteString(strings.ToLower(q.Name))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(int(q.Qtype)))
	if do {
		b.WriteString("|do")
	}
	return b.String()
}

// ttls get cut by age and stale=true also returns expired ones
func (c *cache) get(key string, now time.Time, stale bool) *dns.Msg {
	c.mu.Lock()
	e := c.entries[key]
	c.mu.Unlock()
	if e == nil {
		return nil
	}
	if now.After(e.expires) && (!stale || now.Sub(e.expires) > staleFor) {
		return nil
	}
	m := new(dns.Msg)
	if m.Unpack(e.packed) != nil {
		return nil
	}
	age := uint32(now.Sub(e.stored).Seconds())
	for _, rrs := range [][]dns.RR{m.Answer, m.Ns, m.Extra} {
		for _, rr := range rrs {
			h := rr.Header()
			if h.Rrtype == dns.TypeOPT {
				continue
			}
			if h.Ttl > age {
				h.Ttl -= age
			} else {
				h.Ttl = 1
			}
		}
	}
	return m
}

func (c *cache) put(key string, m *dns.Msg, now time.Time) {
	if m.Truncated || (m.Rcode != dns.RcodeSuccess && m.Rcode != dns.RcodeNameError) {
		return
	}
	ttl := negTTL
	if len(m.Answer) > 0 {
		ttl = maxCacheTTL
		for _, rr := range m.Answer {
			if t := time.Duration(rr.Header().Ttl) * time.Second; t < ttl {
				ttl = t
			}
		}
	}
	if ttl < minCacheTTL {
		ttl = minCacheTTL
	}
	packed, err := m.Pack()
	if err != nil {
		return
	}
	c.mu.Lock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.max {
		for k := range c.entries { // map order is random
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = &cacheEntry{packed: packed, stored: now, expires: now.Add(ttl)}
	c.mu.Unlock()
}

func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *cache) flush() {
	c.mu.Lock()
	c.entries = make(map[string]*cacheEntry, c.max/4)
	c.mu.Unlock()
}
