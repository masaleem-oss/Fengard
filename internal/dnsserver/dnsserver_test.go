package dnsserver

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/policy"
	"github.com/masaleem-oss/Fengard/internal/querylog"
)

// counts queries and answers every A with 203.0.113.7
type fakeUpstream struct {
	addr  string
	hits  atomic.Int64
	delay time.Duration
	srv   *dns.Server
	down  atomic.Bool
}

func startUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u.addr = pc.LocalAddr().String()
	u.srv = &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		if u.down.Load() {
			return // fake an unreachable upstream
		}
		u.hits.Add(1)
		time.Sleep(u.delay)
		m := new(dns.Msg)
		m.SetReply(r)
		q := r.Question[0]
		if q.Qtype == dns.TypeA {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 3600},
				A:   net.IPv4(203, 0, 113, 7),
			})
		}
		w.WriteMsg(m)
	})}
	started := make(chan struct{})
	u.srv.NotifyStartedFunc = func() { close(started) }
	go u.srv.ActivateAndServe()
	<-started
	t.Cleanup(func() { u.srv.Shutdown() })
	return u
}

func startServer(t *testing.T, up *fakeUpstream, mod func(*config.Config)) (*Server, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Settings.Upstreams = []string{up.addr}
	cfg.Groups[0].Categories = []string{"ads"}
	cfg.Groups[0].SafeSearch = true
	if mod != nil {
		mod(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	b := catalog.NewBuilder()
	b.Add("ads.example", catalog.MaskOf([]string{"ads"}))
	engine := policy.New()
	engine.Rebuild(cfg, b.Build())

	s := &Server{
		Policy: engine, Devices: devices.NewTracker(""), Log: querylog.New(100, nil, nil),
		BlockIP: net.IPv4(192, 168, 8, 1), LocalNames: []string{"fengard.lan"},
	}
	s.Init()
	s.Configure(cfg.Settings)

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.ListenAndServe(ctx, addr)
	for i := 0; i < 50; i++ {
		if _, err := query(addr, "fengard.lan", dns.TypeA); err == nil {
			return s, addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return nil, ""
}

func query(addr, name string, qtype uint16) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	c := &dns.Client{Timeout: 3 * time.Second}
	r, _, err := c.Exchange(m, addr)
	return r, err
}

func firstA(m *dns.Msg) string {
	for _, rr := range m.Answer {
		if a, ok := rr.(*dns.A); ok {
			return a.A.String()
		}
	}
	return ""
}

func TestResolveBlockAndCache(t *testing.T) {
	up := startUpstream(t)
	_, addr := startServer(t, up, nil)

	r, err := query(addr, "example.com", dns.TypeA)
	if err != nil || firstA(r) != "203.0.113.7" {
		t.Fatalf("allowed lookup: %v %v", r, err)
	}
	if ttl := r.Answer[0].Header().Ttl; ttl > clientTTL {
		t.Errorf("TTL %d not capped to %d", ttl, clientTTL)
	}
	before := up.hits.Load()
	if r, _ = query(addr, "example.com", dns.TypeA); firstA(r) != "203.0.113.7" {
		t.Fatal("cached lookup failed")
	}
	if up.hits.Load() != before {
		t.Error("second lookup should come from cache")
	}

	r, _ = query(addr, "tracker.ads.example", dns.TypeA)
	if firstA(r) != "192.168.8.1" {
		t.Errorf("blocked lookup = %v, want block IP", r.Answer)
	}
	r, _ = query(addr, "ads.example", dns.TypeHTTPS)
	if len(r.Answer) != 0 {
		t.Errorf("blocked HTTPS record should be empty, got %v", r.Answer)
	}
	r, _ = query(addr, "fengard.lan", dns.TypeA)
	if firstA(r) != "192.168.8.1" {
		t.Errorf("local name = %v", r.Answer)
	}
	r, _ = query(addr, "use-application-dns.net", dns.TypeA)
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("Firefox DoH canary should be NXDOMAIN, got %s", dns.RcodeToString[r.Rcode])
	}
	r, _ = query(addr, "example.com", dns.TypeANY)
	if r.Rcode != dns.RcodeNotImplemented {
		t.Errorf("ANY should be refused, got %s", dns.RcodeToString[r.Rcode])
	}
}

func TestSafeSearchRewrite(t *testing.T) {
	up := startUpstream(t)
	_, addr := startServer(t, up, nil)
	r, err := query(addr, "www.google.com", dns.TypeA)
	if err != nil {
		t.Fatal(err)
	}
	cname, ok := r.Answer[0].(*dns.CNAME)
	if !ok || cname.Target != "forcesafesearch.google.com." {
		t.Fatalf("want CNAME to forcesafesearch, got %v", r.Answer)
	}
	if firstA(r) == "" {
		t.Error("CNAME target address missing")
	}
}

func TestDuplicateLookupsMerged(t *testing.T) {
	up := startUpstream(t)
	up.delay = 150 * time.Millisecond
	_, addr := startServer(t, up, nil)
	before := up.hits.Load()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); query(addr, "merge.example", dns.TypeA) }()
	}
	wg.Wait()
	if n := up.hits.Load() - before; n != 1 {
		t.Errorf("20 identical concurrent lookups hit upstream %d times, want 1", n)
	}
}

func TestServeStaleWhenUpstreamDown(t *testing.T) {
	up := startUpstream(t)
	s, addr := startServer(t, up, nil)
	query(addr, "stale.example", dns.TypeA)
	// expire it then take the upstream down
	s.cache.mu.Lock()
	for _, e := range s.cache.entries {
		e.expires = time.Now().Add(-time.Minute)
	}
	s.cache.mu.Unlock()
	up.down.Store(true)
	r, err := query(addr, "stale.example", dns.TypeA)
	if err != nil || firstA(r) != "203.0.113.7" {
		t.Fatalf("expected stale answer, got %v %v", r, err)
	}
	if s.Counters.Stale.Load() == 0 {
		t.Error("stale counter not incremented")
	}
}

func TestRateLimit(t *testing.T) {
	up := startUpstream(t)
	s, addr := startServer(t, up, func(c *config.Config) { c.Settings.ClientRateQPS = 5 })
	// burst is 3x rate so 15 and firing 40 at once means refill doesnt matter
	var answered atomic.Int64
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := new(dns.Msg)
			m.SetQuestion("fengard.lan.", dns.TypeA)
			c := &dns.Client{Timeout: 300 * time.Millisecond}
			if _, _, err := c.Exchange(m, addr); err == nil {
				answered.Add(1)
			}
		}()
	}
	wg.Wait()
	if answered := answered.Load(); answered > 20 {
		t.Errorf("answered %d of 40 queries from one client; limiter not working", answered)
	}
	if s.Counters.RateLimited.Load() == 0 {
		t.Error("rate limited counter not incremented")
	}
}

func TestStrangersIgnored(t *testing.T) {
	s := &Server{}
	s.Init()
	for _, ip := range []string{"8.8.8.8", "2001:4860::1"} {
		if s.allowedClient(netip.MustParseAddr(ip)) {
			t.Errorf("%s should not be answered", ip)
		}
	}
	for _, ip := range []string{"192.168.8.20", "10.1.2.3", "127.0.0.1", "fd00::5"} {
		if !s.allowedClient(netip.MustParseAddr(ip)) {
			t.Errorf("%s should be answered", ip)
		}
	}
}
