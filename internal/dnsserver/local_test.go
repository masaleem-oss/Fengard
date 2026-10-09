package dnsserver

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/localdns"
)

func TestLocalZoneAndPTR(t *testing.T) {
	up := startUpstream(t)
	leases := filepath.Join(t.TempDir(), "leases")
	os.WriteFile(leases, []byte("0 aa:bb:cc:dd:ee:01 127.0.0.1 kids-tablet *\n"), 0o600)
	tr := devices.NewTracker(leases)
	local := localdns.New(tr)

	s, addr := startServer(t, up, func(c *config.Config) {
		c.Records = []config.DNSRecord{{Name: "nas", Type: "A", Value: "192.168.8.40"}}
	})
	s.Devices = tr
	s.Local = local
	cfg := config.Default()
	cfg.Records = []config.DNSRecord{{Name: "nas", Type: "A", Value: "192.168.8.40"}}
	cfg.Validate()
	local.Rebuild(cfg)

	before := up.hits.Load()
	r, err := query(addr, "nas.lan", dns.TypeA)
	if err != nil || firstA(r) != "192.168.8.40" {
		t.Fatalf("nas.lan = %v %v", r, err)
	}
	r, _ = query(addr, "kids-tablet.lan", dns.TypeA)
	if firstA(r) != "127.0.0.1" {
		t.Fatalf("device name = %v", r.Answer)
	}
	r, _ = query(addr, "missing.lan", dns.TypeA)
	if r.Rcode != dns.RcodeNameError {
		t.Errorf("unknown .lan name should be NXDOMAIN, got %s", dns.RcodeToString[r.Rcode])
	}
	if up.hits.Load() != before {
		t.Error("local zone lookups leaked to the upstream")
	}
	r, _ = query(addr, "1.0.0.127.in-addr.arpa", dns.TypePTR)
	if len(r.Answer) != 1 || r.Answer[0].(*dns.PTR).Ptr != "kids-tablet.lan." {
		t.Errorf("PTR = %v", r.Answer)
	}
	r, _ = query(addr, "9.9.168.192.in-addr.arpa", dns.TypePTR)
	if r.Rcode != dns.RcodeNameError || up.hits.Load() != before {
		t.Error("unknown private PTR should be NXDOMAIN locally")
	}
}

func TestDoHUpstream(t *testing.T) {
	doh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := new(dns.Msg)
		if err := req.Unpack(body); err != nil || r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "bad request", 400)
			return
		}
		m := new(dns.Msg)
		m.SetReply(req)
		m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300}, A: net.IPv4(198, 51, 100, 9)})
		out, _ := m.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(out)
	}))
	defer doh.Close()

	up := startUpstream(t)
	s, addr := startServer(t, up, func(c *config.Config) { c.Settings.Upstreams = []string{doh.URL + "/dns-query"} })
	s.doh.Transport = doh.Client().Transport // trust the test server cert
	r, err := query(addr, "doh.example", dns.TypeA)
	if err != nil || firstA(r) != "198.51.100.9" {
		t.Fatalf("DoH answer = %v %v", r, err)
	}
	if up.hits.Load() != 0 {
		t.Error("plain upstream was used instead of DoH")
	}
}
