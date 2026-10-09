package alerts

import (
	"encoding/json"
	"sync"
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
	db *store.Log

	OnRaise func(Alert)

	mu   sync.Mutex
	last map[string]time.Time // dedupe key to last raised
}

func New(db *store.Log) *Alerts { return &Alerts{db: db, last: map[string]time.Time{}} }

// skipped if the same key was raised within quiet
func (a *Alerts) Raise(key string, quiet time.Duration, al Alert) bool {
	now := time.Now()
	a.mu.Lock()
	if t, ok := a.last[key]; ok && now.Sub(t) < quiet {
		a.mu.Unlock()
		return false
	}
	if len(a.last) > 10000 {
		for k, t := range a.last {
			if now.Sub(t) > 24*time.Hour {
				delete(a.last, k)
			}
		}
	}
	a.last[key] = now
	a.mu.Unlock()

	al.Time = now
	a.db.Append(store.Record{Time: now, Value: al})
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
