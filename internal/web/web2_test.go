package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/auth"
)

// fresh install has to give empty arrays not null the port forwarding page crashed on this
func TestEmptyListsAreArrays(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	for _, path := range []string{"/api/portforwards", "/api/lists", "/api/channels", "/api/keys", "/api/users", "/api/devices", "/api/alerts", "/api/audit"} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		resp, err := c.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var raw json.RawMessage
		json.NewDecoder(resp.Body).Decode(&raw)
		resp.Body.Close()
		if strings.Contains(string(raw), "null") && !strings.Contains(string(raw), "\"") {
			t.Errorf("%s returned null", path)
		}
		if strings.HasPrefix(string(raw), "null") {
			t.Errorf("%s returned null body", path)
		}
	}
	_, g := c.do("GET", "/api/groups", "", false)
	_ = g
	// group lists have to be arrays too
	req, _ := http.NewRequest("GET", srv.URL+"/api/groups", nil)
	resp, _ := c.c.Do(req)
	var groups []map[string]any
	json.NewDecoder(resp.Body).Decode(&groups)
	resp.Body.Close()
	for _, g := range groups {
		for _, k := range []string{"schedules", "apps", "block", "allow", "categories"} {
			if g[k] == nil {
				t.Errorf("group %v: %s is null", g["id"], k)
			}
		}
	}
	// adding a schedule to a profile that has none
	code, body := c.do("PUT", "/api/groups/iot", `{"id":"iot","name":"Smart home","categories":["malware"],"apps":[],"block":[],"allow":[],"safeSearch":false,"schedules":[{"name":"Night","days":[1],"start":"22:00","end":"06:00","blockAll":true,"categories":[],"enabled":true}]}`, true)
	if code != 200 {
		t.Fatalf("add schedule = %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/portforwards", `{"name":"Game","proto":"tcp","extPort":25565,"destIp":"192.168.8.50","destPort":25565,"enabled":true}`, true); code != 200 {
		t.Fatalf("add forward on fresh install = %d %v", code, body)
	}
}

func TestTwoFactorLoginFlow(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	code, body := c.do("POST", "/api/2fa/setup", `{}`, true)
	if code != 200 {
		t.Fatalf("2fa setup = %d %v", code, body)
	}
	secret := body["secret"].(string)
	totp := auth.CodeForTest(secret, 0)
	if code, body := c.do("POST", "/api/2fa/enable", `{"code":"`+totp+`"}`, true); code != 200 || len(body["recoveryCodes"].([]any)) != 8 {
		t.Fatalf("2fa enable = %d %v", code, body)
	}

	other := newClient(t, srv)
	code, body = other.do("POST", "/api/login", `{"username":"admin","password":"correct horse battery"}`, true)
	if code != 200 || body["twoFactor"] != true || body["token"] == nil {
		t.Fatalf("login with 2FA = %d %v", code, body)
	}
	if code, _ := other.do("GET", "/api/overview", "", false); code != http.StatusUnauthorized {
		t.Fatal("password alone opened a session")
	}
	tok := body["token"].(string)
	if code, _ := other.do("POST", "/api/login/2fa", `{"token":"`+tok+`","code":"000000"}`, true); code != http.StatusUnauthorized {
		t.Fatalf("wrong code = %d", code)
	}
	next := auth.CodeForTest(secret, 1)
	if code, body := other.do("POST", "/api/login/2fa", `{"token":"`+tok+`","code":"`+next+`"}`, true); code != 200 {
		t.Fatalf("2fa complete = %d %v", code, body)
	}
	if code, _ := other.do("GET", "/api/overview", "", false); code != 200 {
		t.Fatal("no session after 2FA")
	}
}

func TestAPIKeyAccess(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	code, body := c.do("POST", "/api/keys", `{"name":"monitor","role":"viewer"}`, true)
	if code != 200 {
		t.Fatalf("create key = %d %v", code, body)
	}
	key := body["key"].(string)
	get := func(path, method, payload string) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+key)
		if payload != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req) // no cookie jar no csrf header
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/api/overview", "GET", ""); code != 200 {
		t.Fatalf("viewer key read = %d", code)
	}
	if code := get("/api/rules", "POST", `{"kind":"block","domain":"x.com"}`); code != http.StatusForbidden {
		t.Fatalf("viewer key write = %d, want 403", code)
	}
	key = "fg_wrong"
	if code := get("/api/overview", "GET", ""); code != http.StatusUnauthorized {
		t.Fatalf("bad key = %d", code)
	}
}

func TestCheckSiteAndTempAllow(t *testing.T) {
	srv := newTestServer(t)
	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	c.do("PUT", "/api/groups/kids", `{"id":"kids","name":"Kids","categories":["malware"],"apps":["tiktok"],"block":[],"allow":[],"safeSearch":true,"schedules":[]}`, true)

	code, body := c.do("GET", "/api/check?domain=www.TikTok.com", "", false)
	if code != 200 {
		t.Fatalf("check = %d %v", code, body)
	}
	if body["app"] != "TikTok" {
		t.Errorf("app = %v", body["app"])
	}
	byGroup := map[string]string{}
	for _, p := range body["profiles"].([]any) {
		row := p.(map[string]any)
		byGroup[row["groupId"].(string)] = row["action"].(string)
	}
	if byGroup["kids"] != "blocked" || byGroup["default"] != "allowed" {
		t.Errorf("profiles = %v", byGroup)
	}

	if code, body := c.do("POST", "/api/rules/temp", `{"domain":"tiktok.com","group":"kids","minutes":60}`, true); code != 200 {
		t.Fatalf("temp allow = %d %v", code, body)
	}
	_, body = c.do("GET", "/api/check?domain=www.tiktok.com", "", false)
	for _, p := range body["profiles"].([]any) {
		row := p.(map[string]any)
		if row["groupId"] == "kids" && row["action"] != "allowed" {
			t.Errorf("after temp allow kids = %v", row)
		}
	}
	_, rules := c.do("GET", "/api/rules", "", false)
	if temp := rules["temp"].([]any); len(temp) != 1 {
		t.Errorf("temp rules = %v", temp)
	}
	if code, _ := c.do("DELETE", "/api/rules/temp", `{"domain":"tiktok.com","group":"kids"}`, true); code != 200 {
		t.Fatal("delete temp allow failed")
	}
	if code, body := c.do("POST", "/api/protection/pause", `{"minutes":30}`, true); code != 200 {
		t.Fatalf("pause = %d %v", code, body)
	}
	_, ov := c.do("GET", "/api/overview", "", false)
	if ov["protection"].(map[string]any)["paused"] != true {
		t.Error("overview doesn't show protection paused")
	}
	_ = time.Now
}
