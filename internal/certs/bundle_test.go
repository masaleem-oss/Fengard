package certs

import (
	"crypto/tls"
	"strings"
	"testing"
)

func TestExportImportKeepsTrust(t *testing.T) {
	a, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("two boxes generated the same CA")
	}
	b.DashboardNames = []string{"fengard.lan"}
	if _, err := b.GetCertificate(&tls.ClientHelloInfo{ServerName: "fengard.lan"}); err != nil {
		t.Fatal(err)
	}

	bundle := a.Export()
	if !strings.Contains(string(bundle), "EC PRIVATE KEY") {
		t.Fatal("export is missing the key")
	}
	if err := b.Import(bundle); err != nil {
		t.Fatal(err)
	}
	if b.Fingerprint() != a.Fingerprint() || b.Name() != a.Name() {
		t.Fatal("import did not switch the CA")
	}
	// dashboard cert should chain to the imported ca now
	c, err := b.GetCertificate(&tls.ClientHelloInfo{ServerName: "fengard.lan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Leaf.CheckSignatureFrom(a.st.Load().cert); err != nil {
		t.Fatalf("dashboard cert still signed by the old CA: %v", err)
	}
	// survives a restart
	c2, err := LoadOrCreate(b.dir)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Fingerprint() != a.Fingerprint() {
		t.Fatal("imported CA was not written to disk")
	}
}

func TestImportRejectsGarbage(t *testing.T) {
	a, _ := LoadOrCreate(t.TempDir())
	for _, in := range []string{"", "hello", string(a.CertPEM())} {
		if err := a.Import([]byte(in)); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
}
