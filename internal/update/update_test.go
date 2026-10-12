package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.1", "1.0.0", 1},
		{"1.0.10", "1.0.9", 1},
		{"v1.2.0", "1.2.0", 0},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0", "0.5.0-dev", 1},
		{"0.9", "1.0.0", -1},
		{"1.3.0.1", "1.3.0", 1},
		{"1.3.1", "1.3.0.1", 1},
		{"1.3.0.2", "1.3.0.10", -1},
		{"v1.3.0.0", "1.3.0", 0},
		{"1.4.0", "1.3.0.9", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// fake github with one release and an optional manifest
func fakeGitHub(t *testing.T, tag string, assets []string, m *Manifest) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			rel := release{Tag: tag, Page: srv.URL + "/page"}
			for _, a := range assets {
				rel.Assets = append(rel.Assets, asset{Name: a, URL: srv.URL + "/" + a})
			}
			json.NewEncoder(w).Encode(rel)
		case "/fengard-release.json":
			json.NewEncoder(w).Encode(m)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheck(t *testing.T) {
	bin := "fengardd-" + Target() + ".gz"
	cases := []struct {
		name          string
		tag           string
		assets        []string
		manifest      *Manifest
		newer, ok     bool
		notifications int
	}{
		{"same version", "v1.0.0", []string{bin}, nil, false, true, 0},
		{"newer and supported", "v1.1.0", []string{bin, "fengard-release.json"}, &Manifest{Targets: []string{Target()}}, true, true, 1},
		{"newer, cpu dropped", "v1.1.0", []string{bin, "fengard-release.json"}, &Manifest{Targets: []string{"linux-other"}}, true, false, 1},
		{"newer, needs absurd ram", "v1.1.0", []string{bin, "fengard-release.json"}, &Manifest{MinRAMMB: 1 << 30}, true, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := fakeGitHub(t, c.tag, c.assets, c.manifest)
			n := 0
			u := &Updater{API: srv.URL + "/latest", Current: "1.0.0", Client: srv.Client(),
				OnNotice: func(kind, title, detail string) { n++ }}
			st := u.Check(context.Background())
			if c.name == "newer, needs absurd ram" && ramMB() == 0 {
				t.Skip("no /proc/meminfo here")
			}
			if st.Newer != c.newer || st.Supported != c.ok {
				t.Fatalf("newer=%v supported=%v reason=%q", st.Newer, st.Supported, st.Reason)
			}
			u.Check(context.Background()) // same version again must not alert twice
			if n != c.notifications {
				t.Fatalf("%d notifications, want %d", n, c.notifications)
			}
		})
	}
}

func TestCheckOffline(t *testing.T) {
	u := &Updater{API: "http://127.0.0.1:1/latest", Current: "1.0.0", Client: http.DefaultClient}
	st := u.Check(context.Background())
	if st.Error == "" || st.Available() {
		t.Fatalf("offline check = %+v", st)
	}
}

func TestOddVersion(t *testing.T) {
	u := &Updater{Current: "1.2.0"}
	for _, v := range []string{"1.3.0$(reboot)", "1.3.0;rm", "1.3.0 x", ""} {
		if err := u.install(context.Background(), &release{}, v, false); err == nil || !strings.Contains(err.Error(), "odd version") {
			t.Errorf("%q: %v", v, err)
		}
	}
}

// github's releases/latest skips pre-releases, the list has everything newest first
func fakeReleases(t *testing.T, latest string, list []release) *httptest.Server {
	t.Helper()
	bin := "fengardd-" + Target() + ".gz"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withAssets := func(rel release) release {
			rel.Assets = []asset{{Name: bin, URL: srv.URL + "/" + bin}, {Name: "SHA256SUMS", URL: srv.URL + "/sums"}}
			return rel
		}
		switch {
		case r.URL.Path == "/latest":
			json.NewEncoder(w).Encode(withAssets(release{Tag: latest}))
		case r.URL.Path == "/" && r.URL.Query().Get("per_page") != "":
			out := []release{}
			for _, rel := range list {
				out = append(out, withAssets(rel))
			}
			json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPreviewBuilds(t *testing.T) {
	list := []release{
		{Tag: "v1.3.0.2", Prerelease: true, Draft: true}, // drafts never count
		{Tag: "v1.3.0.1", Prerelease: true},
		{Tag: "v1.3.0"},
		{Tag: "v1.2.6"},
	}
	srv := fakeReleases(t, "v1.3.0", list)
	preview := false
	u := &Updater{API: srv.URL + "/latest", Current: "1.3.0", Client: srv.Client(), Preview: func() bool { return preview }}

	st := u.Check(context.Background())
	if st.Newer || st.Latest != "1.3.0" || st.Prerelease {
		t.Fatalf("preview off must stay on the stable line: %+v", st)
	}

	preview = true
	st = u.Check(context.Background())
	if !st.Newer || st.Latest != "1.3.0.1" || !st.Prerelease {
		t.Fatalf("preview on should find 1.3.0.1: %+v", st)
	}

	// switching it back off hides the preview straight away and nothing installs it
	preview = false
	if st = u.Status(); st.Newer || st.Latest != "" || st.Prerelease {
		t.Fatalf("preview turned off still shows %+v", st)
	}
	if err := u.Install(context.Background()); err == nil {
		t.Fatal("installed a preview with preview builds off")
	}
}

// even if github ever handed a pre-release out as latest, nobody without preview builds takes it
func TestLatestPrereleaseIgnored(t *testing.T) {
	bin := "fengardd-" + Target() + ".gz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(release{Tag: "v1.3.0.1", Prerelease: true, Assets: []asset{{Name: bin, URL: "x"}}})
	}))
	defer srv.Close()
	u := &Updater{API: srv.URL + "/latest", Current: "1.3.0", Client: srv.Client()}
	if st := u.Check(context.Background()); st.Newer || st.Available() {
		t.Fatalf("offered a pre-release: %+v", st)
	}
}

// someone on a preview gets the next stable release once it's out
func TestPreviewToStable(t *testing.T) {
	srv := fakeReleases(t, "v1.3.1", []release{{Tag: "v1.3.1"}, {Tag: "v1.3.0.1", Prerelease: true}})
	for _, preview := range []bool{true, false} {
		u := &Updater{API: srv.URL + "/latest", Current: "1.3.0.1", Client: srv.Client(), Preview: func() bool { return preview }}
		if st := u.Check(context.Background()); !st.Newer || st.Latest != "1.3.1" || st.Prerelease {
			t.Fatalf("preview %v: %+v", preview, st)
		}
	}
}

func TestOlder(t *testing.T) {
	list := []release{
		{Tag: "v1.3.1.2"},
		{Tag: "v1.3.1.1", Prerelease: true},
		{Tag: "v1.3.1"},
		{Tag: "v1.3.0"},
		{Tag: "v1.2.6"}, // older than OldestRollback
		{Tag: "v1.3.0.5", Prerelease: true, Draft: true},
	}
	srv := fakeReleases(t, "v1.3.1", list)
	preview := false
	u := &Updater{API: srv.URL + "/latest", Current: "1.3.1.2", Client: srv.Client(), Preview: func() bool { return preview }}
	versions := func() string {
		got, err := u.Older(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var v []string
		for _, o := range got {
			v = append(v, o.Version)
		}
		return strings.Join(v, " ")
	}
	if got := versions(); got != "1.3.1 1.3.0" {
		t.Fatalf("previews off: %q", got)
	}
	preview = true
	if got := versions(); got != "1.3.1.1 1.3.1 1.3.0" {
		t.Fatalf("previews on: %q", got)
	}
}

// a router install as far as canInstall can tell
func fakeRouter(t *testing.T) (initScript, state string) {
	t.Helper()
	dir := t.TempDir()
	initScript = filepath.Join(dir, "fengard")
	os.WriteFile(initScript, []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "install.env"), nil, 0o644)
	return initScript, dir
}

func TestRollbackRefuses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only routers can switch versions")
	}
	srv := fakeReleases(t, "v1.3.1", []release{{Tag: "v1.3.1"}, {Tag: "v1.2.6"}})
	initScript, state := fakeRouter(t)
	u := &Updater{API: srv.URL + "/latest", Current: "1.3.1.2", Client: srv.Client(), InitScript: initScript, StateDir: state, WorkDir: t.TempDir()}
	for _, v := range []string{"1.2.6", "1.3.2", "1.3.1.2", "9.9.9"} {
		if err := u.Rollback(context.Background(), v); err == nil || !strings.Contains(err.Error(), "isn't one this router can go back to") {
			t.Errorf("%s: %v", v, err)
		}
	}
	// a real choice gets as far as the download, which this fake server doesn't have
	if err := u.Rollback(context.Background(), "1.3.1"); err == nil {
		t.Fatal("expected the missing download to fail")
	}
	if st := u.Status(); st.Installing || st.To != "" {
		t.Fatalf("a failed rollback left %+v", st)
	}
}

func TestHold(t *testing.T) {
	_, state := fakeRouter(t)
	u := &Updater{Current: "1.3.1", StateDir: state}
	if u.held("1.3.1.2") {
		t.Fatal("held with no hold file")
	}
	os.WriteFile(filepath.Join(state, "update-hold"), []byte("1.3.1.2\n"), 0o644)
	if !u.held("1.3.1.2") || !u.held("1.3.1.1") || u.held("1.3.1.3") || u.held("1.3.2") {
		t.Fatal("hold should skip 1.3.1.2 and older only")
	}
	if u.Status().Held != "1.3.1.2" {
		t.Fatal("hold ahead of this version should show")
	}
	os.WriteFile(filepath.Join(state, "update-hold"), []byte("1.3.1;reboot"), 0o644)
	if u.Status().Held != "" {
		t.Fatal("an odd hold file was trusted")
	}
	os.WriteFile(filepath.Join(state, "update-hold"), []byte("1.3.1"), 0o644)
	if u.Status().Held != "" {
		t.Fatal("a hold at the version running now isn't shown")
	}
}

func TestApplyScriptHold(t *testing.T) {
	back := applyScript("/usr/bin/fengardd", "/tmp/n", "/etc/init.d/fengard", "/etc/fengard", "1.3.1", "1.3.1.2", true)
	fwd := applyScript("/usr/bin/fengardd", "/tmp/n", "/etc/init.d/fengard", "/etc/fengard", "1.3.1.2", "1.3.1", false)
	if !strings.Contains(back, `echo "1.3.1.2" >"$STATE/update-hold"`) || strings.Contains(back, "rm -f \"$STATE/update-hold\"") {
		t.Fatalf("going back should hold at 1.3.1.2:\n%s", back)
	}
	if !strings.Contains(fwd, `rm -f "$STATE/update-hold"`) {
		t.Fatal("going forward should clear the hold")
	}
	for _, s := range []string{back, fwd} {
		if !strings.Contains(s, `config.json" "$D/config.`) || !strings.Contains(s, `>"$STATE/previous-version"`) {
			t.Fatal("settings backup or previous version missing")
		}
	}
}
