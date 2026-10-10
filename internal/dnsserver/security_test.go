package dnsserver

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/miekg/dns"
)

func TestDNSSECRequestDoesNotClaimLocalValidation(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	up := &fakeUpstream{addr: pc.LocalAddr().String()}
	var sawDO atomic.Bool
	up.srv = &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		if opt := r.IsEdns0(); opt != nil && opt.Do() {
			sawDO.Store(true)
		}
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(203, 0, 113, 9)}}
		w.WriteMsg(m)
	})}
	started := make(chan struct{})
	up.srv.NotifyStartedFunc = func() { close(started) }
	go up.srv.ActivateAndServe()
	<-started
	t.Cleanup(func() { up.srv.Shutdown() })
	_, addr := startServer(t, up, func(c *config.Config) { c.Settings.DNSSEC = true })
	r, err := query(addr, "unsigned.example", dns.TypeA)
	if err != nil {
		t.Fatal(err)
	}
	if !sawDO.Load() || firstA(r) != "203.0.113.9" || r.AuthenticatedData {
		t.Fatal("DNSSEC request semantics changed")
	}
	r, err = query(addr, "unsigned.example", dns.TypeA)
	if err != nil || r.AuthenticatedData {
		t.Fatal("cache asserted validation")
	}
}
