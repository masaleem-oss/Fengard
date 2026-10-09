package localdns

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
)

func TestLocalNames(t *testing.T) {
	leases := filepath.Join(t.TempDir(), "leases")
	os.WriteFile(leases, []byte("0 aa:bb:cc:dd:ee:01 127.0.0.1 Leos-iPad *\n0 aa:bb:cc:dd:ee:02 127.0.0.2 printer *\n"), 0o600)
	tr := devices.NewTracker(leases)
	tr.Lookup("127.0.0.1") // triggers a scan

	cfg := config.Default()
	cfg.Devices = []config.Device{{MAC: "aa:bb:cc:dd:ee:01", Name: "Leo's iPad", Hostname: "Leos-iPad", Group: "default", Approved: true}}
	cfg.Records = []config.DNSRecord{
		{Name: "nas", Type: "A", Value: "192.168.8.40"},
		{Name: "files.lan", Type: "CNAME", Value: "nas.lan"},
		{Name: "www.example.com", Type: "A", Value: "10.0.0.5"}, // override a public name
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := New(tr)
	r.Rebuild(cfg)

	if a, ok := r.Lookup("leos-ipad.lan"); !ok || len(a.A) != 1 || a.A[0].String() != "127.0.0.1" {
		t.Errorf("device by custom name: %+v %v", a, ok)
	}
	if a, ok := r.Lookup("printer.lan"); !ok || a.A[0].String() != "127.0.0.2" {
		t.Errorf("device by DHCP hostname (not in config): %+v %v", a, ok)
	}
	if a, ok := r.Lookup("nas.lan"); !ok || a.A[0].String() != "192.168.8.40" {
		t.Errorf("record: %+v %v", a, ok)
	}
	if a, ok := r.Lookup("files.lan"); !ok || a.CNAME != "nas.lan" {
		t.Errorf("cname: %+v %v", a, ok)
	}
	if a, ok := r.Lookup("www.example.com"); !ok || a.A[0].String() != "10.0.0.5" {
		t.Errorf("override: %+v %v", a, ok)
	}
	if _, ok := r.Lookup("nothing.lan"); ok {
		t.Error("unknown local name resolved")
	}
	if _, ok := r.Lookup("example.org"); ok {
		t.Error("public name treated as local")
	}
	if n, ok := r.Reverse("127.0.0.1"); !ok || n != "leos-ipad.lan" {
		t.Errorf("reverse = %q %v", n, ok)
	}
	if n, ok := r.Reverse("192.168.8.40"); !ok || n != "nas.lan" {
		t.Errorf("reverse record = %q %v", n, ok)
	}
	if Slug("Sarah's iPhone (Pro)") != "sarahs-iphone-pro" {
		t.Errorf("Slug = %q", Slug("Sarah's iPhone (Pro)"))
	}
}
