package dnsserver

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/masaleem-oss/Fengard/internal/config"
)

// defo.ie's real https record, what https://defo.ie/ech-check.php needs the browser to see
const defoHTTPS = `defo.ie. 1800 IN HTTPS 1 . ipv4hint=213.108.108.101 ech=AMD+DQA8LwAgACCPH7GhfMbYFyyRtRQSBm5dIKRopGud8JZFsPFi9T5/CgAEAAEAAQANY292ZXIuZGVmby5pZQAA/g0APNkAIAAg9EKAb5PKduF7S/487D2+crJLp07k9Fw6ZS5lqti8J1YABAABAAEADWNvdmVyLmRlZm8uaWUAAP4NADyQACAAIHo8xjC3jz7FdWJZuDCaz4kiNyZO3uwnz33hoEguguYFAAQAAQABAA1jb3Zlci5kZWZvLmllAAA= ipv6hint=2a00:c6c0:0:116:5::10`

func mustRR(t *testing.T, s string) dns.RR {
	t.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		t.Fatal(err)
	}
	return rr
}

// the ech config list a browser would use, empty when the answer has none
func echOf(m *dns.Msg) []byte {
	if m == nil {
		return nil
	}
	for _, rr := range m.Answer {
		var vals []dns.SVCBKeyValue
		switch r := rr.(type) {
		case *dns.HTTPS:
			vals = r.Value
		case *dns.SVCB:
			vals = r.Value
		}
		for _, v := range vals {
			if e, ok := v.(*dns.SVCBECHConfig); ok {
				return e.ECH
			}
		}
	}
	return nil
}

// the record without its ttl, which fengard shortens on purpose
func sameRR(got, want dns.RR) bool {
	g, w := dns.Copy(got), dns.Copy(want)
	g.Header().Ttl, w.Header().Ttl = 0, 0
	return g.String() == w.String()
}

func queryOver(addr, name string, qtype uint16, network string, edns bool) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	if edns {
		m.SetEdns0(1232, false)
	}
	c := &dns.Client{Net: network, Timeout: 3 * time.Second}
	r, _, err := c.Exchange(m, addr)
	return r, err
}

// browsers get ech keys from https (65) and svcb (64) records, so an allowed site's record has to
// reach them byte for byte, fresh from the resolver, from the cache, over udp and over tcp
func TestHTTPSRecordsKeepECH(t *testing.T) {
	up := startUpstream(t)
	defo := mustRR(t, defoHTTPS)
	svcb := mustRR(t, `_dns.resolver.example. 300 IN SVCB 1 dns.resolver.example. alpn=h2,h3 port=443 ech=AEX+DQBB2QAgACAAI2/v3UXMNwP3XPrZSw6UZAQ5SqCCblZowhRfLg2iSAAEAAEAAQASY2xvdWRmbGFyZS1lY2guY29tAAA= key65000=hello`)
	// a long ech list over 512 bytes, a client without edns has to come back over tcp for it
	big := make([]byte, 700)
	for i := range big {
		big[i] = byte(i)
	}
	large := mustRR(t, `big.example. 300 IN HTTPS 1 . alpn=h3,h2 ech=`+base64.StdEncoding.EncodeToString(big))
	up.records.Store(&map[string][]dns.RR{
		rrKey("defo.ie.", dns.TypeHTTPS):              {defo},
		rrKey("_dns.resolver.example.", dns.TypeSVCB): {svcb},
		rrKey("big.example.", dns.TypeHTTPS):          {large},
		rrKey("tracker.ads.example.", dns.TypeHTTPS):  {mustRR(t, `tracker.ads.example. 300 IN HTTPS 1 . ipv4hint=203.0.113.9 ech=AEX+DQBB2QAgACAAI2/v3UXMNwP3XPrZSw6UZAQ5SqCCblZowhRfLg2iSAAEAAEAAQASY2xvdWRmbGFyZS1lY2guY29tAAA=`)},
		rrKey("fengard.lan.", dns.TypeHTTPS):          {mustRR(t, `fengard.lan. 300 IN HTTPS 1 . ipv4hint=203.0.113.9`)},
		rrKey("only-https.example.", dns.TypeHTTPS):   {mustRR(t, `only-https.example. 300 IN HTTPS 0 svc.example.`)},
		rrKey("svc.example.", dns.TypeHTTPS):          {mustRR(t, `svc.example. 300 IN HTTPS 1 . ech=AEX+DQBB2QAgACAAI2/v3UXMNwP3XPrZSw6UZAQ5SqCCblZowhRfLg2iSAAEAAEAAQASY2xvdWRmbGFyZS1lY2guY29tAAA=`)},
		rrKey("tracker.ads.example.", dns.TypeSVCB):   {svcb},
	})
	_, addr := startServer(t, up, nil)

	check := func(name string, qtype uint16, want dns.RR) {
		t.Helper()
		before := up.hits.Load()
		for i, how := range []string{"first lookup", "cached", "over tcp"} {
			network := "udp"
			if i == 2 {
				network = "tcp"
			}
			r, err := queryOver(addr, name, qtype, network, true)
			if err != nil || r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
				t.Fatalf("%s %s %s: %v %v", name, dns.TypeToString[qtype], how, r, err)
			}
			if !sameRR(r.Answer[0], want) {
				t.Errorf("%s %s: got\n%s\nwant\n%s", name, how, r.Answer[0], want)
			}
			if got, wantECH := echOf(r), echOf(&dns.Msg{Answer: []dns.RR{want}}); !bytes.Equal(got, wantECH) {
				t.Errorf("%s %s: ech changed", name, how)
			}
		}
		if n := up.hits.Load() - before; n != 1 {
			t.Errorf("%s asked the resolver %d times, want 1 and then the cache", name, n)
		}
	}
	check("defo.ie", dns.TypeHTTPS, defo)
	check("_dns.resolver.example", dns.TypeSVCB, svcb)
	check("big.example", dns.TypeHTTPS, large)

	// no edns means 512 bytes, the client is told to retry over tcp rather than handed half a record
	r, err := queryOver(addr, "big.example", dns.TypeHTTPS, "udp", false)
	if err != nil || !r.Truncated {
		t.Errorf("large answer without edns should be truncated with TC set, got %v %v", r, err)
	}
	if r, _ := queryOver(addr, "big.example", dns.TypeHTTPS, "tcp", false); len(r.Answer) != 1 || !sameRR(r.Answer[0], large) {
		t.Errorf("tcp retry lost the record: %v", r)
	}

	// alias mode is passed on as is and the browser follows it with its own lookup
	if r, _ := query(addr, "only-https.example", dns.TypeHTTPS); len(r.Answer) != 1 || r.Answer[0].(*dns.HTTPS).Priority != 0 {
		t.Errorf("alias mode record = %v", r)
	}

	// blocked names and the router's own names never hand out real addresses or keys
	for _, name := range []string{"tracker.ads.example", "fengard.lan"} {
		for _, qtype := range []uint16{dns.TypeHTTPS, dns.TypeSVCB} {
			r, err := query(addr, name, qtype)
			if err != nil || len(r.Answer) != 0 {
				t.Errorf("%s %s should be empty, got %v %v", name, dns.TypeToString[qtype], r, err)
			}
		}
	}
}

// a safe search name's https record would carry the real google addresses past the rewrite, so it
// gets an empty answer and the browser uses the a and aaaa that point at the safe search servers
func TestSafeSearchHTTPSRecordEmpty(t *testing.T) {
	up := startUpstream(t)
	up.records.Store(&map[string][]dns.RR{
		rrKey("www.google.com.", dns.TypeHTTPS): {mustRR(t, `www.google.com. 300 IN HTTPS 1 . alpn=h2,h3 ipv4hint=142.250.1.1`)},
	})
	_, addr := startServer(t, up, nil)
	before := up.hits.Load()
	for _, qtype := range []uint16{dns.TypeHTTPS, dns.TypeSVCB} {
		r, err := query(addr, "www.google.com", qtype)
		if err != nil || r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
			t.Errorf("safe search %s = %v %v, want an empty answer", dns.TypeToString[qtype], r, err)
		}
	}
	if up.hits.Load() != before {
		t.Error("safe search https lookup went to the resolver")
	}
}

// same through a dns-over-https resolver, the answer is unpacked and packed again on the way
func TestHTTPSRecordsKeepECHOverDoH(t *testing.T) {
	defo := mustRR(t, defoHTTPS)
	doh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := new(dns.Msg)
		if err := req.Unpack(body); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		m := new(dns.Msg)
		m.SetReply(req)
		if q := req.Question[0]; q.Qtype == dns.TypeHTTPS && strings.EqualFold(q.Name, "defo.ie.") {
			m.Answer = append(m.Answer, dns.Copy(defo))
		}
		out, _ := m.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(out)
	}))
	defer doh.Close()

	up := startUpstream(t)
	_, addr := startServer(t, up, func(c *config.Config) { c.Settings.Upstreams = []string{doh.URL + "/dns-query"} }, func(s *Server) {
		s.doh.Transport = doh.Client().Transport
	})
	for _, how := range []string{"first lookup", "cached"} {
		r, err := query(addr, "defo.ie", dns.TypeHTTPS)
		if err != nil || len(r.Answer) != 1 || !sameRR(r.Answer[0], defo) {
			t.Fatalf("%s over DoH = %v %v", how, r, err)
		}
	}
}
