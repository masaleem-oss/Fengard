package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	rate    float64 // tokens per second
	burst   float64
	maxKeys int

	mu      sync.Mutex
	buckets map[string]*bucket
	sweep   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func New(ratePerSec, burst float64, maxKeys int) *Limiter {
	return &Limiter{rate: ratePerSec, burst: burst, maxKeys: maxKeys, buckets: map[string]*bucket{}}
}

func (l *Limiter) SetRate(ratePerSec, burst float64) {
	l.mu.Lock()
	l.rate, l.burst = ratePerSec, burst
	l.mu.Unlock()
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= l.maxKeys {
			l.evict(now)
			if len(l.buckets) >= l.maxKeys {
				// still full so refuse new keys instead of growing forever
				return false
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// drops idle buckets at most once a second so its cheap under a flood
func (l *Limiter) evict(now time.Time) {
	if now.Sub(l.sweep) < time.Second {
		return
	}
	l.sweep = now
	idle := time.Duration(l.burst/l.rate*float64(time.Second)) + time.Second
	for k, b := range l.buckets {
		if now.Sub(b.last) > idle {
			delete(l.buckets, k)
		}
	}
}

func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
