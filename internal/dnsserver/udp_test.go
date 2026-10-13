package dnsserver

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// raw packets, so the ones the dns client would refuse to send can be tried too
func send(t *testing.T, addr string, p []byte) *dns.Msg {
	t.Helper()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(p)
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	b := make([]byte, 4096)
	n, err := c.Read(b)
	if err != nil {
		return nil
	}
	m := new(dns.Msg)
	if m.Unpack(b[:n]) != nil {
		t.Fatalf("unreadable reply %x", b[:n])
	}
	return m
}

func TestUDPOddPackets(t *testing.T) {
	_, addr := startServer(t, startUpstream(t), nil)

	if r := send(t, addr, []byte("not dns at all")); r != nil {
		t.Fatalf("answered garbage: %v", r)
	}
	answer := new(dns.Msg)
	answer.SetQuestion("fengard.lan.", dns.TypeA)
	answer.Response = true
	p, _ := answer.Pack()
	if r := send(t, addr, p); r != nil {
		t.Fatal("answered an answer, that's how reflection attacks work")
	}

	update := new(dns.Msg)
	update.SetUpdate("lan.")
	p, _ = update.Pack()
	if r := send(t, addr, p); r == nil || r.Rcode != dns.RcodeNotImplemented {
		t.Fatalf("update: %v", r)
	}

	two := new(dns.Msg)
	two.SetQuestion("fengard.lan.", dns.TypeA)
	two.Question = append(two.Question, dns.Question{Name: "example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET})
	p, _ = two.Pack()
	if r := send(t, addr, p); r == nil || r.Rcode != dns.RcodeFormatError {
		t.Fatalf("two questions: %v", r)
	}
}

// twice the cap at once still all get answered, the extra ones just wait their turn. more than 300 from
// one address would hit the per device rate limit instead
func TestUDPMoreThanTheCap(t *testing.T) {
	_, addr := startServer(t, startUpstream(t), nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0
	for range maxInFlight * 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := query(addr, "fengard.lan", dns.TypeA); err != nil || len(r.Answer) == 0 {
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		t.Fatalf("%d of %d lookups failed", failed, maxInFlight*2)
	}
}
