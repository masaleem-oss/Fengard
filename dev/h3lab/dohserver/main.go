// dohserver is dns over http/3 for the router lab, so its http/3 check doesn't depend on the internet.
// it's quic-go, not fengard-h3, so the routers still talk to a different quic implementation.
// every name gets 192.0.2.1, the cert covers 10.0.2.2 which is the lab machine as qemu's vms see it.
//
//	dohserver -addr :8853 -ca /root/owrt/doh3-ca.pem
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go/http3"
)

func main() {
	addr := flag.String("addr", ":8853", "udp address to serve on")
	caOut := flag.String("ca", "doh3-ca.pem", "where to write the certificate routers should trust")
	flag.Parse()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "fengard router lab"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(7 * 24 * time.Hour),
		IPAddresses: []net.IP{net.IPv4(10, 0, 2, 2), net.IPv4(127, 0, 0, 1)}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*caOut, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		log.Fatal(err)
	}

	var served atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 65535))
		q := new(dns.Msg)
		if q.Unpack(body) != nil || len(q.Question) != 1 {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		a := new(dns.Msg)
		a.SetReply(q)
		if q.Question[0].Qtype == dns.TypeA {
			a.Answer = append(a.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(192, 0, 2, 1)})
		}
		out, _ := a.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(out)
		if n := served.Add(1); n%100 == 1 {
			log.Printf("answered %d lookups", n)
		}
	})
	srv := &http3.Server{Addr: *addr, Handler: h, TLSConfig: http3.ConfigureTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})}
	log.Printf("dns over http/3 on %s, routers trust %s", *addr, *caOut)
	log.Fatal(srv.ListenAndServe())
}
