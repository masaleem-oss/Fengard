package h3lab

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	h3 "github.com/masaleem-oss/Fengard/internal/fengard-h3"
)

// server is a quic-go dns-over-http/3 server
type server struct {
	t      testing.TB
	udp    *net.UDPConn
	tr     *quic.Transport
	srv    *http3.Server
	cert   tls.Certificate
	pool   *x509.CertPool
	served atomic.Int64
}

type serverOpts struct {
	retry      bool
	idle       time.Duration
	maxStreams int64
	addr       string // reuse a port after a restart
	resetKey   *quic.StatelessResetKey
}

var (
	certOnce sync.Once
	labCert  tls.Certificate
	labPool  *x509.CertPool
)

func testCert(t testing.TB) (tls.Certificate, *x509.CertPool) {
	certOnce.Do(func() {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "h3lab"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
			DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			panic(err)
		}
		c, _ := x509.ParseCertificate(der)
		labPool = x509.NewCertPool()
		labPool.AddCert(c)
		labCert = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	})
	return labCert, labPool
}

// answers: big-N.test gets about N bytes of TXT, slow.test waits, everything else gets 192.0.2.1
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	q := new(dns.Msg)
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/dns-message" || q.Unpack(body) != nil || len(q.Question) != 1 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.served.Add(1)
	a := new(dns.Msg)
	a.SetReply(q)
	name := q.Question[0].Name
	switch {
	case strings.HasPrefix(name, "big-"):
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "big-"), ".test."))
		for size := 0; size < n; size += 250 {
			a.Answer = append(a.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60}, Txt: []string{strings.Repeat("x", 240)}})
		}
	case name == "slow.test.":
		time.Sleep(300 * time.Millisecond)
		fallthrough
	default:
		rr, _ := dns.NewRR(name + " 60 IN A 192.0.2.1")
		a.Answer = append(a.Answer, rr)
	}
	out, err := a.Pack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	w.Write(out)
}

func startServer(t testing.TB, o serverOpts) *server {
	t.Helper()
	cert, pool := testCert(t)
	addr := o.addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ua, _ := net.ResolveUDPAddr("udp", addr)
	var udp *net.UDPConn
	var err error
	for range 50 {
		if udp, err = net.ListenUDP("udp", ua); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, udp: udp, cert: cert, pool: pool}
	s.tr = &quic.Transport{Conn: udp, StatelessResetKey: o.resetKey}
	if o.retry {
		s.tr.VerifySourceAddress = func(net.Addr) bool { return true }
	}
	qc := &quic.Config{MaxIdleTimeout: o.idle, MaxIncomingStreams: o.maxStreams}
	ln, err := s.tr.ListenEarly(http3.ConfigureTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}}), qc)
	if err != nil {
		t.Fatal(err)
	}
	s.srv = &http3.Server{Handler: s, QUICConfig: qc}
	go s.srv.ServeListener(ln)
	return s
}

func (s *server) addr() string { return s.udp.LocalAddr().String() }

func (s *server) stop() {
	s.srv.Close()
	s.tr.Close()
	s.udp.Close()
}

// vanish drops the server without telling anyone, like a crash
func (s *server) vanish() {
	s.tr.Close()
	s.udp.Close()
}

// proxy sits between client and server and misbehaves like a bad network
type proxy struct {
	front  *net.UDPConn
	server *net.UDPAddr
	mu     sync.Mutex
	back   *net.UDPConn
	client *net.UDPAddr

	loss, dup     float64 // 0 to 1
	delay, jitter time.Duration
	blackhole     atomic.Bool
	passed        atomic.Int64
	dropped       atomic.Int64
}

type proxyOpts struct {
	loss, dup     float64
	delay, jitter time.Duration
}

func startProxy(t testing.TB, serverAddr string, o proxyOpts) *proxy {
	front, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	sa, _ := net.ResolveUDPAddr("udp", serverAddr)
	p := &proxy{front: front, server: sa, loss: o.loss, dup: o.dup, delay: o.delay, jitter: o.jitter}
	p.rebind()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, ca, err := front.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p.mu.Lock()
			p.client = ca
			back := p.back
			p.mu.Unlock()
			p.forward(append([]byte(nil), buf[:n]...), func(b []byte) { back.Write(b) })
		}
	}()
	t.Cleanup(func() { front.Close(); p.mu.Lock(); p.back.Close(); p.mu.Unlock() })
	return p
}

// rebind gives the proxy a new source port, like a nat forgetting the mapping
func (p *proxy) rebind() {
	back, err := net.DialUDP("udp", nil, p.server)
	if err != nil {
		panic(err)
	}
	p.mu.Lock()
	old := p.back
	p.back = back
	p.mu.Unlock()
	if old != nil {
		old.Close()
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := back.Read(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			p.mu.Lock()
			ca := p.client
			p.mu.Unlock()
			p.forward(append([]byte(nil), buf[:n]...), func(b []byte) { p.front.WriteToUDP(b, ca) })
		}
	}()
}

func (p *proxy) forward(b []byte, send func([]byte)) {
	if p.blackhole.Load() || p.loss > 0 && mrand.Float64() < p.loss {
		p.dropped.Add(1)
		return
	}
	p.passed.Add(1)
	copies := 1
	if p.dup > 0 && mrand.Float64() < p.dup {
		copies = 2
	}
	for range copies {
		d := p.delay
		if p.jitter > 0 {
			d += time.Duration(mrand.Int64N(int64(p.jitter)))
		}
		if d == 0 {
			send(b)
		} else {
			time.AfterFunc(d, func() { send(b) })
		}
	}
}

func (p *proxy) addr() string { return p.front.LocalAddr().String() }

func newClient(pool *x509.CertPool) *h3.Client {
	return &h3.Client{TLSConfig: &tls.Config{RootCAs: pool}}
}

func query(name string) []byte {
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(name), dns.TypeA)
	if strings.HasPrefix(name, "big-") {
		q.Question[0].Qtype = dns.TypeTXT
	}
	q.Id = 0
	b, _ := q.Pack()
	return b
}

func ask(cl *h3.Client, url, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	body, err := cl.Exchange(ctx, url, query(name))
	if err != nil {
		return err
	}
	r := new(dns.Msg)
	if err := r.Unpack(body); err != nil {
		return err
	}
	if len(r.Answer) == 0 || r.Question[0].Name != dns.Fqdn(name) {
		return fmt.Errorf("wrong answer for %s: %v", name, r)
	}
	return nil
}

// run sends n queries with w at a time and returns how many failed
func run(t testing.TB, cl *h3.Client, url string, n, w int, timeout time.Duration, name func(i int) string) (failed int, firstErr error, took time.Duration) {
	var next, bad atomic.Int64
	var mu sync.Mutex
	start := time.Now()
	var wg sync.WaitGroup
	for range w {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				if err := ask(cl, url, name(i), timeout); err != nil {
					bad.Add(1)
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return int(bad.Load()), firstErr, time.Since(start)
}

func names(i int) string { return "q" + strconv.Itoa(i) + ".test" }

func url(addr string) string { return "https://" + addr + "/dns-query" }

func status(cl *h3.Client) string {
	var b strings.Builder
	for _, s := range cl.Status() {
		fmt.Fprintf(&b, "[%s %s rtt %v sent %d lost %d ptos %d]", s.Server, s.State, s.RTT.Round(time.Microsecond), s.Sent, s.Lost, s.PTOs)
	}
	return b.String()
}

func TestBasic(t *testing.T) {
	s := startServer(t, serverOpts{})
	defer s.stop()
	cl := newClient(s.pool)
	defer cl.Close()
	failed, err, took := run(t, cl, url(s.addr()), 5000, 64, 5*time.Second, names)
	if failed > 0 {
		t.Fatalf("%d of 5000 failed, first: %v", failed, err)
	}
	t.Logf("5000 queries, 64 at a time, in %v (%.0f per second) %s", took.Round(time.Millisecond), 5000/took.Seconds(), status(cl))
	if len(cl.Status()) != 1 {
		t.Fatalf("expected one shared connection: %s", status(cl))
	}
}

func TestLoss(t *testing.T) {
	for _, c := range []struct {
		loss float64
		n    int
	}{{0.05, 400}, {0.15, 300}, {0.30, 150}} {
		t.Run(fmt.Sprintf("%.0f%%", c.loss*100), func(t *testing.T) {
			s := startServer(t, serverOpts{})
			defer s.stop()
			p := startProxy(t, s.addr(), proxyOpts{loss: c.loss})
			cl := newClient(s.pool)
			defer cl.Close()
			failed, err, took := run(t, cl, url(p.addr()), c.n, 16, 15*time.Second, names)
			t.Logf("%d queries with %.0f%% loss each way: %d failed in %v, proxy dropped %d passed %d %s", c.n, c.loss*100, failed, took.Round(time.Millisecond), p.dropped.Load(), p.passed.Load(), status(cl))
			if failed > 0 {
				t.Fatalf("first error: %v", err)
			}
		})
	}
}

func TestReorderAndDuplicate(t *testing.T) {
	s := startServer(t, serverOpts{})
	defer s.stop()
	p := startProxy(t, s.addr(), proxyOpts{dup: 0.2, delay: 5 * time.Millisecond, jitter: 40 * time.Millisecond})
	cl := newClient(s.pool)
	defer cl.Close()
	failed, err, took := run(t, cl, url(p.addr()), 1000, 32, 10*time.Second, names)
	t.Logf("1000 queries, 20%% duplicated, 5-45 ms jitter: %d failed in %v %s", failed, took.Round(time.Millisecond), status(cl))
	if failed > 0 {
		t.Fatalf("first error: %v", err)
	}
}

func TestRetry(t *testing.T) {
	s := startServer(t, serverOpts{retry: true})
	defer s.stop()
	cl := newClient(s.pool)
	defer cl.Close()
	if err := ask(cl, url(s.addr()), "retry.test", 5*time.Second); err != nil {
		t.Fatalf("after a retry packet: %v", err)
	}
}

func TestBigAnswers(t *testing.T) {
	s := startServer(t, serverOpts{})
	defer s.stop()
	p := startProxy(t, s.addr(), proxyOpts{loss: 0.05})
	cl := newClient(s.pool)
	defer cl.Close()
	for _, n := range []int{1500, 4000, 16000, 40000, 60000} {
		if err := ask(cl, url(p.addr()), "big-"+strconv.Itoa(n)+".test", 10*time.Second); err != nil {
			t.Fatalf("%d byte answer: %v", n, err)
		}
	}
	// lots of them so the connection window has to be raised a few times
	failed, err, took := run(t, cl, url(p.addr()), 200, 8, 15*time.Second, func(i int) string { return "big-20000.test" })
	t.Logf("200 answers of 20 KB through 5%% loss: %d failed in %v %s", failed, took.Round(time.Millisecond), status(cl))
	if failed > 0 {
		t.Fatalf("first error: %v", err)
	}
}

func TestStreamLimit(t *testing.T) {
	s := startServer(t, serverOpts{maxStreams: 4})
	defer s.stop()
	cl := newClient(s.pool)
	defer cl.Close()
	failed, err, took := run(t, cl, url(s.addr()), 2000, 100, 10*time.Second, func(i int) string {
		if i%50 == 0 {
			return "slow.test"
		}
		return names(i)
	})
	t.Logf("2000 queries, 100 at once, server allows 4 streams: %d failed in %v", failed, took.Round(time.Millisecond))
	if failed > 0 {
		t.Fatalf("first error: %v", err)
	}
}

func TestIdleThenAgain(t *testing.T) {
	s := startServer(t, serverOpts{idle: time.Second})
	defer s.stop()
	cl := newClient(s.pool)
	defer cl.Close()
	for i := range 3 {
		if err := ask(cl, url(s.addr()), names(i), 5*time.Second); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

func TestServerRestart(t *testing.T) {
	key := quic.StatelessResetKey{1, 2, 3}
	s := startServer(t, serverOpts{resetKey: &key})
	addr := s.addr()
	cl := newClient(s.pool)
	defer cl.Close()
	if err := ask(cl, url(addr), "before.test", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	// gone without a word, the new one only knows to send a stateless reset
	s.vanish()
	s2 := startServer(t, serverOpts{addr: addr, resetKey: &key})
	defer s2.stop()
	start := time.Now()
	if err := ask(cl, url(addr), "after.test", 5*time.Second); err != nil {
		t.Fatalf("after restart: %v %s", err, status(cl))
	}
	t.Logf("back after a restart in %v", time.Since(start).Round(time.Millisecond))
	if time.Since(start) > 2*time.Second {
		t.Fatal("took too long to notice the reset")
	}
	// and a polite close
	s2.stop()
	s3 := startServer(t, serverOpts{addr: addr})
	defer s3.stop()
	if err := ask(cl, url(addr), "polite.test", 5*time.Second); err != nil {
		t.Fatalf("after a clean close: %v", err)
	}
}

func TestRebind(t *testing.T) {
	s := startServer(t, serverOpts{})
	defer s.stop()
	p := startProxy(t, s.addr(), proxyOpts{})
	cl := newClient(s.pool)
	defer cl.Close()
	if err := ask(cl, url(p.addr()), "one.test", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	p.rebind()
	for i := range 20 {
		if err := ask(cl, url(p.addr()), names(i), 5*time.Second); err != nil {
			t.Fatalf("after the nat rebinding: %v %s", err, status(cl))
		}
	}
	t.Log(status(cl))
}

func TestBlackhole(t *testing.T) {
	s := startServer(t, serverOpts{})
	defer s.stop()
	p := startProxy(t, s.addr(), proxyOpts{})
	p.blackhole.Store(true)
	cl := newClient(s.pool)
	defer cl.Close()
	start := time.Now()
	err := ask(cl, url(p.addr()), "void.test", 10*time.Second)
	if !errors.Is(err, h3.ErrHandshake) {
		t.Fatalf("want a handshake timeout, got %v", err)
	}
	t.Logf("gave up after %v", time.Since(start).Round(time.Millisecond))
}

func TestUnreachable(t *testing.T) {
	// a port nobody listens on, linux answers with icmp port unreachable
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	addr := pc.LocalAddr().String()
	pc.Close()
	_, pool := testCert(t)
	cl := newClient(pool)
	defer cl.Close()
	start := time.Now()
	err := ask(cl, url(addr), "nobody.test", 10*time.Second)
	if err == nil {
		t.Fatal("answered by nobody")
	}
	t.Logf("%v after %v", err, time.Since(start).Round(time.Millisecond))
	if runtime.GOOS == "linux" && time.Since(start) > time.Second {
		t.Fatal("should notice the icmp error right away")
	}
}

func TestCancelled(t *testing.T) {
	s := startServer(t, serverOpts{})
	defer s.stop()
	cl := newClient(s.pool)
	defer cl.Close()
	// callers that give up straight away, some before sending and some mid flight
	var wg sync.WaitGroup
	for i := range 500 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ask(cl, url(s.addr()), "slow.test", time.Duration(i%20)*time.Millisecond)
		}()
	}
	wg.Wait()
	failed, err, _ := run(t, cl, url(s.addr()), 500, 16, 5*time.Second, names)
	if failed > 0 {
		t.Fatalf("after cancellations: %d failed, %v", failed, err)
	}
}

func TestNoLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	s := startServer(t, serverOpts{})
	cl := newClient(s.pool)
	run(t, cl, url(s.addr()), 200, 8, 5*time.Second, names)
	cl.Close()
	s.stop()
	time.Sleep(500 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("%d goroutines before, %d after\n%s", before, after, buf[:n])
	}
}

func TestGoaway(t *testing.T) {
	s := startServer(t, serverOpts{})
	defer s.stop()
	cl := newClient(s.pool)
	defer cl.Close()
	if err := ask(cl, url(s.addr()), "warm.test", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	// queries already in flight finish even though the server says goodbye
	errs := make(chan error, 10)
	for range 10 {
		go func() { errs <- ask(cl, url(s.addr()), "slow.test", 5*time.Second) }()
	}
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go s.srv.Shutdown(ctx)
	for range 10 {
		if err := <-errs; err != nil {
			t.Fatalf("in flight query during goaway: %v", err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	for _, st := range cl.Status() {
		if st.State != "closed" {
			t.Logf("after goaway: %+v", st)
		}
	}
}

// quic-go rotates its keys every 100k packets, this many answers makes it happen a couple of times
func TestKeyUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	s := startServer(t, serverOpts{})
	defer s.stop()
	p := startProxy(t, s.addr(), proxyOpts{loss: 0.01})
	cl := newClient(s.pool)
	defer cl.Close()
	failed, err, took := run(t, cl, url(p.addr()), 250000, 64, 10*time.Second, names)
	t.Logf("250k queries through 1%% loss in %v (%.0f per second): %d failed %s", took.Round(time.Millisecond), 250000/took.Seconds(), failed, status(cl))
	if failed > 0 {
		t.Fatalf("first error: %v", err)
	}
	st := cl.Status()
	if len(st) != 1 || st[0].KeyUpdates < 2 {
		t.Fatalf("want one connection that lived through at least two key updates: %+v", st)
	}
}

func BenchmarkQuery(b *testing.B) {
	s := startServer(b, serverOpts{})
	defer s.stop()
	cl := newClient(s.pool)
	defer cl.Close()
	u := url(s.addr())
	ask(cl, u, "warm.test", 5*time.Second)
	msg := query("bench.test")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := cl.Exchange(context.Background(), u, msg); err != nil {
			b.Fatal(err)
		}
	}
}
