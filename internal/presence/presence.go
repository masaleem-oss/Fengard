// package presence works out whos home from which phones are on the wifi
package presence

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Device struct {
	MAC   string    `json:"mac"`
	Home  bool      `json:"home"`
	Since time.Time `json:"since,omitzero"`
}

type Watcher struct {
	// Tracked lists the macs someone wants arrival and leaving alerts for
	Tracked  func() []string
	OnChange func(mac string, home bool, at time.Time)
	// Clients returns the macs connected to the wifi right now
	Clients func(ctx context.Context) (map[string]bool, error)
	Every   time.Duration
	// phones drop off the wifi for a bit when asleep so leaving waits this long
	Away time.Duration
	// false while the add-on isn't installed, nothing gets polled then
	Enabled func() bool

	mu        sync.Mutex
	state     map[string]*Device
	lastSeen  map[string]time.Time
	available bool
}

func (w *Watcher) Run(ctx context.Context) {
	if w.Clients == nil {
		w.Clients = Wifi
	}
	every := w.Every
	if every == 0 {
		every = 20 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		w.Check(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Watcher) Check(ctx context.Context, now time.Time) {
	if w.Enabled != nil && !w.Enabled() {
		w.mu.Lock()
		w.state, w.lastSeen, w.available = nil, nil, false
		w.mu.Unlock()
		return
	}
	var tracked []string
	if w.Tracked != nil {
		tracked = w.Tracked()
	}
	// nobody to watch, so leave the wifi driver alone once we know it answers
	if len(tracked) == 0 {
		w.mu.Lock()
		known := w.available
		if known {
			w.state, w.lastSeen = nil, nil
		}
		w.mu.Unlock()
		if known {
			return
		}
	}
	clients, err := w.Clients(ctx)
	if err != nil {
		w.mu.Lock()
		w.available = false
		w.mu.Unlock()
		return
	}
	away := w.Away
	if away == 0 {
		away = 10 * time.Minute
	}
	type change struct {
		mac  string
		home bool
		at   time.Time
	}
	var changes []change
	w.mu.Lock()
	w.available = true
	if w.state == nil {
		w.state, w.lastSeen = map[string]*Device{}, map[string]time.Time{}
	}
	keep := map[string]bool{}
	for _, mac := range tracked {
		mac = strings.ToLower(mac)
		keep[mac] = true
		on := clients[mac]
		if on {
			w.lastSeen[mac] = now
		}
		d, known := w.state[mac]
		if !known {
			// the first look just learns where everyone is without alerting
			w.state[mac] = &Device{MAC: mac, Home: on, Since: now}
			continue
		}
		switch {
		case on && !d.Home:
			d.Home, d.Since = true, now
			changes = append(changes, change{mac, true, now})
		case !on && d.Home && now.Sub(w.lastSeen[mac]) >= away:
			d.Home, d.Since = false, w.lastSeen[mac]
			changes = append(changes, change{mac, false, d.Since})
		}
	}
	for mac := range w.state {
		if !keep[mac] {
			delete(w.state, mac)
		}
	}
	w.mu.Unlock()
	if w.OnChange != nil {
		for _, c := range changes {
			w.OnChange(c.mac, c.home, c.at)
		}
	}
}

func (w *Watcher) Available() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.available
}

func (w *Watcher) Devices() []Device {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []Device{}
	for _, d := range w.state {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out
}

var macRe = regexp.MustCompile(`^([0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5})\s`)

// Wifi asks iwinfo which clients are on each access point the router runs
func Wifi(ctx context.Context) (map[string]bool, error) {
	aps, err := apList(ctx)
	if err != nil {
		return nil, err
	}
	clients := map[string]bool{}
	for _, iface := range aps {
		if !isUp(iface) {
			continue
		}
		list, err := exec.CommandContext(ctx, "iwinfo", iface, "assoclist").Output()
		if err != nil {
			continue
		}
		for mac := range ParseAssoc(string(list)) {
			clients[mac] = true
		}
	}
	return clients, nil
}

// iwinfo with no interface asks every wifi interface, and mediatek drivers log an error
// for each one that's down, so the access points are looked up rarely and down ones skipped
var aps struct {
	sync.Mutex
	list []string
	at   time.Time
}

// a router with no access point yet, like guest wifi still off, gets looked at again sooner
const (
	apRefresh      = time.Hour
	apRefreshEmpty = 5 * time.Minute
)

func apList(ctx context.Context) ([]string, error) {
	aps.Lock()
	defer aps.Unlock()
	wait := apRefresh
	if len(aps.list) == 0 {
		wait = apRefreshEmpty
	}
	if !aps.at.IsZero() && time.Since(aps.at) < wait {
		return aps.list, nil
	}
	out, err := exec.CommandContext(ctx, "iwinfo").Output()
	if err != nil {
		return nil, err
	}
	aps.list, aps.at = accessPoints(string(out)), time.Now()
	return aps.list, nil
}

// an interface that can't be read counts as up so a missing /sys never hides clients
func isUp(iface string) bool {
	b, err := os.ReadFile("/sys/class/net/" + iface + "/flags")
	if err != nil {
		return true
	}
	flags, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(string(b), "0x")), 16, 32)
	return err != nil || flags&1 != 0
}

// interfaces whose block says Mode: Master
func accessPoints(iwinfo string) []string {
	var out []string
	var cur string
	sc := bufio.NewScanner(strings.NewReader(iwinfo))
	for sc.Scan() {
		line := sc.Text()
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			cur = strings.Fields(line)[0]
			continue
		}
		if cur != "" && strings.Contains(line, "Mode: Master") {
			out = append(out, cur)
			cur = ""
		}
	}
	return out
}

func ParseAssoc(s string) map[string]bool {
	out := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if m := macRe.FindStringSubmatch(sc.Text()); m != nil {
			out[strings.ToLower(m[1])] = true
		}
	}
	return out
}
