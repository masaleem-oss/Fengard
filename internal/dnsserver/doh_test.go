package dnsserver

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/masaleem-oss/Fengard/internal/config"
)

// quad9 turns away http/1.1 with a 505 so doh has to speak http/2
func TestDoHUsesHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor < 2 {
			http.Error(w, "requires HTTP/2", http.StatusHTTPVersionNotSupported)
			return
		}
		body, _ := io.ReadAll(r.Body)
		q := new(dns.Msg)
		if q.Unpack(body) != nil {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		a := new(dns.Msg)
		a.SetReply(q)
		rr, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 192.0.2.7")
		a.Answer = append(a.Answer, rr)
		out, _ := a.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(out)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	s := &Server{}
	s.Init()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	s.doh.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool

	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	r, err := s.exchangeDoH(q, srv.URL+"/dns-query")
	if err != nil || len(r.Answer) != 1 {
		t.Fatalf("doh over http/2: %v %v", r, err)
	}
}

// a doh server given by name gets looked up over plain dns, not through fengard itself
func TestDoHNameUsesBootstrap(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q := new(dns.Msg)
		q.Unpack(body)
		a := new(dns.Msg)
		a.SetReply(q)
		rr, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 192.0.2.7")
		a.Answer = append(a.Answer, rr)
		out, _ := a.Pack()
		w.Write(out)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	// stand in bootstrap that says example.com is us, the test cert is made for that name
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// a and aaaa get asked at the same time
	var asked atomic.Int32
	boot := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		asked.Add(1)
		a := new(dns.Msg)
		a.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			rr, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 127.0.0.1")
			a.Answer = append(a.Answer, rr)
		}
		w.WriteMsg(a)
	})}
	go boot.ActivateAndServe()
	defer boot.Shutdown()

	s := &Server{}
	s.Init()
	s.boot.Store(&[]string{pc.LocalAddr().String()})
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	s.doh.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	q := new(dns.Msg)
	q.SetQuestion("example.org.", dns.TypeA)
	r, err := s.exchangeDoH(q, "https://example.com:"+port+"/dns-query")
	if err != nil || len(r.Answer) != 1 {
		t.Fatalf("doh by name: %v %v", r, err)
	}
	if asked.Load() == 0 {
		t.Fatal("bootstrap was never asked")
	}
}

// with http/3 on but udp refused, the answer still comes over http/2 and http/3 waits a while before trying again
func TestDoH3FallsBackToHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q := new(dns.Msg)
		q.Unpack(body)
		a := new(dns.Msg)
		a.SetReply(q)
		rr, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 192.0.2.9")
		a.Answer = append(a.Answer, rr)
		out, _ := a.Pack()
		w.Write(out)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	s := &Server{}
	s.Init()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	s.doh.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
	s.h3.TLSConfig.RootCAs = pool
	url := srv.URL + "/dns-query"
	s.Configure(config.Settings{Upstreams: []string{url}, DoH3: true})

	ask := func() {
		t.Helper()
		q := new(dns.Msg)
		q.SetQuestion("example.org.", dns.TypeA)
		start := time.Now()
		r, err := s.exchange(q, *s.upstreams.Load())
		if err != nil || len(r.Answer) != 1 {
			t.Fatalf("lookup: %v %v", r, err)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatalf("fallback took %v", time.Since(start))
		}
	}
	ask()
	if n := s.Counters.DoH3Fell.Load(); n != 1 {
		t.Fatalf("want one http/3 attempt that fell back, got %d", n)
	}
	ask()
	if n := s.Counters.DoH3Fell.Load(); n != 1 {
		t.Fatalf("http/3 should wait before trying again, attempts %d", n)
	}
	tr := s.Transports()
	if len(tr) != 1 || tr[0].Using != "HTTP/2" || tr[0].Error == "" || tr[0].RetryAt.IsZero() {
		t.Fatalf("status: %+v", tr)
	}
	// turning it off forgets nothing it shouldn't and reports plain http/2
	s.Configure(config.Settings{Upstreams: []string{url}})
	if tr := s.Transports(); tr[0].Using != "HTTP/2" || tr[0].Error != "" {
		t.Fatalf("off: %+v", tr)
	}
}

// lookups that all fail on the same dead connection count as one failure, not one each
func TestDoH3ConcurrentFailuresCountOnce(t *testing.T) {
	s := &Server{}
	s.Init()
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	url := "https://" + pc.LocalAddr().String() + "/dns-query"
	defer pc.Close() // swallows the handshake so every lookup waits out its time
	s.Configure(config.Settings{Upstreams: []string{url}, DoH3: true})
	body, _ := new(dns.Msg).SetQuestion("example.org.", dns.TypeA).Pack()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, a, _ := s.exchangeDoH3(body, url)
			a.done(true) // as if http/2 answered
		}()
	}
	wg.Wait()
	h := s.health(url)
	if h.fails != 1 || h.probing {
		t.Fatalf("fails %d probing %v", h.fails, h.probing)
	}
	if time.Until(h.waitUntil) > h3MinBackoff {
		t.Fatalf("backed off for %v after one bad moment", time.Until(h.waitUntil))
	}
}

// the dns page read null as a list and failed to load
func TestTransportsEmptyNotNull(t *testing.T) {
	s := &Server{}
	s.Init()
	s.Configure(config.Settings{Upstreams: []string{"1.1.1.1:53"}, DoH3: true})
	if b, _ := json.Marshal(s.Transports()); string(b) != "[]" {
		t.Errorf("got %s", b)
	}
}

// people who turned http/3 on hear about it when a resolver ends up on http/2, once
func TestHTTP3DownReportedOnce(t *testing.T) {
	s := &Server{}
	s.Init()
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer pc.Close()
	url := "https://" + pc.LocalAddr().String() + "/dns-query"
	s.Configure(config.Settings{Upstreams: []string{url}, DoH3: true})
	got := make(chan string, 10)
	s.OnHTTP3Down = func(u, reason string) { got <- u }
	body, _ := new(dns.Msg).SetQuestion("example.org.", dns.TypeA).Pack()
	for range h3DownAfter + 1 {
		_, a, _ := s.exchangeDoH3(body, url)
		a.done(true)
		h := s.health(url)
		h.mu.Lock()
		h.waitUntil = time.Time{}
		h.mu.Unlock()
	}
	select {
	case u := <-got:
		if u != url {
			t.Fatalf("reported %q", u)
		}
	case <-time.After(time.Second):
		t.Fatal("not reported")
	}
	select {
	case <-got:
		t.Fatal("reported twice")
	case <-time.After(100 * time.Millisecond):
	}
}

// a busy server or a bad connection fails over http/2 too, that's no reason to stop using http/3
func TestDoH3NotBlamedWhenHTTP2FailsToo(t *testing.T) {
	s := &Server{}
	s.Init()
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0") // swallows http/3, nothing on tcp so http/2 fails fast
	defer pc.Close()
	url := "https://" + pc.LocalAddr().String() + "/dns-query"
	s.Configure(config.Settings{Upstreams: []string{url}, DoH3: true})
	down := make(chan string, 1)
	s.OnHTTP3Down = func(u, _ string) { down <- u }
	q := new(dns.Msg)
	q.SetQuestion("example.org.", dns.TypeA)
	for range h3DownAfter {
		if _, err := s.exchangeDoH(q.Copy(), url); err == nil {
			t.Fatal("lookup to a dead server worked")
		}
	}
	h := s.health(url)
	h.mu.Lock()
	fails, wait := h.fails, h.waitUntil
	h.mu.Unlock()
	if fails != 0 || !wait.IsZero() {
		t.Fatalf("http/3 got blamed for a dead server: fails %d, waiting until %v", fails, wait)
	}
	select {
	case <-down:
		t.Fatal("alerted that http/3 is down when the whole server was")
	case <-time.After(100 * time.Millisecond):
	}
}

// a new wan or vpn path starts fresh instead of carrying a long backoff over
func TestNetworkChangeForgetsHTTP3Failures(t *testing.T) {
	s := &Server{}
	s.Init()
	h := s.health("https://dns.example/dns-query")
	h.worked, h.fails, h.lastErr, h.waitUntil = true, 6, "timeout", time.Now().Add(time.Hour)
	s.NetworkChanged()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fails != 0 || !h.waitUntil.IsZero() || h.lastErr != "" || !h.worked {
		t.Fatalf("after a network change: %+v", h)
	}
}

// one slow answer from a server where http/3 works doesn't move lookups to http/2, so the page shouldn't say it did
func TestTransportsStayHTTP3AfterOneSlowAnswer(t *testing.T) {
	s := &Server{}
	s.Init()
	url := "https://dns.example/dns-query"
	s.Configure(config.Settings{Upstreams: []string{url}, DoH3: true})
	h := s.health(url)
	h.worked, h.fails, h.lastErr = true, 1, "context deadline exceeded"
	if tr := s.Transports(); len(tr) != 1 || tr[0].Using != "HTTP/3" || tr[0].Error != "" {
		t.Fatalf("one slow answer: %+v", tr)
	}
	h.fails = 2
	if tr := s.Transports(); tr[0].Using != "HTTP/2" || tr[0].Error == "" {
		t.Fatalf("two in a row backs off and should say so: %+v", tr)
	}
}
