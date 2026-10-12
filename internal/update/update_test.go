package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		if err := u.install(context.Background(), &release{}, v); err == nil || !strings.Contains(err.Error(), "odd version") {
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
