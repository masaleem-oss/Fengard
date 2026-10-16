package dnsserver

import (
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// blackhole stands between fengard and a doh server and can stop answering on the connections it
// already has, like a vpn client taking the default route away from them
type blackhole struct {
	ln   net.Listener
	mu   sync.Mutex
	open []chan struct{}
}

func newBlackhole(t *testing.T, target string) *blackhole {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &blackhole{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			cut := make(chan struct{})
			b.mu.Lock()
			b.open = append(b.open, cut)
			b.mu.Unlock()
			pipe := func(dst, src net.Conn) {
				buf := make([]byte, 32<<10)
				for {
					n, err := src.Read(buf)
					select {
					case <-cut:
						// keep reading so the sender sees nothing wrong, just never pass it on
						if err != nil {
							return
						}
						continue
					default:
					}
					if n > 0 {
						dst.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}
			go pipe(up, c)
			go pipe(c, up)
			t.Cleanup(func() { c.Close(); up.Close() })
		}
	}()
	return b
}

func (b *blackhole) cutAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.open {
		close(c)
	}
	b.open = nil
}

func dohAnswer(w http.ResponseWriter, r *http.Request) {
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
}

func routeSetup(t *testing.T) (*Server, *blackhole, string) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(dohAnswer))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	b := newBlackhole(t, srv.Listener.Addr().String())
	s := &Server{}
	s.Init()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	s.doh.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
	url := "https://" + b.ln.Addr().String() + "/dns-query"
	return s, b, url
}

func lookup(s *Server, url string) error {
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	_, err := s.exchangeDoH(q, url)
	return err
}

// gl.inet's vpn client coming up left every lookup timing out on the old connection
func TestNetworkChangeDropsDeadConnections(t *testing.T) {
	s, b, url := routeSetup(t)
	if err := lookup(s, url); err != nil {
		t.Fatal(err)
	}
	b.cutAll()
	s.NetworkChanged()
	start := time.Now()
	if err := lookup(s, url); err != nil {
		t.Fatalf("after the network changed: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

// without being told, pings find the dead connection instead of tcp taking 15 minutes
func TestDeadConnectionFoundByPing(t *testing.T) {
	s, b, url := routeSetup(t)
	s.doh.Transport.(*http.Transport).HTTP2 = &http.HTTP2Config{SendPingTimeout: time.Second, PingTimeout: time.Second}
	if err := lookup(s, url); err != nil {
		t.Fatal(err)
	}
	b.cutAll()
	deadline := time.Now().Add(15 * time.Second)
	for lookup(s, url) != nil {
		if time.Now().After(deadline) {
			t.Fatal("still failing after 15s")
		}
	}
}
