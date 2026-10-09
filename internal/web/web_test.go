package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/auth"
	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/certs"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/dnsserver"
	"github.com/masaleem-oss/Fengard/internal/firewall"
	"github.com/masaleem-oss/Fengard/internal/notify"
	"github.com/masaleem-oss/Fengard/internal/policy"
	"github.com/masaleem-oss/Fengard/internal/querylog"
	"github.com/masaleem-oss/Fengard/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	logOf := func(n string) *store.Log { l, _ := db.Log(n); return l }
	users, _ := db.KV("users")
	keys, _ := db.KV("apikeys")
	prefs, _ := db.KV("prefs")
	cs, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(filepath.Join(dir, "lists"))
	engine := policy.New()
	cs.Subscribe(func(c *config.Config) { engine.Rebuild(c, cat.Set()) })
	ca, _ := certs.LoadOrCreate(dir)
	dns := &dnsserver.Server{}
	dns.Init()
	s := &Server{
		CA: ca, Config: cs, Catalog: cat, Policy: engine,
		Log: querylog.New(10, logOf("queries"), nil), Devices: devices.NewTracker(""), DNS: dns,
		Firewall: &firewall.Manager{Inputs: func() (firewall.Params, error) { return firewall.Params{}, nil }},
		Auth:     auth.New(users, keys), Alerts: alerts.New(logOf("alerts")), Audit: logOf("audit"), Prefs: prefs, Version: "test",
		Notifier: notify.New("test"),
		Started:  time.Now(),
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

type client struct {
	t   *testing.T
	c   *http.Client
	url string
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, c: &http.Client{Jar: jar}, url: srv.URL}
}

func (c *client) do(method, path, body string, csrf bool) (int, map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.url+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf {
		req.Header.Set("X-Fengard", "1")
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func TestAuthFlow(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)

	if code, _ := c.do("GET", "/api/overview", "", false); code != http.StatusUnauthorized {
		t.Fatalf("overview without session = %d, want 401", code)
	}
	_, s := c.do("GET", "/api/session", "", false)
	if s["setupNeeded"] != true {
		t.Fatal("fresh install should need setup")
	}
	if code, _ := c.do("POST", "/api/setup", `{"username":"admin","password":"short"}`, true); code != http.StatusBadRequest {
		t.Fatalf("weak password accepted: %d", code)
	}
	if code, _ := c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, false); code != http.StatusForbidden {
		t.Fatalf("setup without CSRF header = %d, want 403", code)
	}
	if code, body := c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatalf("setup = %d %v", code, body)
	}
	// setup only works once
	other := newClient(t, srv)
	if code, _ := other.do("POST", "/api/setup", `{"username":"evil","password":"another long password"}`, true); code != http.StatusBadRequest {
		t.Fatalf("second setup = %d, want 400", code)
	}
	if code, _ := c.do("GET", "/api/overview", "", false); code != 200 {
		t.Fatalf("overview after setup = %d", code)
	}

	// viewer can read but not change anything
	if code, b := c.do("POST", "/api/users", `{"username":"parent","password":"viewer password 1","role":"viewer"}`, true); code != 200 {
		t.Fatalf("create viewer = %d %v", code, b)
	}
	v := newClient(t, srv)
	if code, _ := v.do("POST", "/api/login", `{"username":"parent","password":"viewer password 1"}`, true); code != 200 {
		t.Fatalf("viewer login = %d", code)
	}
	if code, _ := v.do("GET", "/api/devices", "", false); code != 200 {
		t.Fatalf("viewer read = %d", code)
	}
	if code, _ := v.do("POST", "/api/rules", `{"kind":"block","domain":"x.com"}`, true); code != http.StatusForbidden {
		t.Fatalf("viewer write = %d, want 403", code)
	}

	// admin change works and gets validated
	if code, _ := c.do("POST", "/api/rules", `{"kind":"block","domain":"https://TikTok.com/foo"}`, true); code != 200 {
		t.Fatalf("add rule = %d", code)
	}
	_, rules := c.do("GET", "/api/rules", "", false)
	if b, _ := rules["block"].([]any); len(b) != 1 || b[0] != "tiktok.com" {
		t.Fatalf("rules = %v", rules)
	}
	if code, _ := c.do("POST", "/api/rules", `{"kind":"block","domain":"not a domain"}`, true); code != http.StatusBadRequest {
		t.Fatalf("invalid rule = %d, want 400", code)
	}
	if code, _ := c.do("POST", "/api/rules", `{"kind":"block","domain":"x.com","extra":1}`, true); code != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", code)
	}
}

func TestLoginBruteForceLimited(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	limited := false
	for range 10 {
		code, _ := newClient(t, srv).do("POST", "/api/login", `{"username":"admin","password":"wrong password!"}`, true)
		if code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("10 rapid wrong passwords were never rate limited")
	}
}

func TestBlockPageForOtherHosts(t *testing.T) {
	srv := newTestServer(t)
	admin := newClient(t, srv)
	admin.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	if code, _ := admin.do("POST", "/api/rules", `{"kind":"block","domain":"blocked.example"}`, true); code != 200 {
		t.Fatalf("add rule = %d", code)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/some/path", nil)
	req.Host = "www.blocked.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "www.blocked.example") || !strings.Contains(string(body), "Request access") {
		t.Fatalf("block page: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "<script>") {
		t.Error("block page must not contain inline scripts (CSP)")
	}
	// dashboard api shouldnt be reachable through a blocked hostname
	req, _ = http.NewRequest("GET", srv.URL+"/api/session", nil)
	req.Host = "evil.example"
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("API via foreign host = %d, want block page", resp.StatusCode)
	}
}

func TestAccessRequestCreatesAlert(t *testing.T) {
	srv := newTestServer(t)
	send := func(csrf bool) int {
		req, _ := http.NewRequest("POST", srv.URL+"/__fengard/request", strings.NewReader(`{"domain":"games.example","note":"homework"}`))
		req.Host = "games.example"
		req.Header.Set("Content-Type", "application/json")
		if csrf {
			req.Header.Set("X-Fengard", "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := send(false); code != http.StatusForbidden {
		t.Fatalf("request without header = %d, want 403", code)
	}
	if code := send(true); code != 200 {
		t.Fatalf("request = %d", code)
	}
	limited := false
	for range 5 {
		if send(true) == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("access requests are not rate limited")
	}

	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	_, body := c.do("GET", "/api/alerts", "", false)
	list, _ := body["alerts"].([]any)
	if len(list) == 0 || body["unread"].(float64) < 1 {
		t.Fatalf("no access request alert: %v", body)
	}
	if code, _ := c.do("POST", "/api/alerts/read", "", true); code != 200 {
		t.Fatal("mark read failed")
	}
	if _, body = c.do("GET", "/api/alerts", "", false); body["unread"].(float64) != 0 {
		t.Errorf("unread after mark read = %v", body["unread"])
	}
}
