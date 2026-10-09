package devices

import "testing"

func TestVendor(t *testing.T) {
	cases := map[string]string{
		"b8:27:eb:12:34:56": "Raspberry Pi Foundation",
		"F0-D1-A9-00-00-01": "Apple",
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
