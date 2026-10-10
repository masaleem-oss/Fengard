// a minute only counts if a device looked up ActiveThreshold names in it
package screentime

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math/bits"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

const minutesPerDay = 24 * 60

// one bit per minute of the day
type minuteSet [(minutesPerDay + 63) / 64]uint64

func (m *minuteSet) set(i int) bool {
	w, b := i/64, uint64(1)<<(i%64)
	if m[w]&b != 0 {
		return false
	}
	m[w] |= b
	return true
}

func (m *minuteSet) count() int {
	n := 0
	for _, w := range m {
		n += bits.OnesCount64(w)
	}
	return n
}

type device struct {
	Group   string                `json:"group"`
	Total   minuteSet             `json:"total"`
	Targets map[string]*minuteSet `json:"targets"`

	minute   int
	curNames map[uint64]struct{} // distinct names this minute
}

type group struct {
	used  map[string]int // empty key is the total
	dirty bool
	bonus int
}

type Tracker struct {
	// distinct names a minute needs to count default 3
	ActiveThreshold int

	flushMu sync.Mutex
	mu      sync.Mutex
	loc     *time.Location
	day     string
	devices map[string]*device // by identity
	groups  map[string]*group
	kv      *store.KV
	changed bool
}

const maxDevices = 512

func New(kv *store.KV, loc *time.Location) *Tracker {
	if loc == nil {
		loc = time.Local
	}
	t := &Tracker{ActiveThreshold: 3, loc: loc, kv: kv, devices: map[string]*device{}, groups: map[string]*group{}}
	t.day = time.Now().In(loc).Format("2006-01-02")
	t.restore()
	return t
}

func (t *Tracker) SetLocation(loc *time.Location) {
	if loc == nil {
		return
	}
	t.mu.Lock()
	t.loc = loc
	t.mu.Unlock()
}

// resets at local midnight and caller holds t.mu
func (t *Tracker) rollover(now time.Time) {
	day := now.In(t.loc).Format("2006-01-02")
	if day == t.day {
		return
	}
	t.day = day
	t.devices = map[string]*device{}
	for _, g := range t.groups {
		g.used, g.bonus, g.dirty = map[string]int{}, 0, false
	}
	t.changed = true
}

func (t *Tracker) Note(identity, groupID, domain string, targets []string, now time.Time) {
	if identity == "" || groupID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover(now)
	local := now.In(t.loc)
	minute := local.Hour()*60 + local.Minute()

	d := t.devices[identity]
	if d == nil {
		if len(t.devices) >= maxDevices {
			return
		}
		d = &device{Targets: map[string]*minuteSet{}, minute: -1}
		t.devices[identity] = d
	}
	g := t.groups[groupID]
	if g == nil {
		g = &group{used: map[string]int{}}
		t.groups[groupID] = g
	}
	if d.Group != groupID {
		if old := t.groups[d.Group]; old != nil {
			old.dirty = true
		}
		d.Group = groupID
		g.dirty = true
	}
	if d.minute != minute {
		d.minute, d.curNames = minute, map[uint64]struct{}{}
	}
	if len(d.curNames) < 64 {
		h := fnv.New64a()
		h.Write([]byte(domain))
		d.curNames[h.Sum64()] = struct{}{}
	}
	if len(d.curNames) >= t.ActiveThreshold && d.Total.set(minute) {
		g.dirty, t.changed = true, true
	}
	for _, tg := range targets {
		ms := d.Targets[tg]
		if ms == nil {
			if len(d.Targets) >= 64 {
				continue
			}
			ms = &minuteSet{}
			d.Targets[tg] = ms
		}
		if ms.set(minute) {
			g.dirty, t.changed = true, true
		}
	}
}

// caller holds t.mu
func (t *Tracker) recompute(id string, g *group) {
	var total minuteSet
	targets := map[string]*minuteSet{}
	for _, d := range t.devices {
		if d.Group != id {
			continue
		}
		for i := range total {
			total[i] |= d.Total[i]
		}
		for tg, ms := range d.Targets {
			u := targets[tg]
			if u == nil {
				u = &minuteSet{}
				targets[tg] = u
			}
			for i := range u {
				u[i] |= ms[i]
			}
		}
	}
	g.used = map[string]int{"": total.count()}
	for tg, ms := range targets {
		g.used[tg] = ms.count()
	}
	g.dirty = false
}

// empty target means online at all
func (t *Tracker) Used(groupID, target string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover(time.Now())
	g := t.groups[groupID]
	if g == nil {
		return 0
	}
	if g.dirty {
		t.recompute(groupID, g)
	}
	return g.used[target]
}

func (t *Tracker) Bonus(groupID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover(time.Now())
	if g := t.groups[groupID]; g != nil {
		return g.bonus
	}
	return 0
}

// negative takes minutes back
func (t *Tracker) AddBonus(groupID string, minutes int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rollover(time.Now())
	g := t.groups[groupID]
	if g == nil {
		g = &group{used: map[string]int{}}
		t.groups[groupID] = g
	}
	g.bonus = max(0, g.bonus+minutes)
	t.changed = true
	return g.bonus
}

func (t *Tracker) DeviceMinutes(groupID string) map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]int{}
	for id, d := range t.devices {
		if d.Group == groupID {
			out[id] = d.Total.count()
		}
	}
	return out
}

func (t *Tracker) Targets(groupID string) map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	g := t.groups[groupID]
	if g == nil {
		return map[string]int{}
	}
	if g.dirty {
		t.recompute(groupID, g)
	}
	out := make(map[string]int, len(g.used))
	for k, v := range g.used {
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// todays counters survive a restart

type savedDevice struct {
	Group   string               `json:"group"`
	Total   minuteSet            `json:"total"`
	Targets map[string]minuteSet `json:"targets"`
}

func (t *Tracker) restore() {
	if t.kv == nil {
		return
	}
	prefix := "d:" + t.day + ":"
	t.kv.Each(func(key string, data []byte) error {
		switch {
		case strings.HasPrefix(key, prefix):
			var sd savedDevice
			if json.Unmarshal(data, &sd) == nil {
				d := &device{Group: sd.Group, Total: sd.Total, Targets: map[string]*minuteSet{}, minute: -1}
				for k, v := range sd.Targets {
					ms := v
					d.Targets[k] = &ms
				}
				id := strings.TrimPrefix(key, prefix)
				t.devices[id] = d
				if t.groups[sd.Group] == nil {
					t.groups[sd.Group] = &group{used: map[string]int{}}
				}
				t.groups[sd.Group].dirty = true
			}
		case strings.HasPrefix(key, "b:"+t.day+":"):
			var n int
			if json.Unmarshal(data, &n) == nil {
				id := strings.TrimPrefix(key, "b:"+t.day+":")
				if t.groups[id] == nil {
					t.groups[id] = &group{used: map[string]int{}}
				}
				t.groups[id].bonus = n
			}
		}
		return nil
	})
}

func (t *Tracker) Flush() error {
	if t.kv == nil {
		return nil
	}
	t.flushMu.Lock()
	defer t.flushMu.Unlock()
	t.mu.Lock()
	if !t.changed {
		t.mu.Unlock()
		return nil
	}
	t.changed = false
	values := make(map[string]any, len(t.devices)+len(t.groups))
	for id, d := range t.devices {
		sd := savedDevice{Group: d.Group, Total: d.Total, Targets: map[string]minuteSet{}}
		for target, minutes := range d.Targets {
			sd.Targets[target] = *minutes
		}
		values[fmt.Sprintf("d:%s:%s", t.day, id)] = sd
	}
	for id, g := range t.groups {
		values[fmt.Sprintf("b:%s:%s", t.day, id)] = g.bonus
	}
	t.mu.Unlock()
	if err := t.kv.ReplacePrefix("", values); err != nil {
		t.mu.Lock()
		t.changed = true
		t.mu.Unlock()
		return err
	}
	return nil
}

func (t *Tracker) Run(ctx context.Context) {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := t.Flush(); err != nil {
				log.Printf("screen time persistence: %v", err)
			}
			return
		case <-tk.C:
			if err := t.Flush(); err != nil {
				log.Printf("screen time persistence: %v", err)
			}
		}
	}
}
