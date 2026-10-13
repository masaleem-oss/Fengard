package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestAddons(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)
	if code, _ := c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatal("setup failed")
	}
	// a new install starts without the vpn
	if code, _ := c.do("GET", "/api/vpn", "", false); code != http.StatusNotFound {
		t.Fatalf("vpn api without the add-on = %d", code)
	}
	if code, _ := c.do("PUT", "/api/features/vpn", "", true); code != 200 {
		t.Fatalf("install = %d", code)
	}
	if code, _ := c.do("GET", "/api/vpn", "", false); code != 200 {
		t.Fatalf("vpn api with the add-on = %d", code)
	}
	// saving settings from a tab that doesn't know about add-ons keeps them
	if code, _ := c.do("PUT", "/api/settings", `{"boxName":"Home","upstreams":["1.1.1.1:53"],"localZone":"lan","defaultGroup":"default","logRetentionDays":14,"clientRateQps":100,"timezone":"UTC"}`, true); code != 200 {
		t.Fatalf("settings = %d", code)
	}
	if code, _ := c.do("GET", "/api/vpn", "", false); code != 200 {
		t.Fatal("saving settings dropped the vpn add-on")
	}
	if code, _ := c.do("DELETE", "/api/features/vpn", "", true); code != 200 {
		t.Fatalf("remove = %d", code)
	}
	if code, _ := c.do("POST", "/api/vpn/peers", `{"name":"phone"}`, true); code != http.StatusNotFound {
		t.Fatalf("adding a peer without the add-on = %d", code)
	}
	if code, _ := c.do("PUT", "/api/features/nope", "", true); code != http.StatusNotFound {
		t.Fatalf("unknown add-on = %d", code)
	}
	// files of an add-on that isn't on disk aren't served
	if code, _ := c.do("GET", "/features/vpn/page.js", "", false); code != http.StatusNotFound {
		t.Fatalf("add-on file = %d", code)
	}
}

func TestDashboardLayout(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	if _, b := c.do("GET", "/api/prefs/dashboard", "", false); b["widgets"] != nil {
		t.Fatalf("new user has a layout: %v", b)
	}
	layout := `{"widgets":[{"id":"stats","size":"full"},{"id":"speedtest","size":"md"}]}`
	if code, _ := c.do("PUT", "/api/prefs/dashboard", layout, true); code != 200 {
		t.Fatalf("save = %d", code)
	}
	// reading alerts used to overwrite everything else saved for the user
	c.do("POST", "/api/alerts/read", "", true)
	_, b := c.do("GET", "/api/prefs/dashboard", "", false)
	if w, _ := b["widgets"].([]any); len(w) != 2 {
		t.Fatalf("layout after marking alerts read: %v", b)
	}
	for _, bad := range []string{`{"widgets":[{"id":"x","size":"huge"}]}`, `{"widgets":[{"id":"../x","size":"sm"}]}`,
		`{"widgets":[{"id":"a","size":"sm"},{"id":"a","size":"sm"}]}`, `{"widgets":[` + strings.Repeat(`{"id":"a","size":"sm"},`, 40) + `{"id":"b","size":"sm"}]}`} {
		if code, _ := c.do("PUT", "/api/prefs/dashboard", bad, true); code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, code)
		}
	}
}
