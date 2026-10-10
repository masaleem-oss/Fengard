package catalog

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Category struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Desc    string   `json:"desc"`
	Sources []string `json:"-"`
}

const hagezi = "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/"

// index is the mask bit
var Categories = []Category{
	{ID: "ads", Name: "Ads & trackers", Desc: "Advertising, analytics and tracking domains",
		Sources: []string{hagezi + "wildcard/pro.mini-onlydomains.txt"}},
	{ID: "malware", Name: "Malware & phishing", Desc: "Known malicious, phishing and scam domains",
		Sources: []string{hagezi + "wildcard/tif.mini-onlydomains.txt", "https://urlhaus.abuse.ch/downloads/hostfile/"}},
	{ID: "adult", Name: "Adult content", Desc: "Pornography and explicit content",
		Sources: []string{hagezi + "wildcard/nsfw-onlydomains.txt"}},
	{ID: "gambling", Name: "Gambling", Desc: "Betting, casinos and gambling",
		Sources: []string{hagezi + "wildcard/gambling.mini-onlydomains.txt"}},
	{ID: "social", Name: "Social media", Desc: "TikTok, Instagram, Snapchat, Facebook and similar",
		Sources: []string{hagezi + "wildcard/social-onlydomains.txt"}},
	{ID: "gaming", Name: "Gaming", Desc: "Online games, game stores and launchers"},
	{ID: "streaming", Name: "Video streaming", Desc: "YouTube, Netflix, Twitch and similar"},
	{ID: "ai", Name: "AI chatbots", Desc: "ChatGPT, Claude, Gemini, Character.AI and similar"},
	{ID: "bypass", Name: "VPN, proxy & DNS bypass", Desc: "Services used to get around filtering",
		Sources: []string{hagezi + "wildcard/doh-vpn-proxy-bypass-onlydomains.txt"}},
	{ID: "tor", Name: "Tor", Desc: "The Tor network, Tor Browser and its bridges; relays are also blocked in the firewall"},
}

const DoHIPsSource = hagezi + "ips/doh.txt"

// system resolver might be fengard itself or a censoring isp so list hosts get resolved separately
var listResolvers = []string{"1.1.1.1:53", "9.9.9.9:53", "8.8.8.8:53"}

// seed domains per category files named by category id
//
//go:embed seeds
var seeds embed.FS

const maxDownload = 64 << 20

func Index(id string) int {
	for i, c := range Categories {
		if c.ID == id {
			return i
		}
	}
	return -1
}

func MaskOf(ids []string) Mask {
	var m Mask
	for _, id := range ids {
		if i := Index(id); i >= 0 {
			m |= 1 << i
		}
	}
	return m
}

func (m Mask) IDs() []string {
	var out []string
	for i, c := range Categories {
		if m&(1<<i) != 0 {
			out = append(out, c.ID)
		}
	}
	return out
}

type Status struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Desc    string    `json:"desc"`
	Domains int       `json:"domains"`
	Updated time.Time `json:"updated"`
	Error   string    `json:"error,omitempty"`
}

// position in the slice is the mask bit
type CustomSource struct{ ID, Name, URL string }

type Catalog struct {
	dir    string
	client *http.Client

	OnChange func()

	set    atomic.Pointer[DomainSet]
	dohIPs atomic.Pointer[[]string]
	torIPs atomic.Pointer[[]string]
	custom atomic.Pointer[[]CustomSource]

	mu     sync.Mutex
	status []Status
	update sync.Mutex
	loadMu sync.Mutex
}

func New(dir string, initial ...CustomSource) *Catalog {
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var last error
		for _, addr := range listResolvers {
			d := net.Dialer{Timeout: 3 * time.Second}
			conn, err := d.DialContext(ctx, network, addr)
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}}
	dialer := &net.Dialer{Timeout: 15 * time.Second, Resolver: resolver}
	c := &Catalog{dir: dir, client: &http.Client{Timeout: 60 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}}}
	c.set.Store(&DomainSet{})
	c.dohIPs.Store(&[]string{})
	tor := mergeTor(nil)
	c.torIPs.Store(&tor)
	if len(initial) > MaxCustomLists {
		initial = initial[:MaxCustomLists]
	}
	custom := append([]CustomSource{}, initial...)
	c.custom.Store(&custom)
	c.status = make([]Status, len(Categories))
	for i, cat := range Categories {
		c.status[i] = Status{ID: cat.ID, Name: cat.Name, Desc: cat.Desc}
	}
	return c
}

func (c *Catalog) Set() *DomainSet { return c.set.Load() }

// client that resolves through public dns so it works even while fengard is the one answering
func (c *Catalog) HTTPClient() *http.Client { return c.client }

func (c *Catalog) DoHIPs() []string { return *c.dohIPs.Load() }

func (c *Catalog) TorIPs() []string { return *c.torIPs.Load() }

func (c *Catalog) Status() []Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Status{}, c.status...)
}

func (c *Catalog) SetCustom(src []CustomSource) {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	if len(src) > MaxCustomLists {
		src = src[:MaxCustomLists]
	}
	cp := append([]CustomSource{}, src...)
	c.custom.Store(&cp)
	if err := c.load(); err != nil {
		log.Printf("loading custom blocklists: %v", err)
	}
}

func (c *Catalog) Custom() []CustomSource { return *c.custom.Load() }

func (c *Catalog) UpdateCustom(ctx context.Context, url string) error {
	c.update.Lock()
	defer c.update.Unlock()
	if err := c.download(ctx, url); err != nil {
		return err
	}
	return c.Load()
}

func cacheName(url string) string {
	r := strings.NewReplacer("https://", "", "http://", "", "/", "_", ":", "_")
	return r.Replace(url)
}

func (c *Catalog) Load() error {
	c.loadMu.Lock()
	defer c.loadMu.Unlock()
	return c.load()
}

func (c *Catalog) load() error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	custom := *c.custom.Load()
	b := NewBuilder()
	counts := make([]int, len(Categories)+len(custom))
	mtimes := make([]time.Time, len(Categories)+len(custom))

	for i, cat := range Categories {
		bit := Mask(1) << i
		add := func(d string) { b.Add(d, bit); counts[i]++ }

		if f, err := seeds.Open("seeds/" + cat.ID + ".txt"); err == nil {
			ParseList(f, add)
			f.Close()
		}
		for _, src := range cat.Sources {
			path := filepath.Join(c.dir, cacheName(src))
			f, err := os.Open(path)
			if err != nil {
				continue // not downloaded yet
			}
			if fi, err := f.Stat(); err == nil && fi.ModTime().After(mtimes[i]) {
				mtimes[i] = fi.ModTime()
			}
			ParseList(f, add)
			f.Close()
		}
	}
	for i, cs := range custom {
		bit := CustomBit(i)
		k := len(Categories) + i
		add := func(d string) { b.Add(d, bit); counts[k]++ }
		f, err := os.Open(filepath.Join(c.dir, cacheName(cs.URL)))
		if err != nil {
			continue
		}
		if fi, err := f.Stat(); err == nil {
			mtimes[k] = fi.ModTime()
		}
		ParseList(f, add)
		f.Close()
	}
	c.set.Store(b.Build())

	if f, err := os.Open(filepath.Join(c.dir, cacheName(DoHIPsSource))); err == nil {
		var ips []string
		ParseIPs(f, func(ip string) { ips = append(ips, ip) })
		f.Close()
		c.dohIPs.Store(&ips)
	}
	if f, err := os.Open(filepath.Join(c.dir, cacheName(TorIPsSource))); err == nil {
		relays, err := ParseTorRelays(f)
		f.Close()
		if err != nil {
			log.Printf("tor relay list: %v", err)
		} else {
			tor := mergeTor(relays)
			c.torIPs.Store(&tor)
		}
	} else if f, err := os.Open(filepath.Join(c.dir, cacheName(TorIPsMirror))); err == nil {
		var relays []string
		ParseIPs(f, func(ip string) { relays = append(relays, ip) })
		f.Close()
		tor := mergeTor(relays)
		c.torIPs.Store(&tor)
	}

	c.mu.Lock()
	status := make([]Status, 0, len(counts))
	for i, cat := range Categories {
		st := Status{ID: cat.ID, Name: cat.Name, Desc: cat.Desc, Domains: counts[i], Updated: mtimes[i]}
		if i < len(c.status) {
			st.Error = c.status[i].Error
		}
		status = append(status, st)
	}
	for i, cs := range custom {
		k := len(Categories) + i
		st := Status{ID: "list:" + cs.ID, Name: cs.Name, Desc: cs.URL, Domains: counts[k], Updated: mtimes[k]}
		if k < len(c.status) && c.status[k].ID == st.ID {
			st.Error = c.status[k].Error
		}
		status = append(status, st)
	}
	c.status = status
	c.mu.Unlock()
	if c.OnChange != nil {
		c.OnChange()
	}
	return nil
}

// keeps the old cached copy if a download fails
func (c *Catalog) Update(ctx context.Context) error {
	c.update.Lock()
	defer c.update.Unlock()

	var errs []error
	failed := map[int]string{}
	for i, cat := range Categories {
		for _, src := range cat.Sources {
			if err := c.download(ctx, src); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", src, err))
				failed[i] = err.Error()
			}
		}
	}
	for i, cs := range *c.custom.Load() {
		if err := c.download(ctx, cs.URL); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", cs.URL, err))
			failed[len(Categories)+i] = err.Error()
		}
	}
	if err := c.download(ctx, DoHIPsSource); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", DoHIPsSource, err))
	}
	if err := c.download(ctx, TorIPsSource); err != nil {
		// torproject.org is blocked in some countries so try the github mirror
		if err2 := c.download(ctx, TorIPsMirror); err2 != nil {
			errs = append(errs, fmt.Errorf("%s: %w", TorIPsSource, err), fmt.Errorf("%s: %w", TorIPsMirror, err2))
		}
	}
	if err := c.Load(); err != nil {
		return err
	}
	c.mu.Lock()
	for i := range c.status {
		c.status[i].Error = failed[i]
	}
	c.mu.Unlock()
	return errors.Join(errs...)
}

func (c *Catalog) download(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Fengard/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	path := filepath.Join(c.dir, cacheName(url))
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	f.Close()
	if err == nil && n > maxDownload {
		err = errors.New("list too large")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Catalog) Run(ctx context.Context, every time.Duration) {
	stale := true
	for _, s := range c.Status() {
		if !s.Updated.IsZero() && time.Since(s.Updated) < every {
			stale = false
		}
	}
	// address lists added after the domain cache was made get fetched now
	if _, err := os.Stat(filepath.Join(c.dir, cacheName(DoHIPsSource))); err != nil {
		stale = true
	}
	_, e1 := os.Stat(filepath.Join(c.dir, cacheName(TorIPsSource)))
	_, e2 := os.Stat(filepath.Join(c.dir, cacheName(TorIPsMirror)))
	if e1 != nil && e2 != nil {
		stale = true
	}
	if !stale {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
	for {
		if err := c.Update(ctx); err != nil {
			log.Printf("blocklist update: %v", err)
		} else {
			log.Printf("blocklists updated: %d domains", c.Set().Len())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func ParseList(r io.Reader, add func(domain string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if i := bytes.IndexAny(line, "#!"); i >= 0 {
			line = line[:i]
		}
		fields := bytes.Fields(line)
		if len(fields) == 0 {
			continue
		}
		d := string(fields[0])
		if net.ParseIP(d) != nil {
			if len(fields) < 2 {
				continue
			}
			d = string(fields[1])
		}
		d = strings.TrimPrefix(strings.TrimPrefix(d, "||"), "*.")
		d = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(d), "^"), ".")
		if ValidDomain(d) {
			add(d)
		}
	}
}

func ParseIPs(r io.Reader, add func(string)) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(s, '#'); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		if s == "" {
			continue
		}
		if net.ParseIP(s) != nil {
			add(s)
		} else if _, _, err := net.ParseCIDR(s); err == nil {
			add(s)
		}
	}
}

func ValidDomain(d string) bool {
	if len(d) < 3 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	switch d {
	case "localhost.localdomain", "local", "broadcasthost":
		return false
	}
	for i := 0; i < len(d); i++ {
		ch := d[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '.' || ch == '_') {
			return false
		}
	}
	return d[0] != '.' && d[len(d)-1] != '.' && !strings.Contains(d, "..")
}
