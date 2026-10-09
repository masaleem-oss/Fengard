package querylog

import (
	"encoding/json"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

type Entry struct {
	Time     time.Time `json:"time"`
	Client   string    `json:"client"`
	MAC      string    `json:"mac,omitempty"`
	Device   string    `json:"device,omitempty"`
	Group    string    `json:"group,omitempty"`
	Domain   string    `json:"domain"`
	Type     string    `json:"type"`
	Action   string    `json:"action"`
	Reason   string    `json:"reason,omitempty"`
	Category string    `json:"category,omitempty"`
	Cached   bool      `json:"cached,omitempty"`
	Ms       float64   `json:"ms"`
}

func (e *Entry) Blocked() bool { return e.Action != "allowed" && e.Action != "safesearch" }

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Hour struct {
	Time    time.Time `json:"time"`
	Total   int       `json:"total"`
	Blocked int       `json:"blocked"`
}

type Stats struct {
	Total      int     `json:"total"`
	Blocked    int     `json:"blocked"`
	Cached     int     `json:"cached"`
	TopBlocked []Count `json:"topBlocked"`
	TopDomains []Count `json:"topDomains"`
	TopDevices []Count `json:"topDevices"`
	Categories []Count `json:"categories"`
	AvgMs      float64 `json:"avgMs"`
	Hours      []Hour  `json:"hours"` // last 24h oldest first
	Dropped    uint64  `json:"droppedWrites"`
}

const (
	hours      = 24
	maxTopKeys = 20000 // cap keys so random subdomain floods cant eat memory
)

type Log struct {
	mu      sync.Mutex
	ring    []Entry
	next    int
	full    bool
	total   int
	blocked int
	cached  int
	byBlk   map[string]int
	byDom   map[string]int
	byDev   map[string]int
	byCat   map[string]int
	msSum   float64
	hours   [hours]Hour

	db      *store.Log
	hourly  *store.KV
	dirty   map[int64]bool // hours changed since last flush
	ch      chan Entry
	dropped atomic.Uint64
}

const keepHourlyDays = 31

func New(recent int, db *store.Log, hourly *store.KV) *Log {
	l := &Log{
		ring:   make([]Entry, recent),
		byBlk:  map[string]int{},
		byDom:  map[string]int{},
		byDev:  map[string]int{},
		byCat:  map[string]int{},
		db:     db,
		hourly: hourly,
		dirty:  map[int64]bool{},
	}
	if db != nil {
		l.restore()
		l.ch = make(chan Entry, 8192)
		go l.writer()
	}
	return l
}

func hourKey(t time.Time) string { return "h:" + strconv.FormatInt(t.Unix(), 10) }

func (l *Log) Series(days int) []Hour {
	end := time.Now().Truncate(time.Hour)
	start := end.Add(-time.Duration(days*24-1) * time.Hour)
	byTime := map[int64]Hour{}
	if l.hourly != nil {
		l.hourly.Each(func(k string, v []byte) error {
			var h Hour
			if json.Unmarshal(v, &h) == nil && !h.Time.Before(start) {
				byTime[h.Time.Unix()] = h
			}
			return nil
		})
	}
	l.mu.Lock()
	for _, h := range l.hours {
		if !h.Time.IsZero() && !h.Time.Before(start) {
			byTime[h.Time.Unix()] = h
		}
	}
	l.mu.Unlock()
	out := make([]Hour, 0, days*24)
	for t := start; !t.After(end); t = t.Add(time.Hour) {
		h, ok := byTime[t.Unix()]
		if !ok {
			h = Hour{Time: t}
		}
		out = append(out, h)
	}
	return out
}

func (l *Log) flushHours() {
	if l.hourly == nil {
		return
	}
	l.mu.Lock()
	var changed []Hour
	for unix := range l.dirty {
		if h := l.hours[unix/3600%hours]; h.Time.Unix() == unix {
			changed = append(changed, h)
		}
	}
	l.dirty = map[int64]bool{}
	l.mu.Unlock()
	for _, h := range changed {
		l.hourly.Put(hourKey(h.Time), h)
	}
}

// call on shutdown
func (l *Log) Flush() { l.flushHours() }

// reload the last 24h from disk so a restart doesnt wipe the dashboard
func (l *Log) restore() {
	cutoff := time.Now().Add(-hours * time.Hour)
	var entries []Entry
	l.db.Scan(time.Time{}, 300000, func(t time.Time, v []byte) bool {
		if t.Before(cutoff) {
			return false
		}
		var e Entry
		if json.Unmarshal(v, &e) == nil {
			entries = append(entries, e)
		}
		return true
	})
	for i := len(entries) - 1; i >= 0; i-- { // oldest first like live traffic
		l.add(entries[i])
	}
	// saved hour totals win since they survive pruning and the 300k cap
	if l.hourly != nil {
		l.mu.Lock()
		for i := range l.hours {
			if h := l.hours[i]; !h.Time.IsZero() {
				var saved Hour
				if ok, _ := l.hourly.Get(hourKey(h.Time), &saved); ok && saved.Total > h.Total {
					l.hours[i] = saved
				}
			}
		}
		l.dirty = map[int64]bool{}
		l.mu.Unlock()
	}
}

func (l *Log) Add(e Entry) {
	l.add(e)
	if l.ch != nil {
		select {
		case l.ch <- e:
		default:
			l.dropped.Add(1) // disk cant keep up dont slow dns down
		}
	}
}

func bump(m map[string]int, k string) {
	if _, ok := m[k]; ok || len(m) < maxTopKeys {
		m[k]++
	}
}

func (l *Log) add(e Entry) {
	l.mu.Lock()
	l.ring[l.next] = e
	l.next = (l.next + 1) % len(l.ring)
	if l.next == 0 {
		l.full = true
	}
	l.total++
	blocked := e.Blocked()
	if blocked {
		l.blocked++
		bump(l.byBlk, e.Domain)
		if e.Category != "" {
			l.byCat[e.Category]++
		}
	}
	l.msSum += e.Ms
	if e.Cached {
		l.cached++
	}
	bump(l.byDom, e.Domain)
	dev := e.Device
	if dev == "" {
		dev = e.Client
	}
	bump(l.byDev, dev)

	h := e.Time.Truncate(time.Hour)
	slot := &l.hours[h.Unix()/3600%hours]
	if !slot.Time.Equal(h) {
		*slot = Hour{Time: h}
	}
	slot.Total++
	if blocked {
		slot.Blocked++
	}
	l.dirty[h.Unix()] = true
	l.mu.Unlock()
}

func (l *Log) writer() {
	batch := make([]store.Record, 0, 512)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	hourTick := time.NewTicker(time.Minute)
	defer hourTick.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := l.db.Append(batch...); err != nil {
			log.Printf("query log write: %v", err)
			l.dropped.Add(uint64(len(batch)))
		}
		batch = batch[:0]
	}
	for {
		select {
		case e := <-l.ch:
			batch = append(batch, store.Record{Time: e.Time, Value: e})
			if len(batch) == cap(batch) {
				flush()
			}
		case <-tick.C:
			flush()
		case <-hourTick.C:
			l.flushHours()
		}
	}
}

func (l *Log) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	size := l.next
	if l.full {
		size = len(l.ring)
	}
	n = min(n, size)
	out := make([]Entry, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, l.ring[(l.next-i+len(l.ring))%len(l.ring)])
	}
	return out
}

func (l *Log) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().Truncate(time.Hour)
	hs := make([]Hour, hours)
	for i := range hours {
		t := now.Add(time.Duration(i-hours+1) * time.Hour)
		hs[i] = Hour{Time: t}
		if s := l.hours[t.Unix()/3600%hours]; s.Time.Equal(t) {
			hs[i] = s
		}
	}
	return Stats{
		Total: l.total, Blocked: l.blocked, Cached: l.cached,
		TopBlocked: top(l.byBlk, 10),
		TopDomains: top(l.byDom, 10),
		TopDevices: top(l.byDev, 10),
		Categories: top(l.byCat, 20),
		AvgMs:      l.msSum / float64(max(1, l.total)),
		Hours:      hs,
		Dropped:    l.dropped.Load(),
	}
}

type Filter struct {
	MAC     string
	Client  string
	Search  string
	Blocked bool
	Before  time.Time
	Limit   int
}

func (l *Log) History(f Filter) []Entry {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	out := []Entry{}
	if l.db == nil {
		return out
	}
	search := strings.ToLower(f.Search)
	l.db.Scan(f.Before, 500000, func(_ time.Time, v []byte) bool {
		var e Entry
		if json.Unmarshal(v, &e) != nil {
			return true
		}
		if (f.MAC != "" && e.MAC != f.MAC) || (f.Client != "" && e.Client != f.Client) ||
			(f.Blocked && !e.Blocked()) || (search != "" && !strings.Contains(e.Domain, search)) {
			return true
		}
		out = append(out, e)
		return len(out) < f.Limit
	})
	return out
}

func (l *Log) Prune(days int) {
	if l.db == nil {
		return
	}
	if n, err := l.db.Prune(time.Now().AddDate(0, 0, -days)); err != nil {
		log.Printf("query log prune: %v", err)
	} else if n > 0 {
		log.Printf("query log: pruned %d entries older than %d days", n, days)
	}
	if l.hourly != nil {
		cutoff := time.Now().AddDate(0, 0, -keepHourlyDays)
		var old []string
		l.hourly.Each(func(k string, v []byte) error {
			var h Hour
			if json.Unmarshal(v, &h) == nil && h.Time.Before(cutoff) {
				old = append(old, k)
			}
			return nil
		})
		for _, k := range old {
			l.hourly.Delete(k)
		}
	}
}

func top(m map[string]int, n int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out[:min(n, len(out))]
}
