package dnsserver

import (
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miekg/dns"
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
