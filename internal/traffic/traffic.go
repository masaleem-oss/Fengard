// package traffic counts how much each device sends and gets from the internet
// by reading the routers connection table so no firewall rules are needed
package traffic

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

const Conntrack = "/proc/net/nf_conntrack"

type Usage struct {
	Down uint64 `json:"down"`
	Up   uint64 `json:"up"`
}

type Device struct {
	MAC      string  `json:"mac"`
	DownRate float64 `json:"downRate"` // bytes a second
	UpRate   float64 `json:"upRate"`
	Today    Usage   `json:"today"`
}

type Snapshot struct {
	Available bool     `json:"available"`
	DownRate  float64  `json:"downRate"`
	UpRate    float64  `json:"upRate"`
	Today     Usage    `json:"today"`
	Devices   []Device `json:"devices"`
}

// Lookup turns a lan address into a device mac
type Lookup func(ip string) (mac string, ok bool)

type Meter struct {
	Path     string
	Lookup   Lookup
	KV       *store.KV
	Location func() *time.Location
	Every    time.Duration

	mu      sync.Mutex
	last    map[string][2]uint64 // per connection bytes seen last time
	rates   map[string]Usage     // per mac bytes in the last interval
	at      time.Time
	gap     time.Duration
	day     string
	today   map[string]*Usage
	primed  bool
	dirty   bool
	locals  map[netip.Addr]bool
	localAt time.Time
}

func (m *Meter) Available() bool {
	_, err := os.Stat(m.path())
	return err == nil
}

func (m *Meter) path() string {
	if m.Path != "" {
		return m.Path
	}
	return Conntrack
}

func (m *Meter) Run(ctx context.Context) {
	if !m.Available() {
		return
	}
	every := m.Every
	if every == 0 {
		every = 3 * time.Second
	}
	m.load()
	t := time.NewTicker(every)
	defer t.Stop()
	save := time.NewTicker(5 * time.Minute)
	defer save.Stop()
	for {
		m.Sample()
		select {
		case <-ctx.Done():
			m.Flush()
			return
		case <-save.C:
			m.Flush()
		case <-t.C:
		}
	}
}

func (m *Meter) dayKey(t time.Time) string {
	loc := time.Local
	if m.Location != nil {
		loc = m.Location()
	}
	return t.In(loc).Format("2006-01-02")
}

func (m *Meter) Sample() {
	f, err := os.Open(m.path())
	if err != nil {
		return
	}
	defer f.Close()
	m.SampleFrom(f, time.Now())
}

func (m *Meter) SampleFrom(r io.Reader, now time.Time) {
	locals := m.localAddrs(now)
	seen := map[string][2]uint64{}
	delta := map[string]Usage{}
	macs := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		c, ok := parse(sc.Text())
		if !ok {
			continue
		}
		// only traffic between a lan device and the internet
		var dev string
		var up, down uint64
		switch {
		case !remote(c.src, locals) && remote(c.dst, locals):
			dev, up, down = c.src.String(), c.origBytes, c.replyBytes
		case remote(c.src, locals) && !remote(c.dst, locals):
			// a port forward coming in
			dev, up, down = c.dst.String(), c.replyBytes, c.origBytes
		default:
			continue
		}
		seen[c.key] = [2]uint64{up, down}
		mac, known := macs[dev]
		if !known {
			mac, _ = m.Lookup(dev)
			macs[dev] = mac
		}
		if mac == "" {
			continue
		}
		prev, had := m.last[c.key]
		if !had && !m.primed {
			continue
		}
		u := delta[mac]
		u.Up += sub(up, prev[0])
		u.Down += sub(down, prev[1])
		delta[mac] = u
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if day := m.dayKey(now); day != m.day {
		if m.day != "" {
			m.flushLocked()
		}
		m.day, m.today = day, map[string]*Usage{}
	}
	for mac, d := range delta {
		t := m.today[mac]
		if t == nil {
			t = &Usage{}
			m.today[mac] = t
		}
		t.Up += d.Up
		t.Down += d.Down
		m.dirty = true
	}
	if !m.at.IsZero() {
		m.gap = now.Sub(m.at)
	}
	m.last, m.rates, m.at, m.primed = seen, delta, now, true
}

// a connection that got reused or reset starts again from zero
func sub(now, before uint64) uint64 {
	if now < before {
		return now
	}
	return now - before
}

type conn struct {
	key                   string
	src, dst              netip.Addr
	origBytes, replyBytes uint64
}

// ipv4 2 tcp 6 117 ESTABLISHED src=a dst=b sport=x dport=y packets=1 bytes=2 src=b dst=c sport=y dport=x packets=3 bytes=4 ...
func parse(line string) (conn, bool) {
	var c conn
	f := strings.Fields(line)
	if len(f) < 4 {
		return c, false
	}
	var srcs, dsts, bytes []string
	var sport, dport string
	for _, x := range f {
		k, v, ok := strings.Cut(x, "=")
		if !ok {
			continue
		}
		switch k {
		case "src":
			srcs = append(srcs, v)
		case "dst":
			dsts = append(dsts, v)
		case "bytes":
			bytes = append(bytes, v)
		case "sport":
			if sport == "" {
				sport = v
			}
		case "dport":
			if dport == "" {
				dport = v
			}
		}
	}
	if len(srcs) < 1 || len(dsts) < 1 || len(bytes) < 2 {
		return c, false
	}
	var err error
	if c.src, err = netip.ParseAddr(srcs[0]); err != nil {
		return c, false
	}
	if c.dst, err = netip.ParseAddr(dsts[0]); err != nil {
		return c, false
	}
	c.origBytes, _ = strconv.ParseUint(bytes[0], 10, 64)
	c.replyBytes, _ = strconv.ParseUint(bytes[1], 10, 64)
	c.key = f[2] + " " + srcs[0] + " " + dsts[0] + " " + sport + " " + dport
	return c, true
}

// remote means out on the internet, not the lan or the router itself
func remote(a netip.Addr, locals map[netip.Addr]bool) bool {
	a = a.Unmap()
	if locals[a] || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	// carrier nat space is the isp side but tailscale lives there too
	return !netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

func (m *Meter) localAddrs(now time.Time) map[netip.Addr]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locals != nil && now.Sub(m.localAt) < time.Minute {
		return m.locals
	}
	out := map[netip.Addr]bool{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out[p.Addr().Unmap()] = true
			}
		}
	}
	m.locals, m.localAt = out, now
	return out
}

func (m *Meter) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Available: m.primed || m.Available(), Devices: []Device{}}
	secs := m.gap.Seconds()
	byMAC := map[string]*Device{}
	get := func(mac string) *Device {
		if d := byMAC[mac]; d != nil {
			return d
		}
		d := &Device{MAC: mac}
		byMAC[mac] = d
		return d
	}
	if secs > 0 && time.Since(m.at) < 3*m.gap {
		for mac, u := range m.rates {
			d := get(mac)
			d.DownRate, d.UpRate = float64(u.Down)/secs, float64(u.Up)/secs
			s.DownRate += d.DownRate
			s.UpRate += d.UpRate
		}
	}
	for mac, u := range m.today {
		get(mac).Today = *u
		s.Today.Down += u.Down
		s.Today.Up += u.Up
	}
	for _, d := range byMAC {
		s.Devices = append(s.Devices, *d)
	}
	sort.Slice(s.Devices, func(i, j int) bool {
		a, b := s.Devices[i], s.Devices[j]
		if ra, rb := a.DownRate+a.UpRate, b.DownRate+b.UpRate; ra != rb {
			return ra > rb
		}
		return a.Today.Down+a.Today.Up > b.Today.Down+b.Today.Up
	})
	return s
}

// History is each days total per device, newest first
func (m *Meter) History(mac string, days int) []DayUsage {
	out := []DayUsage{}
	if m.KV == nil {
		return out
	}
	m.Flush()
	m.KV.Each(func(key string, v []byte) error {
		day, who, ok := strings.Cut(key, "|")
		if !ok || (mac != "" && who != mac) {
			return nil
		}
		var u Usage
		if json.Unmarshal(v, &u) == nil {
			out = append(out, DayUsage{Day: day, MAC: who, Usage: u})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Day > out[j].Day })
	if mac != "" && len(out) > days {
		out = out[:days]
	}
	return out
}

type DayUsage struct {
	Day string `json:"day"`
	MAC string `json:"mac"`
	Usage
}

func (m *Meter) Flush() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flushLocked()
}

const keepDays = 31

func (m *Meter) flushLocked() {
	if m.KV == nil || !m.dirty {
		return
	}
	for mac, u := range m.today {
		m.KV.Put(m.day+"|"+mac, u)
	}
	m.dirty = false
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	var old []string
	m.KV.Each(func(key string, _ []byte) error {
		if day, _, _ := strings.Cut(key, "|"); day < cutoff {
			old = append(old, key)
		}
		return nil
	})
	for _, k := range old {
		m.KV.Delete(k)
	}
}

// picks up todays totals after a restart
func (m *Meter) load() {
	if m.KV == nil {
		return
	}
	day := m.dayKey(time.Now())
	today := map[string]*Usage{}
	m.KV.Each(func(key string, v []byte) error {
		d, mac, ok := strings.Cut(key, "|")
		if !ok || d != day {
			return nil
		}
		var u Usage
		if json.Unmarshal(v, &u) == nil {
			today[mac] = &u
		}
		return nil
	})
	m.mu.Lock()
	m.day, m.today = day, today
	m.mu.Unlock()
}
