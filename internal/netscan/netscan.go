// package netscan looks for risky open ports on the devices at home
package netscan

import (
	"bufio"
	"context"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

type Severity string

const (
	High   Severity = "high"
	Medium Severity = "medium"
	Low    Severity = "low"
)

type Finding struct {
	Port     int      `json:"port"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Severity Severity `json:"severity"`
	Internet bool     `json:"internet,omitempty"` // opened to the internet through upnp
}

type DeviceResult struct {
	MAC      string    `json:"mac"`
	IP       string    `json:"ip"`
	Findings []Finding `json:"findings"`
}

type Report struct {
	Time    time.Time      `json:"time,omitzero"`
	Checked int            `json:"checked"`
	Devices []DeviceResult `json:"devices"`
}

// the doors worth worrying about on a home network and what to do about each
var risky = []Finding{
	{Port: 23, Severity: High, Title: "Telnet is open", Detail: "Telnet is an old way to log in with no encryption, and botnets hunt for it. Turn it off in the device's settings, or update its firmware."},
	{Port: 2323, Severity: High, Title: "Telnet is open", Detail: "Telnet is an old way to log in with no encryption, and botnets hunt for it. Turn it off in the device's settings, or update its firmware."},
	{Port: 5555, Severity: High, Title: "Android debugging is open", Detail: "Anyone on your Wi-Fi can install apps on this device or take it over. It's common on cheap TV boxes. Turn off ADB or network debugging in developer options."},
	{Port: 2375, Severity: High, Title: "Docker is open without a password", Detail: "This gives full control of the machine to anyone on the network. Only expose Docker over TLS, or close the port."},
	{Port: 21, Severity: Medium, Title: "FTP file sharing is open", Detail: "FTP sends passwords without encryption. If you don't use it, turn it off in the device's settings."},
	{Port: 5900, Severity: Medium, Title: "Screen sharing (VNC) is open", Detail: "Make sure it needs a strong password, or turn it off when you're not using it."},
	{Port: 554, Severity: Medium, Title: "Camera video stream is open", Detail: "Some cameras stream video without a password. Check the camera's app for a stream or RTSP password."},
	{Port: 1883, Severity: Medium, Title: "Smart home broker has no encryption", Detail: "MQTT on this port is unencrypted and often has no password. Set a password, or use the encrypted port 8883."},
	{Port: 6379, Severity: Medium, Title: "Redis database is open", Detail: "Redis usually has no password by default. Set one, or bind it to localhost."},
	{Port: 27017, Severity: Medium, Title: "MongoDB database is open", Detail: "Check it needs a login. Open MongoDB servers are a common source of data leaks."},
	{Port: 9200, Severity: Medium, Title: "Elasticsearch is open", Detail: "Check it needs a login. Open Elasticsearch servers are a common source of data leaks."},
	{Port: 3389, Severity: Low, Title: "Remote Desktop is on", Detail: "Fine if you use it. Make sure the account has a strong password."},
}

type Target struct {
	MAC string
	IP  string
}

type Scanner struct {
	// Targets lists the devices online right now
	Targets   func() []Target
	KV        *store.KV
	OnFinding func(mac string, f Finding)
	// UPnPLeases are files where the router lists ports devices opened to the internet
	UPnPLeases []string
	// Ports overrides the built in list, for tests
	Ports   []Finding
	Timeout time.Duration
	Every   time.Duration

	mu      sync.Mutex
	running bool
	last    Report
	kick    chan struct{}
}

func (s *Scanner) init() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kick == nil {
		s.kick = make(chan struct{}, 1)
	}
}

func (s *Scanner) Run(ctx context.Context) {
	s.init()
	s.load()
	every := s.Every
	if every == 0 {
		every = 7 * 24 * time.Hour
	}
	// the first scan waits a bit so devices have shown up after a boot
	first := 10 * time.Minute
	if last := s.Last().Time; !last.IsZero() {
		first = max(time.Minute, every-time.Since(last))
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
		s.Scan(ctx)
		t.Reset(every)
	}
}

// Trigger asks for a scan now, false if one is already going
func (s *Scanner) Trigger() bool {
	s.init()
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if running {
		return false
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
	return true
}

func (s *Scanner) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Scanner) Last() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func (s *Scanner) Scan(ctx context.Context) Report {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return s.last
	}
	s.running = true
	before := s.last
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	var targets []Target
	if s.Targets != nil {
		targets = s.Targets()
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = time.Second
	}
	ports := s.Ports
	if ports == nil {
		ports = risky
	}
	found := map[string][]Finding{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, t := range targets {
		for _, f := range ports {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				d := net.Dialer{Timeout: timeout}
				c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(t.IP, strconv.Itoa(f.Port)))
				if err != nil {
					return
				}
				c.Close()
				mu.Lock()
				found[t.MAC] = append(found[t.MAC], f)
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	ipOf := map[string]string{}
	for _, t := range targets {
		ipOf[t.MAC] = t.IP
		ipOf["ip:"+t.IP] = t.MAC
	}
	for _, l := range s.upnp() {
		mac := ipOf["ip:"+l.ip]
		if mac == "" {
			continue
		}
		found[mac] = append(found[mac], Finding{Port: l.port, Severity: High, Internet: true,
			Title: "Port " + strconv.Itoa(l.port) + " is open to the internet",
			Detail: "This device asked the router to open a port to the whole internet" + forWhat(l.desc) +
				". If you don't need to reach it from outside, turn off remote access or UPnP in its settings."})
	}

	r := Report{Time: time.Now(), Checked: len(targets), Devices: []DeviceResult{}}
	for mac, fs := range found {
		sort.Slice(fs, func(i, j int) bool { return rank(fs[i].Severity) < rank(fs[j].Severity) })
		r.Devices = append(r.Devices, DeviceResult{MAC: mac, IP: ipOf[mac], Findings: fs})
	}
	sort.Slice(r.Devices, func(i, j int) bool { return r.Devices[i].MAC < r.Devices[j].MAC })
	if ctx.Err() != nil {
		return before
	}
	s.mu.Lock()
	s.last = r
	s.mu.Unlock()
	if s.KV != nil {
		s.KV.Put("last", r)
	}
	if s.OnFinding != nil {
		had := map[string]bool{}
		for _, d := range before.Devices {
			for _, f := range d.Findings {
				had[d.MAC+"/"+strconv.Itoa(f.Port)] = true
			}
		}
		for _, d := range r.Devices {
			for _, f := range d.Findings {
				if f.Severity != Low && !had[d.MAC+"/"+strconv.Itoa(f.Port)] {
					s.OnFinding(d.MAC, f)
				}
			}
		}
	}
	return r
}

func forWhat(desc string) string {
	if desc == "" {
		return ""
	}
	return " (" + desc + ")"
}

func rank(s Severity) int {
	switch s {
	case High:
		return 0
	case Medium:
		return 1
	}
	return 2
}

func (s *Scanner) load() {
	if s.KV == nil {
		return
	}
	var r Report
	if ok, _ := s.KV.Get("last", &r); ok {
		s.mu.Lock()
		s.last = r
		s.mu.Unlock()
	}
}

type lease struct {
	port int
	ip   string
	desc string
}

// miniupnpd writes PROTO:EXTPORT:IP:INTPORT:EXPIRY:DESCRIPTION
func (s *Scanner) upnp() []lease {
	files := s.UPnPLeases
	if files == nil {
		files = []string{"/var/run/miniupnpd.leases", "/tmp/upnp.leases", "/var/upnp.leases"}
	}
	var out []lease
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		out = append(out, parseLeases(f)...)
		f.Close()
	}
	return out
}

func parseLeases(f *os.File) []lease {
	var out []lease
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.SplitN(sc.Text(), ":", 6)
		if len(p) < 4 {
			continue
		}
		port, err := strconv.Atoi(p[1])
		if err != nil || net.ParseIP(p[2]) == nil {
			continue
		}
		l := lease{port: port, ip: p[2]}
		if len(p) == 6 {
			l.desc = strings.TrimSpace(p[5])
		}
		out = append(out, l)
	}
	return out
}
