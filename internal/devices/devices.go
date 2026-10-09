package devices

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Info struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname,omitempty"`
}

type Seen struct {
	IPs      []string  `json:"ips"`
	Hostname string    `json:"hostname,omitempty"`
	LastSeen time.Time `json:"lastSeen"`
}

type Tracker struct {
	LeasesFile string                         // dnsmasq format
	Neighbors  func() []Info                  // tests swap this out
	OnNew      func(mac, hostname, ip string) // first time a mac shows up
	byIP       atomic.Pointer[map[string]Info]

	mu       sync.Mutex
	seen     map[string]*Seen // by mac
	lastScan time.Time
	static   map[string]Info // fixed ip to identity eg vpn tunnel addrs
}

const maxDevices = 4096 // cap memory if something floods the neighbor table

func NewTracker(leases string) *Tracker {
	t := &Tracker{LeasesFile: leases, Neighbors: readNeighbors, seen: map[string]*Seen{}}
	empty := map[string]Info{}
	t.byIP.Store(&empty)
	return t
}

func (t *Tracker) Known(macs []string) {
	t.mu.Lock()
	for _, m := range macs {
		if t.seen[m] == nil {
			t.seen[m] = &Seen{}
		}
	}
	t.mu.Unlock()
}

// for stuff the lease file and neighbor table dont know about like vpn clients
func (t *Tracker) SetStatic(m map[string]Info) {
	t.mu.Lock()
	t.static = m
	t.mu.Unlock()
	t.scan()
}

// unknown ips trigger a rescan at most every 2s so spoofed floods cant cause a storm
func (t *Tracker) Lookup(ip string) (Info, bool) {
	if info, ok := (*t.byIP.Load())[ip]; ok {
		t.touch(info)
		return info, true
	}
	t.mu.Lock()
	rescan := time.Since(t.lastScan) > 2*time.Second
	if rescan {
		t.lastScan = time.Now()
	}
	t.mu.Unlock()
	if rescan {
		t.scan()
		if info, ok := (*t.byIP.Load())[ip]; ok {
			t.touch(info)
			return info, true
		}
	}
	return Info{}, false
}

func (t *Tracker) touch(info Info) {
	t.mu.Lock()
	if s := t.seen[info.MAC]; s != nil {
		s.LastSeen = time.Now()
	}
	t.mu.Unlock()
}

func (t *Tracker) Seen() map[string]Seen {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]Seen, len(t.seen))
	for mac, s := range t.seen {
		out[mac] = Seen{IPs: append([]string(nil), s.IPs...), Hostname: s.Hostname, LastSeen: s.LastSeen}
	}
	return out
}

func (t *Tracker) IPsOf(mac string) []string {
	var ips []string
	for ip, info := range *t.byIP.Load() {
		if info.MAC == mac {
			ips = append(ips, ip)
		}
	}
	return ips
}

func (t *Tracker) Run(ctx context.Context, every time.Duration) {
	t.scan()
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			t.scan()
		}
	}
}

func (t *Tracker) scan() {
	m := map[string]Info{}
	hostnames := map[string]string{}
	for _, l := range ReadLeases(t.LeasesFile) {
		m[l.IP] = l
		if l.Hostname != "" {
			hostnames[l.MAC] = l.Hostname
		}
	}
	var neigh []Info
	if t.Neighbors != nil {
		neigh = t.Neighbors()
	}
	for _, n := range neigh {
		if _, ok := m[n.IP]; !ok {
			n.Hostname = hostnames[n.MAC]
			m[n.IP] = n
		}
		if len(m) >= maxDevices*4 {
			break
		}
	}
	t.mu.Lock()
	for ip, info := range t.static {
		m[ip] = info
		if info.Hostname != "" {
			hostnames[info.MAC] = info.Hostname
		}
	}
	t.mu.Unlock()
	t.byIP.Store(&m)

	type newDev struct{ mac, host, ip string }
	var fresh []newDev
	t.mu.Lock()
	byMAC := map[string][]string{}
	for ip, info := range m {
		byMAC[info.MAC] = append(byMAC[info.MAC], ip)
	}
	for mac, ips := range byMAC {
		s := t.seen[mac]
		if s == nil {
			if len(t.seen) >= maxDevices {
				continue
			}
			s = &Seen{LastSeen: time.Now()}
			t.seen[mac] = s
			fresh = append(fresh, newDev{mac, hostnames[mac], ips[0]})
		}
		s.IPs = ips
		if h := hostnames[mac]; h != "" {
			s.Hostname = h
		}
	}
	t.mu.Unlock()

	if t.OnNew != nil {
		for _, d := range fresh {
			t.OnNew(d.mac, d.host, d.ip)
		}
	}
}

// lease lines are expiry mac ip hostname clientid
func ReadLeases(path string) []Info {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Info
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		hw, err := net.ParseMAC(fields[1])
		if err != nil || net.ParseIP(fields[2]) == nil {
			continue
		}
		host := fields[3]
		if host == "*" {
			host = ""
		}
		out = append(out, Info{MAC: hw.String(), IP: fields[2], Hostname: host})
	}
	return out
}

// ye arp table on windows and mac since there is no ip neigh
func readNeighbors() []Info {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "linux":
		out, err := exec.CommandContext(ctx, "ip", "neigh", "show").Output()
		if err != nil {
			return nil
		}
		return ParseNeighbors(out)
	case "windows":
		out, err := exec.CommandContext(ctx, "arp", "-a").Output()
		if err != nil {
			return nil
		}
		return ParseARP(out)
	case "darwin", "freebsd", "openbsd", "netbsd":
		out, err := exec.CommandContext(ctx, "arp", "-an").Output()
		if err != nil {
			return nil
		}
		return ParseARP(out)
	}
	return nil
}

// handles both windows and mac style arp -a output
func ParseARP(out []byte) []Info {
	var res []Info
	for _, line := range bytes.Split(out, []byte("\n")) {
		var ip net.IP
		var hw net.HardwareAddr
		for _, f := range strings.Fields(string(line)) {
			f = strings.Trim(f, "()")
			if ip == nil {
				if p := net.ParseIP(f); p != nil && p.To4() != nil {
					ip = p
					continue
				}
			}
			if ip != nil && hw == nil {
				hw = parseLooseMAC(f)
			}
		}
		if ip == nil || hw == nil || hw[0]&1 == 1 || ip.IsMulticast() {
			continue
		}
		res = append(res, Info{MAC: hw.String(), IP: ip.String()})
	}
	return res
}

// mac prints unpadded stuff like a:b:c:d:e:f
func parseLooseMAC(s string) net.HardwareAddr {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ':' || r == '-' })
	if len(parts) != 6 {
		return nil
	}
	for i, p := range parts {
		if len(p) == 1 {
			parts[i] = "0" + p
		}
	}
	hw, err := net.ParseMAC(strings.Join(parts, ":"))
	if err != nil {
		return nil
	}
	return hw
}

func ParseNeighbors(out []byte) []Info {
	var res []Info
	for _, line := range bytes.Split(out, []byte("\n")) {
		f := strings.Fields(string(line))
		if len(f) < 5 || net.ParseIP(f[0]) == nil {
			continue
		}
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "lladdr" {
				if hw, err := net.ParseMAC(f[i+1]); err == nil && f[len(f)-1] != "FAILED" {
					res = append(res, Info{MAC: hw.String(), IP: f[0]})
				}
				break
			}
		}
	}
	return res
}
