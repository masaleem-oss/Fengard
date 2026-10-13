package features

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
)

const src = "../../features"

// the manifest is generated, a changed add-on file without go run ./tools/featurepack fails here
func TestManifestIsCurrent(t *testing.T) {
	packs, err := Scan(src, config.Features)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != len(Packs) {
		t.Fatalf("manifest has %d add-ons, features/ has %d", len(Packs), len(packs))
	}
	for i, p := range packs {
		if p.ID != Packs[i].ID || !slices.Equal(p.Files, Packs[i].Files) {
			t.Errorf("%s changed, run go run ./tools/featurepack", p.ID)
		}
	}
}

type recorder struct {
	mu  sync.Mutex
	got []string
}

func (r *recorder) note(id string, ready bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := "-" + id
	if ready {
		s = "+" + id
	}
	r.got = append(r.got, s)
}

func manager(t *testing.T, want []string, sources ...string) (*Manager, *recorder) {
	rec := &recorder{}
	m := &Manager{Dir: filepath.Join(t.TempDir(), "features"), Sources: sources, Wanted: func() []string { return want }, OnChange: rec.note}
	return m, rec
}

func TestInstallFromSourceTree(t *testing.T) {
	m, rec := manager(t, []string{"vpn", "presence"}, src)
	m.Check()
	m.reconcile(context.Background())
	if !m.Ready("vpn") || !m.Ready("presence") || m.Ready("vendors") {
		t.Fatalf("ready: vpn %v presence %v vendors %v", m.Ready("vpn"), m.Ready("presence"), m.Ready("vendors"))
	}
	if !slices.Equal(rec.got, []string{"+vpn", "+presence"}) {
		t.Errorf("changes %v", rec.got)
	}
	if _, err := os.Stat(m.Path("vpn", "page.js")); err != nil {
		t.Error(err)
	}
	// nothing changes the second time
	m.reconcile(context.Background())
	if len(rec.got) != 2 {
		t.Errorf("changes %v", rec.got)
	}
}

func archives(t *testing.T) string {
	dir := t.TempDir()
	for _, p := range Packs {
		b, err := Build(src, &p)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, Archive(p.ID)), b, 0o644)
	}
	return dir
}

func TestInstallFromArchivesAndRemove(t *testing.T) {
	want := []string{"vendors", "speedtest"}
	m, rec := manager(t, nil, archives(t))
	m.Wanted = func() []string { return want }
	m.reconcile(context.Background())
	if !m.Ready("vendors") || !m.Ready("speedtest") {
		t.Fatal("not installed")
	}
	want = []string{"speedtest"}
	m.reconcile(context.Background())
	if m.Ready("vendors") {
		t.Error("still ready after removing")
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "vendors")); !os.IsNotExist(err) {
		t.Error("files left behind")
	}
	if !slices.Equal(rec.got, []string{"+vendors", "+speedtest", "-vendors"}) {
		t.Errorf("changes %v", rec.got)
	}
}

func TestDownload(t *testing.T) {
	dir := archives(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
	}))
	defer srv.Close()
	m, _ := manager(t, []string{"devicecheck"}, "", t.TempDir(), srv.URL+"/v1/")
	m.reconcile(context.Background())
	if !m.Ready("devicecheck") || hits != 1 {
		t.Fatalf("ready %v after %d downloads", m.Ready("devicecheck"), hits)
	}
}

func TestTamperedDownloadIsRefused(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "presence"), 0o755)
	os.WriteFile(filepath.Join(dir, "presence", "widget.js"), []byte("alert(1)"), 0o644)
	m, rec := manager(t, []string{"presence"}, dir)
	m.reconcile(context.Background())
	if m.Ready("presence") || len(rec.got) != 0 {
		t.Fatal("installed a file that doesn't match the manifest")
	}
	if inf := m.List()[slices.IndexFunc(m.List(), func(i Info) bool { return i.ID == "presence" })]; inf.Error == "" || !inf.Wanted {
		t.Errorf("no error shown: %+v", inf)
	}
}

func TestArchiveCantWriteOutsideItsFolder(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, name := range []string{"../../escaped.js", "widget.js"} {
		body := []byte("x")
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 1, ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg})
		tw.Write(body)
	}
	tw.Close()
	zw.Close()
	to := filepath.Join(t.TempDir(), "a", "b")
	extract(&buf, Find("presence"), to)
	if _, err := os.Stat(filepath.Join(to, "..", "..", "escaped.js")); !os.IsNotExist(err) {
		t.Error("a file landed outside the add-on's folder")
	}
}

func TestFailedUpdateKeepsTheOldFiles(t *testing.T) {
	m, _ := manager(t, []string{"presence"}, src)
	m.reconcile(context.Background())
	// a newer fengardd wants different files and can't get them
	p := Find("presence")
	saved := p.Files[0].SHA256
	p.Files[0].SHA256 = "00"
	defer func() { p.Files[0].SHA256 = saved }()
	m.Sources = nil
	m.reconcile(context.Background())
	if !m.Ready("presence") {
		t.Error("the add-on went away while its update couldn't download")
	}
	if _, err := os.Stat(m.Path("presence", "widget.js")); err != nil {
		t.Error(err)
	}
}

func TestOfflineSaysWhy(t *testing.T) {
	m, _ := manager(t, []string{"vpn"})
	m.reconcile(context.Background())
	for _, i := range m.List() {
		if i.ID == "vpn" && (i.Ready || i.Error == "") {
			t.Errorf("%+v", i)
		}
	}
}
