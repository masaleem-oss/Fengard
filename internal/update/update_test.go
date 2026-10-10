package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
