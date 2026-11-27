package routerui

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/masaleem-oss/Fengard/install"
)

var logo = []byte("<svg/>")

// a router with luci that just came back from a firmware upgrade: fengard's kept files only
func upgraded(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"usr/share/luci/menu.d", "usr/share/rpcd/acl.d", "www/luci-static/resources", "etc/fengard", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, root, "etc/fengard/uninstall.sh", []byte("#!/bin/sh\n# an older uninstaller\n"), 0o755)
	write(t, root, "tmp/luci-indexcache.abc.json", []byte("{}"), 0o644)
	if err := os.MkdirAll(filepath.Join(root, "tmp/luci-modulecache"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func write(t *testing.T, root, p string, b []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, p), b, mode); err != nil {
		t.Fatal(err)
	}
}

func exists(root, p string) bool {
	_, err := os.Stat(filepath.Join(root, p))
	return err == nil
}

func TestEnsurePutsThePageBack(t *testing.T) {
	root := upgraded(t)
	reloads := 0
	h := Healer{Root: root, Version: "1.3.5", Logo: logo, Reload: func() error { reloads++; return nil }}
	res, err := h.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Page) != 5 || !res.Uninstaller || res.NoLuCI {
		t.Fatalf("result %+v", res)
	}
	for _, f := range LuCIFiles(logo) {
		b, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil || !bytes.Equal(b, f.Data) {
			t.Fatalf("%s not written: %v", f.Path, err)
		}
		if runtime.GOOS != "windows" {
			st, _ := os.Stat(filepath.Join(root, f.Path))
			if st.Mode().Perm() != f.Mode {
				t.Fatalf("%s mode %v want %v", f.Path, st.Mode().Perm(), f.Mode)
			}
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, uninstallPath)); !bytes.Equal(b, lf(install.Uninstall)) {
		t.Fatal("uninstaller not brought up to date")
	}
	if b, _ := os.ReadFile(filepath.Join(root, versionPath)); string(b) != "1.3.5\n" {
		t.Fatalf("version %q", b)
	}
	if exists(root, "tmp/luci-indexcache.abc.json") || exists(root, "tmp/luci-modulecache") {
		t.Fatal("luci's menu cache was left behind")
	}
	if reloads != 1 {
		t.Fatalf("rpcd reloaded %d times", reloads)
	}

	// the next start finds everything in place and touches nothing
	write(t, root, "tmp/luci-indexcache.def.json", []byte("{}"), 0o644)
	res, err = h.Ensure()
	if err != nil || res.Changed() {
		t.Fatalf("second start changed %+v %v", res, err)
	}
	if reloads != 1 || !exists(root, "tmp/luci-indexcache.def.json") {
		t.Fatal("reloaded rpcd or cleared the cache with nothing changed")
	}
}

func TestEnsureFixesOneFile(t *testing.T) {
	root := upgraded(t)
	h := Healer{Root: root, Logo: logo, Reload: func() error { return nil }}
	if _, err := h.Ensure(); err != nil {
		t.Fatal(err)
	}
	// an older version's page, and the helper having lost its exec bit
	write(t, root, "www/luci-static/resources/view/fengard.js", []byte("old"), 0o644)
	if err := os.Chmod(filepath.Join(root, "usr/libexec/fengard-luci"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := h.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/libexec/fengard-luci", "/www/luci-static/resources/view/fengard.js"}
	if runtime.GOOS == "windows" {
		want = want[1:]
	}
	if len(res.Page) != len(want) || res.Page[0] != want[0] || res.Uninstaller {
		t.Fatalf("result %+v want %v", res, want)
	}
}

func TestEnsureWithoutLuCI(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/fengard"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "etc/fengard/uninstall.sh", []byte("old"), 0o755)
	h := Healer{Root: root, Version: "1.3.5", Logo: logo, Reload: func() error { t.Fatal("reloaded rpcd without luci"); return nil }}
	res, err := h.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if !res.NoLuCI || len(res.Page) != 0 || !res.Uninstaller {
		t.Fatalf("result %+v", res)
	}
	if exists(root, "usr") || exists(root, "www") {
		t.Fatal("made luci directories on a router without luci")
	}
}

func TestEnsureNotInstalled(t *testing.T) {
	root := upgraded(t)
	os.Remove(filepath.Join(root, uninstallPath))
	res, err := Healer{Root: root, Logo: logo}.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if res.Uninstaller || exists(root, uninstallPath) {
		t.Fatal("wrote an uninstaller that wasn't there")
	}
}

// the uninstaller removes every file this puts there, else a removal leaves a dead menu entry
func TestUninstallRemovesThePage(t *testing.T) {
	sh := string(install.Uninstall)
	for _, f := range LuCIFiles(logo) {
		p := "/" + f.Path
		if filepath.Base(filepath.Dir(f.Path)) == "fengard" {
			p = "/" + filepath.ToSlash(filepath.Dir(f.Path))
		}
		if !bytes.Contains([]byte(sh), []byte(p)) {
			t.Errorf("router-uninstall.sh doesn't remove %s", p)
		}
	}
	if !bytes.Contains([]byte(sh), []byte("/"+filepath.ToSlash(filepath.Dir(versionPath)))) {
		t.Errorf("router-uninstall.sh doesn't remove %s", versionPath)
	}
}
