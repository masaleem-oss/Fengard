package certs

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
)

func TestIssuesOnlyForBlockedHosts(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.Allowed = func(h string) bool { return h == "blocked.example" }

	if _, err := a.GetCertificate(&tls.ClientHelloInfo{ServerName: "bank.example"}); err == nil {
		t.Fatal("issued a certificate for a host that isn't blocked")
	}
	c, err := a.GetCertificate(&tls.ClientHelloInfo{ServerName: "blocked.example"})
	if err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(a.CertPEM())
	if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: "blocked.example", Roots: roots}); err != nil {
		t.Fatalf("leaf does not verify against CA: %v", err)
	}

	// reload has to keep the same ca or every device needs reinstalling
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != a.Name() {
		t.Fatalf("CA changed on reload: %s != %s", b.Name(), a.Name())
	}
}
