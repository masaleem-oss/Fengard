// package integrity takes a baseline of what's on the router and flags what changed since
// it only notices, it never undoes anything
package integrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

const (
	Packages = "packages"
	Files    = "files"
	SSHKeys  = "ssh"
	Accounts = "accounts"
	Startup  = "startup"
	Ports    = "ports"
	Exposure = "exposure"
)

// Watches in the order the dashboard shows them
var Watches = []string{Packages, Files, SSHKeys, Accounts, Startup, Ports, Exposure}

type Snapshot struct {
	Time     time.Time `json:"time"`
	Firmware string    `json:"firmware"`
	// a watch that's missing was never collected, an empty one had nothing in it
	Watches map[string]map[string]string `json:"watches"`
}

type Change struct {
	ID        string    `json:"id"`
	Watch     string    `json:"watch"`
	Kind      string    `json:"kind"` // added, removed or changed
	Item      string    `json:"item"`
	What      string    `json:"what"`
	Before    string    `json:"before,omitempty"`
	After     string    `json:"after,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
}

type Result struct {
	Time      time.Time         `json:"time,omitzero"`
	Took      time.Duration     `json:"took,omitempty"`
	Changes   []Change          `json:"changes"`
	Errors    map[string]string `json:"errors,omitempty"` // watches that couldn't be read
	Truncated bool              `json:"truncated,omitempty"`
}

type Status struct {
	Available bool              `json:"available"`
	Running   bool              `json:"running"`
	Baseline  time.Time         `json:"baseline,omitzero"`
	Firmware  string            `json:"firmware,omitempty"`
	LastCheck time.Time         `json:"lastCheck,omitzero"`
	Changes   []Change          `json:"changes"`
	Errors    map[string]string `json:"errors,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

type Checker struct {
	Collector *Collector
	KV        *store.KV
	// false on a computer, the watches only make sense on a router
	Available bool
	Enabled   func() bool
	// Watching says if a watch is turned on, nil watches everything
	Watching func(watch string) bool
	Location func() *time.Location
	Hour     int // nightly check hour, 0 means 2 am
	// OnChanges gets the changes when a check finds something not seen before
	OnChanges  func(all []Change, fresh []Change)
	OnFirmware func(from, to string)

	mu       sync.Mutex // guards the fields below
	running  bool
	loaded   bool
	baseline *Snapshot
	last     Result
	kick     chan struct{}

	stateMu sync.Mutex // one baseline edit at a time, check or approve
}

func (c *Checker) init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kick == nil {
		c.kick = make(chan struct{}, 1)
	}
	if !c.loaded {
		c.loaded = true
		if c.KV != nil {
			var b Snapshot
			if ok, _ := c.KV.Get("baseline", &b); ok {
				c.baseline = &b
			}
			c.KV.Get("last", &c.last)
		}
	}
}

func (c *Checker) on() bool { return c.Available && (c.Enabled == nil || c.Enabled()) }

func (c *Checker) watching(w string) bool { return c.Watching == nil || c.Watching(w) }

func (c *Checker) Run(ctx context.Context) {
	c.init()
	if !c.Available {
		return
	}
	hour := c.Hour
	if hour == 0 {
		hour = 2
	}
	// a random minute so a slow flash isn't busy at the same time as other nightly work
	minute := rand.IntN(50)
	wait := 5 * time.Minute // the first baseline soon after install
	if !c.Status().Baseline.IsZero() {
		wait = time.Minute
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
			c.check(ctx)
		case <-t.C:
			if c.on() && (c.Status().Baseline.IsZero() || c.due(hour, minute)) {
				c.check(ctx)
			}
			t.Reset(5 * time.Minute)
		}
	}
}

func (c *Checker) due(hour, minute int) bool {
	loc := time.Local
	if c.Location != nil {
		loc = c.Location()
	}
	now := time.Now().In(loc)
	if now.Hour() != hour || now.Minute() < minute {
		return false
	}
	last := c.Status().LastCheck.In(loc)
	return last.YearDay() != now.YearDay() || last.Year() != now.Year()
}

// check runs at low priority on its own thread so dns never waits on hashing
func (c *Checker) check(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		lowPriority()
		c.Check(ctx)
	}()
	<-done
}

// Trigger asks for a check now, false if one is already going or it's off
func (c *Checker) Trigger() bool {
	c.init()
	if !c.Available {
		return false
	}
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if running {
		return false
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
	return true
}

func (c *Checker) Status() Status {
	c.init()
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{Available: c.Available, Running: c.running, LastCheck: c.last.Time, Changes: c.last.Changes,
		Errors: c.last.Errors, Truncated: c.last.Truncated}
	if st.Changes == nil {
		st.Changes = []Change{}
	}
	if c.baseline != nil {
		st.Baseline, st.Firmware = c.baseline.Time, c.baseline.Firmware
	}
	return st
}

// Check collects a snapshot and compares it to the baseline, the first one becomes the baseline
func (c *Checker) Check(ctx context.Context) Result {
	c.init()
	c.mu.Lock()
	if c.running {
		r := c.last
		c.mu.Unlock()
		return r
	}
	c.running = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	started := time.Now()
	cur := Snapshot{Time: started, Firmware: c.Collector.Firmware(), Watches: map[string]map[string]string{}}
	errs := map[string]string{}
	truncated := false
	for _, w := range Watches {
		if !c.watching(w) {
			continue
		}
		m, cut, err := c.Collector.Collect(ctx, w)
		if ctx.Err() != nil {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.last
		}
		if err != nil {
			errs[w] = err.Error()
			continue
		}
		truncated = truncated || cut
		cur.Watches[w] = m
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.mu.Lock()
	base, prev := c.baseline, c.last
	c.mu.Unlock()

	res := Result{Time: time.Now(), Took: time.Since(started).Round(time.Millisecond), Errors: errs, Truncated: truncated}
	if len(res.Errors) == 0 {
		res.Errors = nil
	}
	var rebased string
	switch {
	case base == nil:
		base = &cur
	case cur.Firmware != base.Firmware && base.Firmware != "" && cur.Firmware != "":
		// an upgrade changes nearly everything so start again rather than flood the owner
		rebased = base.Firmware
		base = &cur
	default:
		nb := Snapshot{Time: base.Time, Firmware: base.Firmware, Watches: map[string]map[string]string{}}
		for w, m := range base.Watches {
			nb.Watches[w] = m
		}
		if nb.Firmware == "" {
			nb.Firmware = cur.Firmware
		}
		// a watch turned on later starts from what's there now
		for w, m := range cur.Watches {
			if _, ok := nb.Watches[w]; !ok {
				nb.Watches[w] = m
			}
		}
		base = &nb
		res.Changes = diff(base, &cur, c.Collector)
	}
	if res.Changes == nil {
		res.Changes = []Change{}
	}
	seen := map[string]time.Time{}
	for _, ch := range prev.Changes {
		seen[ch.ID] = ch.FirstSeen
	}
	var fresh []Change
	for i := range res.Changes {
		if t, ok := seen[res.Changes[i].ID]; ok {
			res.Changes[i].FirstSeen = t
		} else {
			res.Changes[i].FirstSeen = res.Time
			fresh = append(fresh, res.Changes[i])
		}
	}
	c.save(base, res)
	log.Printf("security check: %d changes, took %s", len(res.Changes), res.Took)
	if rebased != "" && c.OnFirmware != nil {
		c.OnFirmware(rebased, cur.Firmware)
	}
	if len(fresh) > 0 && c.OnChanges != nil {
		c.OnChanges(res.Changes, fresh)
	}
	return res
}

func (c *Checker) save(base *Snapshot, res Result) {
	c.mu.Lock()
	c.baseline, c.last = base, res
	c.mu.Unlock()
	if c.KV != nil {
		if err := c.KV.Put("baseline", base); err != nil {
			log.Printf("security check: saving the baseline: %v", err)
		}
		if err := c.KV.Put("last", res); err != nil {
			log.Printf("security check: %v", err)
		}
	}
}

// Approve takes changes into the baseline, no ids and all false does nothing
func (c *Checker) Approve(ids []string, all bool) int {
	c.init()
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.mu.Lock()
	base, last := c.baseline, c.last
	c.mu.Unlock()
	if base == nil {
		return 0
	}
	nb := Snapshot{Time: base.Time, Firmware: base.Firmware, Watches: map[string]map[string]string{}}
	for w, m := range base.Watches {
		nb.Watches[w] = m
	}
	copied := map[string]bool{}
	keep := []Change{}
	n := 0
	for _, ch := range last.Changes {
		if !all && !slices.Contains(ids, ch.ID) {
			keep = append(keep, ch)
			continue
		}
		n++
		if !copied[ch.Watch] {
			m := make(map[string]string, len(nb.Watches[ch.Watch]))
			for k, v := range nb.Watches[ch.Watch] {
				m[k] = v
			}
			nb.Watches[ch.Watch], copied[ch.Watch] = m, true
		}
		if ch.Kind == "removed" {
			delete(nb.Watches[ch.Watch], ch.Item)
		} else {
			nb.Watches[ch.Watch][ch.Item] = ch.After
		}
	}
	if n == 0 {
		return 0
	}
	nb.Time = time.Now()
	last.Changes = keep
	c.save(&nb, last)
	return n
}

func diff(base, cur *Snapshot, col *Collector) []Change {
	var out []Change
	var newFiles []string
	for _, w := range Watches {
		now, ok := cur.Watches[w]
		if !ok {
			continue // turned off or unreadable this time, not gone
		}
		was := base.Watches[w]
		var keys []string
		for k := range now {
			keys = append(keys, k)
		}
		for k := range was {
			if _, ok := now[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			before, had := was[k]
			after, has := now[k]
			kind := "changed"
			switch {
			case had && has && same(w, before, after):
				continue
			case !had:
				kind = "added"
			case !has:
				kind = "removed"
			}
			ch := Change{ID: changeID(w, k, before, after, kind), Watch: w, Kind: kind, Item: k, Before: before, After: after}
			if w == Files && kind == "added" {
				newFiles = append(newFiles, k)
			}
			out = append(out, ch)
		}
	}
	owners := map[string]string{}
	if col != nil {
		owners = col.owners(newFiles)
	}
	for i := range out {
		out[i].What = describe(out[i], owners[out[i].Item])
	}
	return out
}

// same ignores a port's program going missing, it can't always be read
func same(watch, a, b string) bool {
	if watch == Ports && (a == "" || b == "") {
		return true
	}
	return a == b
}

func changeID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}
