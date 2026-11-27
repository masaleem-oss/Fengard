// Package routerui keeps fengard's page in the router's own web interface (luci) in place.
//
// a firmware upgrade with keep settings, or a reset and restoring a backup, brings back only
// what's in /etc/sysupgrade.conf, /lib/upgrade/keep.d and changed conffiles. that's fengard's
// program, service and /etc/fengard, so it comes back running, but the luci page under /usr/share
// and /www that the package put there is gone, and opkg or apk no longer lists the package. so
// fengardd puts the page back each time it starts, for installer and package installs alike
package routerui

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/masaleem-oss/Fengard/install"
	"github.com/masaleem-oss/Fengard/internal/persist"
)

type File struct {
	Path string // under the root, no leading slash
	Data []byte
	Mode fs.FileMode
}

// the page's files, the ipk and apk packages ship exactly these
func LuCIFiles(logo []byte) []File {
	return []File{
		{"usr/libexec/fengard-luci", lf(install.LuCIHelper), 0o755},
		{"usr/share/luci/menu.d/luci-app-fengard.json", lf(install.LuCIMenu), 0o644},
		{"usr/share/rpcd/acl.d/luci-app-fengard.json", lf(install.LuCIACL), 0o644},
		{"www/luci-static/resources/view/fengard.js", lf(install.LuCIView), 0o644},
		{"www/luci-static/resources/fengard/logo.svg", logo, 0o644},
	}
}

const (
	uninstallPath = "etc/fengard/uninstall.sh"
	versionPath   = "usr/share/fengard/version"
)

type Healer struct {
	Root    string // / on the router, a temp dir in tests
	Version string // shown on the luci page
	Logo    []byte
	// rpcd reads the page's permissions when it loads so it reloads after they change
	Reload func() error
}

type Result struct {
	Uninstaller bool     // /etc/fengard/uninstall.sh was rewritten
	Page        []string // luci files that were missing or different
	NoLuCI      bool     // the firmware has no luci, nothing to put back
}

func (r Result) Changed() bool { return r.Uninstaller || len(r.Page) > 0 }

// writes only what's missing or different so a normal start never touches the flash
func (h Healer) Ensure() (Result, error) {
	var res Result
	var errs []error
	// the uninstaller from install time doesn't know about files a later version adds, like this
	// page on installer installs, so it follows the running program to always remove all of it
	if _, err := os.Stat(h.path(uninstallPath)); err == nil {
		ch, err := put(h.path(uninstallPath), lf(install.Uninstall), 0o755)
		res.Uninstaller = ch
		errs = append(errs, err)
	}
	// luci-base puts menu.d there, firmware without luci (or one too old for json menus) gets nothing
	if !isDir(h.path("usr/share/luci/menu.d")) || !isDir(h.path("www/luci-static/resources")) {
		res.NoLuCI = true
		return res, errors.Join(errs...)
	}
	for _, f := range LuCIFiles(h.Logo) {
		if len(f.Data) == 0 {
			continue
		}
		ch, err := put(h.path(f.Path), f.Data, f.Mode)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ch {
			res.Page = append(res.Page, "/"+f.Path)
		}
	}
	if h.Version != "" {
		_, err := put(h.path(versionPath), []byte(h.Version+"\n"), 0o644)
		errs = append(errs, err)
	}
	if len(res.Page) > 0 {
		// luci caches its menu, without this a browser already signed in doesn't see the entry
		caches, _ := filepath.Glob(h.path("tmp/luci-indexcache*"))
		for _, c := range append(caches, h.path("tmp/luci-modulecache")) {
			os.RemoveAll(c)
		}
		if h.Reload != nil {
			errs = append(errs, h.Reload())
		}
	}
	return res, errors.Join(errs...)
}

func (h Healer) path(p string) string {
	root := h.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

func put(path string, data []byte, mode fs.FileMode) (bool, error) {
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() && st.Mode().Perm() == mode {
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := persist.WriteFile(path, data, mode); err != nil {
		return false, err
	}
	return true, nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// a windows checkout can turn the scripts into crlf and ash can't run those
func lf(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
