package alerts

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/masaleem-oss/Fengard/internal/ratelimit"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

type Alert struct {
	Time     time.Time `json:"time"`
	Kind     string    `json:"kind"`
	Severity Severity  `json:"severity"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail,omitempty"`
	MAC      string    `json:"mac,omitempty"`
	Domain   string    `json:"domain,omitempty"`
}

type Alerts struct {
	db           *store.Log
	requestLimit *ratelimit.Limiter
	dropped      atomic.Uint64

	OnRaise func(Alert)

	mu    sync.Mutex
	sweep time.Time
	last  map[string]time.Time // dedupe key to last raised
}

const maxDedupeKeys = 10000

func New(db *store.Log) *Alerts {
	return &Alerts{db: db, last: map[string]time.Time{}, requestLimit: ratelimit.New(1, 30, 1)}
}

func (a *Alerts) Dropped() uint64 { return a.dropped.Load() }

// skipped if the same key was raised within quiet
func (a *Alerts) Raise(key string, quiet time.Duration, al Alert) bool {
	request := al.Kind == "access_request" || al.Kind == "time_request"
	if request && !a.requestLimit.Allow("") {
		a.dropped.Add(1)
		return false
	}
	now := time.Now()
	a.mu.Lock()
	if t, ok := a.last[key]; ok && now.Sub(t) < quiet {
		a.mu.Unlock()
		return false
	}
	limit := maxDedupeKeys
	if request {
		limit -= 128
	}
	if _, exists := a.last[key]; !exists && len(a.last) >= limit {
		if now.Sub(a.sweep) >= time.Minute {
			a.sweep = now
			for k, t := range a.last {
				if now.Sub(t) > 24*time.Hour {
					delete(a.last, k)
				}
			}
		}
		if len(a.last) >= limit && request {
			a.dropped.Add(1)
			a.mu.Unlock()
			return false
		}
		// forget the oldest key so a flood cant hide a real new alert
		if len(a.last) >= limit {
			oldest, at := "", now
			for k, t := range a.last {
				if t.Before(at) {
					oldest, at = k, t
				}
			}
			delete(a.last, oldest)
		}
	}
	a.last[key] = now
	a.mu.Unlock()

	al.Time = now
	if a.db != nil {
		if err := a.db.Append(store.Record{Time: now, Value: al}); err != nil {
			a.dropped.Add(1)
		}
	}
	if a.OnRaise != nil {
		a.OnRaise(al)
	}
	return true
}

func (a *Alerts) List(limit int, before time.Time) []Alert {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := []Alert{}
	a.db.Scan(before, limit*4, func(_ time.Time, v []byte) bool {
		var al Alert
		if json.Unmarshal(v, &al) == nil {
			out = append(out, al)
		}
		return len(out) < limit
	})
	return out
}

func (a *Alerts) Prune(days int) { a.db.Prune(time.Now().AddDate(0, 0, -days)) }
