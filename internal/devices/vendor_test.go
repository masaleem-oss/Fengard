package devices

import (
	"runtime"
	"testing"
)

func TestVendor(t *testing.T) {
	if Vendor("b8:27:eb:12:34:56") != "" {
		t.Error("a name without the add-on loaded")
	}
	UseVendors("../../features/vendors/oui.txt.gz")
	if Vendor("b8:27:eb:12:34:56") == "" {
		t.Fatal("no names after loading")
	}
	defer UseVendors("")
	cases := map[string]string{
		"b8:27:eb:12:34:56": "Raspberry Pi Foundation",
		"F0-D1-A9-00-00-01": "Apple",
		"00:00:00:00:00:01": "Xerox",
		"02:fe:00:00:00:01": "", // locally administered
		"not a mac":         "",
	}
	for mac, want := range cases {
		if got := Vendor(mac); got != want {
			t.Errorf("Vendor(%q) = %q, want %q", mac, got, want)
		}
	}
	if !IsRandomized("da:a1:19:00:00:01") || IsRandomized("f0:d1:a9:00:00:01") {
		t.Error("IsRandomized wrong")
	}
}

func TestVendorMemory(t *testing.T) {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	UseVendors("../../features/vendors/oui.txt.gz")
	if Vendor("b8:27:eb:12:34:56") == "" {
		t.Fatal("no names after loading")
	}
	runtime.GC()
	runtime.ReadMemStats(&b)
	used := int64(b.HeapAlloc) - int64(a.HeapAlloc)
	t.Logf("%d prefixes, %d names, %.2f MB", len(vendors.keys), len(vendors.ends), float64(used)/1e6)
	UseVendors("")
	if used > 1500_000 {
		t.Errorf("the registry takes %d bytes, it used to fit in 1.5 MB", used)
	}
}
