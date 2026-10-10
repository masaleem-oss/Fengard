package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteHTTPRejectsAdministrativeRequests(t *testing.T) {
	s := &Server{ProbeURL: "https://192.168.8.1:8443/__fengard/ping"}
	h := s.Handler()
	for _, path := range []string{"/api/setup", "/api/login", "/api/login/2fa", "/api/session", "/api/config/export", "/api/certificate/export", "/api/vpn/peers/test/config", "/js/main.js", "/index.html?script=1"} {
		r := httptest.NewRequest(http.MethodPost, "http://192.168.8.1"+path, strings.NewReader(`{"password":"secret"}`))
		r.RemoteAddr = "192.168.8.2:1234"
		r.Header.Set("Authorization", "Bearer fg_secret")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUpgradeRequired {
			t.Fatalf("%s status %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/config/export", "/js/main.js", "/api/session"} {
		r := httptest.NewRequest(http.MethodGet, "http://192.168.8.1"+path, nil)
		r.RemoteAddr = "192.168.8.2:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUpgradeRequired {
			t.Fatalf("GET %s status %d", path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://192.168.8.1/", nil)
	r.RemoteAddr = "192.168.8.2:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://192.168.8.1:8443/") || !strings.Contains(w.Body.String(), "SSH") || strings.Contains(w.Body.String(), "<script") {
		t.Fatal("bootstrap missing verified HTTPS flow")
	}
}

func TestHTTPSSetupCookiesAndSecretExports(t *testing.T) {
	local := newTestServer(t)
	h := local.Config.Handler
	remoteHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.RemoteAddr = "203.0.113.5:1234"; h.ServeHTTP(w, r) })
	plain := httptest.NewServer(remoteHandler)
	defer plain.Close()
	insecure := newClient(t, plain)
	if code, _ := insecure.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != http.StatusUpgradeRequired {
		t.Fatalf("HTTP setup status %d", code)
	}
	tlsServer := httptest.NewTLSServer(remoteHandler)
	defer tlsServer.Close()
	secure := newClient(t, tlsServer)
	secure.c = tlsServer.Client()
	secure.c.Jar, _ = cookiejar.New(nil)
	if code, body := secure.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatalf("HTTPS setup %d %v", code, body)
	}
	resp, err := secure.c.Get(tlsServer.URL + "/api/config/export")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTPS export %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", tlsServer.URL+"/api/login", strings.NewReader(`{"username":"admin","password":"correct horse battery"}`))
	req.Header.Set("X-Fengard", "1")
	resp, err = secure.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(resp.Cookies()) != 1 || !resp.Cookies()[0].Secure || !resp.Cookies()[0].HttpOnly {
		t.Fatal("HTTPS cookie lacks protection")
	}
	resp, err = secure.c.Get(tlsServer.URL + "/api/certificate/export")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(data), "PRIVATE KEY") {
		t.Fatal("HTTPS CA export unavailable")
	}
	resp, err = insecure.c.Get(plain.URL + "/fengard-ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(data), "CERTIFICATE") || strings.Contains(string(data), "PRIVATE KEY") {
		t.Fatal("public bootstrap certificate invalid")
	}
}

func TestViewerCannotExportVPNCredentials(t *testing.T) {
	srv := newTestServer(t)
	admin := newClient(t, srv)
	if code, _ := admin.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatal("setup")
	}
	if code, _ := admin.do("POST", "/api/users", `{"username":"viewer","password":"viewer password 1","role":"viewer"}`, true); code != 200 {
		t.Fatal("viewer creation")
	}
	viewer := newClient(t, srv)
	if code, _ := viewer.do("POST", "/api/login", `{"username":"viewer","password":"viewer password 1"}`, true); code != 200 {
		t.Fatal("viewer login")
	}
	for _, suffix := range []string{"", "?download=1"} {
		if code, _ := viewer.do("GET", "/api/vpn/peers/any/config"+suffix, "", false); code != http.StatusForbidden {
			t.Fatalf("viewer profile status %d", code)
		}
		if code, _ := admin.do("GET", "/api/vpn/peers/any/config"+suffix, "", false); code != http.StatusNotFound {
			t.Fatalf("admin profile routing status %d", code)
		}
	}
	_, created := admin.do("POST", "/api/keys", `{"name":"viewer","role":"viewer"}`, true)
	req, _ := http.NewRequest("GET", srv.URL+"/api/vpn/peers/any/config", nil)
	req.Header.Set("Authorization", "Bearer "+created["key"].(string))
	resp, err := viewer.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer key profile status %d", resp.StatusCode)
	}
}

func TestBlockAssetPrefixCannotServeDashboard(t *testing.T) {
	h := (&Server{}).Handler()
	for _, path := range []string{"/__fengard/", "/__fengard/index.html", "/__fengard/js/main.js", "/__fengard/js/pages/settings.js", "/__fengard/js/../index.html"} {
		r := httptest.NewRequest(http.MethodGet, "http://192.168.8.1"+path, nil)
		r.RemoteAddr = "192.168.8.2:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("dashboard served at %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/__fengard/js/block.js", "/__fengard/js/me.js", "/__fengard/img/logo.svg", "/__fengard/fonts/plex-sans-latin.woff2"} {
		r := httptest.NewRequest(http.MethodGet, "http://blocked.example"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("public block asset missing at %s: %d", path, w.Code)
		}
	}
}

func TestLANHTTPUnlessRequireHTTPS(t *testing.T) {
	local := newTestServer(t)
	h := local.Config.Handler
	lan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.RemoteAddr = "192.168.8.155:1234"; h.ServeHTTP(w, r) }))
	defer lan.Close()
	c := newClient(t, lan)
	if code, body := c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatalf("LAN HTTP setup %d %v", code, body)
	}
	_, set := c.do("GET", "/api/settings", "", false)
	set["requireHttps"] = true
	b, _ := json.Marshal(set)
	if code, _ := c.do("PUT", "/api/settings", string(b), true); code != http.StatusBadRequest {
		t.Fatalf("turning on require https over HTTP gave %d", code)
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.RemoteAddr = "192.168.8.155:1234"; h.ServeHTTP(w, r) }))
	defer tlsServer.Close()
	secure := newClient(t, tlsServer)
	secure.c = tlsServer.Client()
	secure.c.Jar, _ = cookiejar.New(nil)
	if code, _ := secure.do("POST", "/api/login", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatalf("HTTPS login %d", code)
	}
	if code, body := secure.do("PUT", "/api/settings", string(b), true); code != 200 {
		t.Fatalf("require https over HTTPS %d %v", code, body)
	}
	if code, _ := c.do("GET", "/api/settings", "", false); code != http.StatusUpgradeRequired {
		t.Fatalf("LAN HTTP after require https gave %d", code)
	}
}
