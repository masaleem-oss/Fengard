// go run ./tools/release -version 1.0.0 -repo owner/name
// output goes to dist
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

type target struct {
	os, arch, env string
	router        bool
}

// armv5 covers every 32bit arm router and mips is softfloat since most router cpus have no fpu
var targets = []target{
	{"linux", "arm64", "", true},
	{"linux", "arm", "GOARM=5", true},
	{"linux", "mipsle", "GOMIPS=softfloat", true},
	{"linux", "mips", "GOMIPS=softfloat", true},
	{"linux", "mips64", "GOMIPS64=softfloat", true},
	{"linux", "mips64le", "GOMIPS64=softfloat", true},
	{"linux", "amd64", "", true},
	{"linux", "386", "GO386=softfloat", true},
	{"linux", "riscv64", "", true},
	{"linux", "loong64", "", true},
	{"windows", "amd64", "", false},
	{"windows", "arm64", "", false},
	{"darwin", "arm64", "", false},
	{"darwin", "amd64", "", false},
}

func (t target) name() string { return t.os + "-" + t.arch }
func (t target) exe() string {
	if t.os == "windows" {
		return "fengardd.exe"
	}
	return "fengardd"
}

func main() {
	version := flag.String("version", "", "version number, e.g. 1.0.0 (required)")
	repo := flag.String("repo", "masaleem-oss/Fengard", "github owner/name for the one line router install")
	only := flag.String("only", "", "comma-separated targets to build (for testing), e.g. linux-amd64,windows-amd64")
	out := flag.String("out", "dist", "output directory")
	flag.Parse()
	if *version == "" {
		log.Fatal("-version is required")
	}
	root, err := repoRoot()
	if err != nil {
		log.Fatal(err)
	}
	build := targets
	if *only != "" {
		want := strings.Split(*only, ",")
		build = nil
		for _, t := range targets {
			if slices.Contains(want, t.name()) {
				build = append(build, t)
			}
		}
		if len(build) == 0 {
			log.Fatalf("no targets match %q", *only)
		}
	}

	name := "fengard-" + *version
	dist := filepath.Join(root, *out)
	kit := filepath.Join(dist, name)
	assets := filepath.Join(dist, "assets")
	must(os.RemoveAll(kit))
	must(os.RemoveAll(assets))
	must(os.MkdirAll(assets, 0o755))
	tmp, err := os.MkdirTemp("", "fengard-release")
	must(err)
	defer os.RemoveAll(tmp)

	var wg sync.WaitGroup
	errs := make([]error, len(build))
	sem := make(chan struct{}, runtime.NumCPU())
	for i, t := range build {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs[i] = compile(root, t, *version, filepath.Join(tmp, t.name(), t.exe()))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			log.Fatalf("%s: %v", build[i].name(), err)
		}
		log.Printf("built %s", build[i].name())
	}

	releaseURL := "@RELEASE_URL@"
	if *repo != "" {
		releaseURL = "https://github.com/" + *repo + "/releases/latest/download"
	}
	routerInstall := lf(read(root, "install/router-install.sh"))
	routerInstall = bytes.Replace(routerInstall, []byte("RELEASE_URL='@RELEASE_URL@'"), []byte("RELEASE_URL='"+releaseURL+"'"), 1)
	routerUninstall := lf(read(root, "install/router-uninstall.sh"))

	// launchers on top rest in install and bin
	files := []kitFile{
		{"Install Fengard.cmd", crlf(read(root, "install/Install Fengard.cmd")), 0o755},
		{"install.sh", lf(read(root, "install/install.sh")), 0o755},
		{"install/fengard-setup.ps1", crlf(read(root, "install/fengard-setup.ps1")), 0o644},
		{"install/router-install.sh", routerInstall, 0o755},
		{"install/router-uninstall.sh", routerUninstall, 0o755},
		{"README.md", read(root, "README.md"), 0o644},
		{"LICENSE", read(root, "LICENSE"), 0o644},
		{"NOTICE", read(root, "NOTICE"), 0o644},
	}
	// all linux builds go in one bundle and only the right one gets unpacked
	var bundle bytes.Buffer
	gz := gzip.NewWriter(&bundle)
	tw := tar.NewWriter(gz)
	addTar(tw, "router-install.sh", routerInstall, 0o755)
	addTar(tw, "router-uninstall.sh", routerUninstall, 0o755)
	for _, t := range build {
		data := read(tmp, filepath.Join(t.name(), t.exe()))
		if t.os == "linux" {
			addTar(tw, "bin/"+t.name()+"/fengardd", data, 0o755)
		} else {
			files = append(files, kitFile{"bin/" + t.name() + "/" + t.exe(), data, 0o755})
		}
		if t.router {
			writeFile(filepath.Join(assets, "fengardd-"+t.name()+".gz"), gzipBytes(data), 0o644)
		}
	}
	must(tw.Close())
	must(gz.Close())
	files = append(files, kitFile{"install/linux-bundle.tar.gz", bundle.Bytes(), 0o644})

	for _, f := range files {
		writeFile(filepath.Join(kit, filepath.FromSlash(f.path)), f.data, f.mode)
	}
	zipPath := filepath.Join(dist, name+".zip")
	writeZip(zipPath, name, files)
	writeFile(filepath.Join(assets, name+".zip"), read(dist, name+".zip"), 0o644)
	writeFile(filepath.Join(assets, "router-install.sh"), routerInstall, 0o644)
	writeFile(filepath.Join(assets, "router-uninstall.sh"), routerUninstall, 0o644)
	writeSums(assets)
	log.Printf("kit:    %s", kit)
	log.Printf("zip:    %s", zipPath)
	log.Printf("assets: %s", assets)
}

type kitFile struct {
	path string
	data []byte
	mode os.FileMode
}

func compile(root string, t target, version, out string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version, "-o", out, "./cmd/fengardd")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.os, "GOARCH="+t.arch)
	if t.env != "" {
		cmd.Env = append(cmd.Env, t.env)
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v\n%s", err, b)
	}
	return nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("run from inside the Fengard source tree")
		}
		dir = parent
	}
}

func read(dir, name string) []byte {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	must(err)
	return b
}

func writeFile(path string, data []byte, mode os.FileMode) {
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	must(os.WriteFile(path, data, mode))
}

// sh needs lf and cmd wants crlf
func lf(b []byte) []byte   { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
func crlf(b []byte) []byte { return bytes.ReplaceAll(lf(b), []byte("\n"), []byte("\r\n")) }

func addTar(tw *tar.Writer, name string, data []byte, mode int64) {
	must(tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now(), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}))
	_, err := tw.Write(data)
	must(err)
}

func gzipBytes(data []byte) []byte {
	var b bytes.Buffer
	w, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	_, err := w.Write(data)
	must(err)
	must(w.Close())
	return b.Bytes()
}

func writeZip(path, top string, files []kitFile) {
	f, err := os.Create(path)
	must(err)
	zw := zip.NewWriter(f)
	for _, kf := range files {
		h := &zip.FileHeader{Name: top + "/" + kf.path, Method: zip.Deflate, Modified: time.Now()}
		h.SetMode(kf.mode) // keeps exec bit on mac and linux
		w, err := zw.CreateHeader(h)
		must(err)
		_, err = w.Write(kf.data)
		must(err)
	}
	must(zw.Close())
	must(f.Close())
}

func writeSums(dir string) {
	entries, err := os.ReadDir(dir)
	must(err)
	var lines []string
	for _, e := range entries {
		f, err := os.Open(filepath.Join(dir, e.Name()))
		must(err)
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		must(err)
		lines = append(lines, fmt.Sprintf("%x  %s", h.Sum(nil), e.Name()))
	}
	sort.Strings(lines)
	writeFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
