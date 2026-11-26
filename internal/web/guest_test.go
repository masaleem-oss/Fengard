package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/guest"
)

type fakeGuest struct {
	mu      sync.Mutex
	router  bool
	guestIP string
	kicked  []string
}

func (f *fakeGuest) Status() guest.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return guest.Status{Router: f.router, State: "off"}
}

func (f *fakeGuest) Disconnect(mac string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kicked = append(f.kicked, mac)
	return true
}

func (f *fakeGuest) IsGuest(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ip == f.guestIP
}

const (
	waitMAC = "aa:bb:cc:00:00:01"
	lanMAC  = "aa:bb:cc:00:00:02"
)

func guestServer(t *testing.T, router bool) (*client, *fakeGuest, *config.Store) {
	f := &fakeGuest{router: router}
	var cs *config.Store
	srv := newTestServer(t, func(s *Server) {
		s.Guest, cs = f, s.Config
		s.Config.Update("test", "devices", func(c *config.Config) error {
			c.Devices = append(c.Devices,
				config.Device{MAC: waitMAC, Hostname: "Pixel-8", Group: "default", Guest: true, FirstSeen: time.Now()},
				config.Device{MAC: lanMAC, Hostname: "laptop", Group: "default", Approved: true})
			return nil
		})
	})
	c := newClient(t, srv)
	if code, b := c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatalf("setup %d %v", code, b)
	}
	return c, f, cs
}

func TestGuestAPI(t *testing.T) {
	c, f, cs := guestServer(t, true)

	code, g := c.do("GET", "/api/guest", "", false)
	if code != 200 || g["enabled"] != false || len(g["waiting"].([]any)) != 1 || len(g["guests"].([]any)) != 0 {
		t.Fatalf("guest = %d %v", code, g)
	}

	if code, b := c.do("PUT", "/api/guest", `{"enabled":true,"ssid":"Sam's Guests"}`, true); code != 200 {
		t.Fatalf("turn on = %d %v", code, b)
	}
	cfg := cs.Get()
	pw := cfg.Guest.Password
	if !cfg.Guest.Enabled || cfg.Guest.SSID != "Sam's Guests" || len(pw) < 16 || cfg.Guest.Group != "guests" {
		t.Fatalf("config %+v", cfg.Guest)
	}
	if gr := cfg.Group("guests"); gr == nil || gr.Name != "Guests" || strings.Join(gr.Categories, ",") != "malware,adult" {
		t.Errorf("guests profile %+v", gr)
	}
	_, g = c.do("GET", "/api/guest", "", false)
	if g["password"] != pw || g["qr"] != guest.QR("Sam's Guests", pw) || g["groupName"] != "Guests" {
		t.Errorf("guest info %v", g)
	}

	// a new password keeps the name and the approved guests
	c.do("POST", "/api/guest/"+waitMAC+"/approve", "", true)
	if code, _ := c.do("POST", "/api/guest/password", "", true); code != 200 || cs.Get().Guest.Password == pw {
		t.Errorf("new password = %d", code)
	}
	if d := cs.Get().Device(waitMAC); !d.Approved || d.Denied {
		t.Errorf("approve: %+v", d)
	}
	_, g = c.do("GET", "/api/guest", "", false)
	if len(g["guests"].([]any)) != 1 || len(g["waiting"].([]any)) != 0 {
		t.Errorf("after approve %v", g)
	}

	// kick blocks it and asks the driver to disconnect it
	code, b := c.do("POST", "/api/guest/"+waitMAC+"/kick", "", true)
	if d := cs.Get().Device(waitMAC); code != 200 || d.Approved || !d.Denied || b["disconnected"] != true || len(f.kicked) != 1 {
		t.Errorf("kick = %d %v %+v", code, b, d)
	}
	_, g = c.do("GET", "/api/guest", "", false)
	if len(g["denied"].([]any)) != 1 {
		t.Errorf("after kick %v", g)
	}
	// approving from the devices page lets it back on too
	c.do("PATCH", "/api/devices/"+waitMAC, `{"approved":true}`, true)
	if d := cs.Get().Device(waitMAC); !d.Approved || d.Denied {
		t.Errorf("devices page approve: %+v", d)
	}
	if code, _ := c.do("POST", "/api/guest/"+waitMAC+"/deny", "", true); code != 200 || cs.Get().Device(waitMAC).Approved {
		t.Errorf("deny = %d", code)
	}

	// household devices aren't guests, and actions have to be one of three
	if code, _ := c.do("POST", "/api/guest/"+lanMAC+"/kick", "", true); code != 400 || !cs.Get().Device(lanMAC).Approved {
		t.Errorf("kicked a lan device: %d", code)
	}
	if code, _ := c.do("POST", "/api/guest/"+waitMAC+"/explode", "", true); code != 404 {
		t.Errorf("unknown action = %d", code)
	}
	if code, _ := c.do("PUT", "/api/guest", `{"enabled":true,"ssid":"`+strings.Repeat("x", 40)+`"}`, true); code != 400 {
		t.Errorf("long ssid = %d", code)
	}

	// a deleted guests profile comes back when guest wi-fi is turned on again
	c.do("PUT", "/api/guest", `{"enabled":false}`, true)
	c.do("DELETE", "/api/groups/guests", "", true)
	c.do("PUT", "/api/guest", `{"enabled":true}`, true)
	if cfg := cs.Get(); cfg.Group("guests") == nil || cfg.Guest.Group != "guests" || cfg.Guest.SSID != "Sam's Guests" {
		t.Errorf("profile not recreated %+v", cfg.Guest)
	}

	// viewers see the lists but never the password
	c.do("POST", "/api/users", `{"username":"parent","password":"viewer password 1","role":"viewer"}`, true)
	jar, _ := cookiejar.New(nil)
	v := &client{t: t, c: &http.Client{Jar: jar}, url: c.url}
	v.do("POST", "/api/login", `{"username":"parent","password":"viewer password 1"}`, true)
	if code, g := v.do("GET", "/api/guest", "", false); code != 200 || g["password"] != nil || g["qr"] != nil {
		t.Errorf("viewer = %d %v", code, g)
	}
	if code, _ := v.do("POST", "/api/guest/password", "", true); code != 403 {
		t.Errorf("viewer changed the password: %d", code)
	}
}

// a computer install can't make a guest network
func TestGuestNeedsRouter(t *testing.T) {
	c, _, cs := guestServer(t, false)
	if code, b := c.do("PUT", "/api/guest", `{"enabled":true}`, true); code != 400 || !strings.Contains(b["error"].(string), "router") || cs.Get().Guest.Enabled {
		t.Errorf("enable on a computer = %d %v", code, b)
	}
	if _, g := c.do("GET", "/api/guest", "", false); g["status"].(map[string]any)["router"] != false {
		t.Errorf("status %v", g["status"])
	}
}

// guests get the approval page, never the dashboard or its sign in
func TestGuestNoDashboard(t *testing.T) {
	c, f, _ := guestServer(t, true)
	f.guestIP = "127.0.0.1"
	resp, err := c.c.Get(c.url + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || strings.Contains(string(body), "app.css") {
		t.Errorf("guest got the dashboard: %d", resp.StatusCode)
	}
	if code, _ := c.do("GET", "/api/guest", "", false); code == 200 {
		t.Error("guest reached the api")
	}
	f.guestIP = ""
	if code, _ := c.do("GET", "/api/guest", "", false); code != 200 {
		t.Errorf("admin on the lan = %d", code)
	}
}

// a waiting phone's captive check shows the waiting page, and it can check back from there
func TestGuestCaptiveWaiting(t *testing.T) {
	leases := filepath.Join(t.TempDir(), "leases")
	os.WriteFile(leases, []byte("0 "+waitMAC+" 127.0.0.1 Pixel-8 *\n"), 0o600)
	f := &fakeGuest{router: true}
	var cs *config.Store
	srv := newTestServer(t, func(s *Server) {
		s.Guest, cs, s.Devices = f, s.Config, devices.NewTracker(leases)
		s.Config.Update("test", "devices", func(c *config.Config) error {
			c.Devices = append(c.Devices, config.Device{MAC: waitMAC, Group: "default", Guest: true})
			return nil
		})
	})
	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Host = "captive.apple.com"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("/hotspot-detect.html"); code != http.StatusForbidden || !strings.Contains(body, "Waiting for approval") {
		t.Errorf("captive check while waiting = %d", code)
	}
	if _, body := get("/__fengard/me.json"); !strings.Contains(body, `"pending":true`) {
		t.Errorf("me.json under the captive host: %s", body)
	}
	if code, _ := get("/__fengard/js/block.js"); code != 200 {
		t.Errorf("block.js under the captive host = %d", code)
	}
	cs.Update("test", "approve", func(c *config.Config) error { c.Device(waitMAC).Approved = true; return nil })
	if code, body := get("/hotspot-detect.html"); code != 200 || !strings.Contains(body, "Success") {
		t.Errorf("captive check once approved = %d %s", code, body)
	}
}

func TestDefaultSSID(t *testing.T) {
	for box, want := range map[string]string{
		"":                                "Fengard Guest",
		"Sam's house":                     "Sam's house Guest",
		strings.Repeat("é", 20):           strings.Repeat("é", 13) + " Guest",
		"  The Very Long Name Of A Home ": "The Very Long Name Of A Ho Guest",
	} {
		if got := defaultSSID(box); got != want || len(got) > 32 {
			t.Errorf("defaultSSID(%q) = %q, want %q", box, got, want)
		}
	}
}
