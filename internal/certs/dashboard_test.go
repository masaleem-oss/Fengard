package certs

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
)

func TestDashboardCertificate(t *testing.T) {
	a, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.Allowed = func(string) bool { return false }
	a.DashboardNames = []string{"fengard.lan"}
	a.DashboardIPs = []net.IP{net.ParseIP("192.168.8.1")}

	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(a.CertPEM())
	for _, sni := range []string{"", "fengard.lan", "192.168.8.1"} {
		c, err := a.GetCertificate(&tls.ClientHelloInfo{ServerName: sni})
		if err != nil {
			t.Fatalf("SNI %q: %v", sni, err)
		}
		opts := x509.VerifyOptions{Roots: roots}
		if sni == "" || net.ParseIP(sni) != nil {
			if err := c.Leaf.VerifyHostname("192.168.8.1"); err != nil {
				t.Errorf("SNI %q: IP SAN missing: %v", sni, err)
			}
		} else {
			opts.DNSName = sni
		}
		if _, err := c.Leaf.Verify(opts); err != nil {
			t.Errorf("SNI %q: %v", sni, err)
		}
	}
	if _, err := a.GetCertificate(&tls.ClientHelloInfo{ServerName: "bank.example"}); err == nil {
		t.Error("non-dashboard, non-blocked host got a certificate")
	}
}
