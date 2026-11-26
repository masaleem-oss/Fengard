package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/integrity"
)

func TestSecurityCheckAPI(t *testing.T) {
	root := t.TempDir()
	passwd := filepath.Join(root, "etc", "passwd")
	os.MkdirAll(filepath.Dir(passwd), 0o755)
	os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/ash\n"), 0o644)
	checker := &integrity.Checker{
		Collector: &integrity.Collector{Root: root, UCI: func(context.Context) ([]byte, error) { return nil, nil }},
		Available: true,
	}
	srv := newTestServer(t, func(s *Server) { s.Integrity = checker })
	admin := newClient(t, srv)
	if code, _ := admin.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true); code != 200 {
		t.Fatal("setup")
	}
	admin.do("POST", "/api/users", `{"username":"viewer","password":"viewer password 1","role":"viewer"}`, true)
	viewer := newClient(t, srv)
	if code, _ := viewer.do("POST", "/api/login", `{"username":"viewer","password":"viewer password 1"}`, true); code != 200 {
		t.Fatal("viewer login")
	}

	code, st := viewer.do("GET", "/api/security", "", false)
	if code != 200 || st["available"] != true || st["enabled"] != true || len(st["watches"].([]any)) != len(integrity.Watches) {
		t.Fatalf("status %d %v", code, st)
	}
	if code, _ := viewer.do("POST", "/api/security/check", "", true); code != http.StatusForbidden {
		t.Errorf("viewer started a check: %d", code)
	}
	if code, _ := viewer.do("POST", "/api/security/approve", `{"all":true}`, true); code != http.StatusForbidden {
		t.Errorf("viewer approved: %d", code)
	}

	// the dashboard's check now runs in the background, here the same check runs directly
	if code, _ := admin.do("POST", "/api/security/check", "", true); code != 200 {
		t.Errorf("check now = %d", code)
	}
	checker.Check(context.Background())
	os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/ash\ntoor:x:0:0::/root:/bin/ash\n"), 0o644)
	checker.Check(context.Background())
	_, st = admin.do("GET", "/api/security", "", false)
	changes := st["changes"].([]any)
	if len(changes) != 1 || st["baseline"] == nil || st["lastCheck"] == nil {
		t.Fatalf("after a change %v", st)
	}
	ch := changes[0].(map[string]any)
	if ch["watch"] != "accounts" || ch["kind"] != "added" || ch["item"] != "toor" || ch["what"] == "" || ch["firstSeen"] == nil {
		t.Errorf("change %v", ch)
	}
	if code, _ := admin.do("POST", "/api/security/approve", `{}`, true); code != http.StatusBadRequest {
		t.Errorf("empty approve = %d", code)
	}
	if code, body := admin.do("POST", "/api/security/approve", `{"ids":["`+ch["id"].(string)+`"]}`, true); code != 200 || body["approved"] != float64(1) {
		t.Errorf("approve = %d %v", code, body)
	}
	if _, st = admin.do("GET", "/api/security", "", false); len(st["changes"].([]any)) != 0 {
		t.Errorf("still listed %v", st["changes"])
	}

	// the watches are settings like any other
	_, set := admin.do("GET", "/api/settings", "", false)
	set["securityUnwatched"] = []string{"ports"}
	set["securityCheckOff"] = true
	if code, body := admin.do("PUT", "/api/settings", toJSON(set), true); code != 200 {
		t.Fatalf("settings %d %v", code, body)
	}
	_, st = admin.do("GET", "/api/security", "", false)
	if st["enabled"] != false {
		t.Error("turning it off didn't stick")
	}
	for _, w := range st["watches"].([]any) {
		w := w.(map[string]any)
		if (w["id"] == "ports") == w["on"].(bool) {
			t.Errorf("watch %v", w)
		}
	}
	set["securityUnwatched"] = []string{"nope"}
	if code, _ := admin.do("PUT", "/api/settings", toJSON(set), true); code != http.StatusBadRequest {
		t.Errorf("unknown watch saved: %d", code)
	}

	// the audit log has the approval
	req, _ := http.NewRequest("GET", srv.URL+"/api/audit", nil)
	resp, err := admin.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Approved 1 security check change") || !strings.Contains(string(b), "Ran the security check") {
		t.Errorf("audit %s", b)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestSecurityCheckOnAComputer(t *testing.T) {
	srv := newTestServer(t, func(s *Server) {
		s.Integrity = &integrity.Checker{Collector: &integrity.Collector{Root: t.TempDir()}}
	})
	c := newClient(t, srv)
	c.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	if code, st := c.do("GET", "/api/security", "", false); code != 200 || st["available"] != false {
		t.Errorf("status %d %v", code, st)
	}
	if code, _ := c.do("POST", "/api/security/check", "", true); code != http.StatusNotFound {
		t.Errorf("check on a computer = %d", code)
	}
	// no checker at all is the same
	bare := newClient(t, newTestServer(t))
	bare.do("POST", "/api/setup", `{"username":"admin","password":"correct horse battery"}`, true)
	if code, st := bare.do("GET", "/api/security", "", false); code != 200 || st["available"] != false {
		t.Errorf("no checker %d %v", code, st)
	}
}
