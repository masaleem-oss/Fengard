package main

import (
	"archive/tar"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// one file going into an ipk or apk, paths have no leading slash
type pkgFile struct {
	path string
	data []byte
	mode int64
}

type pkgInfo struct {
	name, version, desc, url, license string
	files                             []pkgFile
	postinst, prerm, postrm           string
}

// opkg and apk both want a plain version number, test builds get 0.0.0
var plainVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

func pkgVersion(v string) string {
	if plainVersion.MatchString(v) {
		return v
	}
	return "0.0.0"
}

func (p pkgInfo) size() int {
	n := 0
	for _, f := range p.files {
		n += len(f.data)
	}
	return n
}

// ipk is a tar.gz of debian-binary control.tar.gz and data.tar.gz like openwrt's ipkg-build makes
func buildIPK(p pkgInfo) []byte {
	control := fmt.Sprintf("Package: %s\nVersion: %s\nLicense: %s\nSection: net\nURL: %s\nArchitecture: all\nInstalled-Size: %d\nDescription: %s\n",
		p.name, p.version, p.license, p.url, p.size(), p.desc)
	ctl := tarGz(func(tw *tar.Writer) {
		tarDir(tw, "./")
		tarFile(tw, "./control", []byte(control), 0o644)
		tarFile(tw, "./postinst", []byte(p.postinst), 0o755)
		tarFile(tw, "./prerm", []byte(p.prerm), 0o755)
		tarFile(tw, "./postrm", []byte(p.postrm), 0o755)
	})
	data := tarGz(func(tw *tar.Writer) {
		tarDir(tw, "./")
		seen := map[string]bool{}
		for _, f := range sortedFiles(p.files) {
			for _, d := range parents(f.path) {
				if !seen[d] {
					seen[d] = true
					tarDir(tw, "./"+d+"/")
				}
			}
			tarFile(tw, "./"+f.path, f.data, f.mode)
		}
	})
	return tarGz(func(tw *tar.Writer) {
		tarFile(tw, "./debian-binary", []byte("2.0\n"), 0o644)
		tarFile(tw, "./data.tar.gz", data, 0o644)
		tarFile(tw, "./control.tar.gz", ctl, 0o644)
	})
}

func tarGz(fill func(*tar.Writer)) []byte {
	var b bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	fill(tw)
	must(tw.Close())
	must(gz.Close())
	return b.Bytes()
}

func tarDir(tw *tar.Writer, name string) {
	must(tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, ModTime: time.Now(), Typeflag: tar.TypeDir, Uname: "root", Gname: "root", Format: tar.FormatGNU}))
}

func tarFile(tw *tar.Writer, name string, data []byte, mode int64) {
	must(tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now(), Typeflag: tar.TypeReg, Uname: "root", Gname: "root", Format: tar.FormatGNU}))
	_, err := tw.Write(data)
	must(err)
}

func parents(p string) []string {
	var out []string
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		out = append([]string{d}, out...)
	}
	return out
}

func sortedFiles(files []pkgFile) []pkgFile {
	out := slices.Clone(files)
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// apk v3 for openwrt 25.12 and later, written the way apk mkpkg does it
// the format is in apk-tools doc/apk-v3.5.scd and src/adb.h

const (
	adbSpecial = 0x00000000
	adbInt     = 0x10000000
	adbInt32   = 0x20000000
	adbInt64   = 0x30000000
	adbBlob8   = 0x80000000
	adbBlob16  = 0x90000000
	adbBlob32  = 0xa0000000
	adbArray   = 0xd0000000
	adbObject  = 0xe0000000
)

// adb is the tree part of the file, values point at offsets in it
type adb struct{ buf []byte }

func (a *adb) align(n int) {
	for len(a.buf)%n != 0 {
		a.buf = append(a.buf, 0)
	}
}

func (a *adb) blob(b []byte) uint32 {
	switch n := len(b); {
	case n == 0:
		return adbSpecial
	case n <= 0xff:
		off := len(a.buf)
		a.buf = append(append(a.buf, byte(n)), b...)
		return adbBlob8 | uint32(off)
	case n <= 0xffff:
		a.align(2)
		off := len(a.buf)
		a.buf = binary.LittleEndian.AppendUint16(a.buf, uint16(n))
		a.buf = append(a.buf, b...)
		return adbBlob16 | uint32(off)
	default:
		a.align(4)
		off := len(a.buf)
		a.buf = binary.LittleEndian.AppendUint32(a.buf, uint32(n))
		a.buf = append(a.buf, b...)
		return adbBlob32 | uint32(off)
	}
}

func (a *adb) str(s string) uint32 { return a.blob([]byte(s)) }

func (a *adb) int(v uint64) uint32 {
	switch {
	case v >= 1<<32:
		a.align(4)
		off := len(a.buf)
		a.buf = binary.LittleEndian.AppendUint64(a.buf, v)
		return adbInt64 | uint32(off)
	case v >= 0x10000000:
		a.align(4)
		off := len(a.buf)
		a.buf = binary.LittleEndian.AppendUint32(a.buf, uint32(v))
		return adbInt32 | uint32(off)
	}
	return adbInt | uint32(v)
}

// slot 0 holds the slot count and empty slots at the end are dropped
func (a *adb) obj(kind uint32, slots ...uint32) uint32 {
	n := len(slots)
	for n > 0 && slots[n-1] == adbSpecial {
		n--
	}
	if n == 0 {
		return adbSpecial
	}
	a.align(4)
	off := len(a.buf)
	a.buf = binary.LittleEndian.AppendUint32(a.buf, uint32(n+1))
	for _, s := range slots[:n] {
		a.buf = binary.LittleEndian.AppendUint32(a.buf, s)
	}
	return kind | uint32(off)
}

func buildAPK(p pkgInfo) []byte {
	a := &adb{buf: make([]byte, 8)} // header: compat version, version, reserved, root

	// a dir entry for every folder that holds files, sorted the way apk keeps them
	dirs := map[string][]pkgFile{}
	for _, f := range sortedFiles(p.files) {
		d := path.Dir(f.path)
		dirs[d] = append(dirs[d], f)
	}
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	acl := func(mode int64) uint32 { return a.obj(adbObject, a.int(uint64(mode)), a.str("root"), a.str("root")) }
	mtime := uint64(time.Now().Unix())
	var paths []uint32
	for _, d := range names {
		var files []uint32
		for _, f := range dirs[d] {
			sum := sha256.Sum256(f.data)
			files = append(files, a.obj(adbObject, a.str(path.Base(f.path)), acl(f.mode), a.int(uint64(len(f.data))), a.int(mtime), a.blob(sum[:])))
		}
		paths = append(paths, a.obj(adbObject, a.str(d), acl(0o755), a.obj(adbArray, files...)))
	}

	// the package id is filled in after hashing the tree with it zeroed
	idOff := len(a.buf) + 1
	id := a.blob(make([]byte, 20))
	info := a.obj(adbObject,
		a.str(p.name), a.str(p.version), id, a.str(p.desc), a.str("noarch"), a.str(p.license), a.str(p.name), adbSpecial,
		a.str(p.url), adbSpecial, a.int(mtime), a.int(uint64(max(p.size(), 1))))
	scripts := a.obj(adbObject, adbSpecial, adbSpecial, a.str(p.postinst), a.str(p.prerm), a.str(p.postrm), adbSpecial, a.str(p.postinst))
	root := a.obj(adbObject, info, a.obj(adbArray, paths...), scripts)
	binary.LittleEndian.PutUint32(a.buf[4:], root)
	sum := sha256.Sum256(a.buf)
	copy(a.buf[idOff:idOff+20], sum[:20])

	var out bytes.Buffer
	out.WriteString("ADB.")
	out.WriteString("pckg")
	adbBlock(&out, 0, a.buf)
	for i, d := range names {
		for j, f := range dirs[d] {
			if len(f.data) == 0 {
				continue
			}
			hdr := binary.LittleEndian.AppendUint32(nil, uint32(i+1))
			hdr = binary.LittleEndian.AppendUint32(hdr, uint32(j+1))
			adbBlock(&out, 2, append(hdr, f.data...))
		}
	}

	// ADBd then raw deflate of the whole thing, same as apk's default
	var z bytes.Buffer
	z.WriteString("ADBd")
	fw, _ := flate.NewWriter(&z, flate.BestCompression)
	_, err := fw.Write(out.Bytes())
	must(err)
	must(fw.Close())
	return z.Bytes()
}

// block header is 2 bits of type and 30 of size including the header, padded to 8
func adbBlock(out *bytes.Buffer, typ uint32, payload []byte) {
	if len(payload) > 0x3fffffff-4 {
		must(fmt.Errorf("apk block too big"))
	}
	var h [4]byte
	binary.LittleEndian.PutUint32(h[:], typ<<30|uint32(4+len(payload)))
	out.Write(h[:])
	out.Write(payload)
	for pad := (8 - (4+len(payload))%8) % 8; pad > 0; pad-- {
		out.WriteByte(0)
	}
}

// what goes in the package next to the program
func pkgFiles(root string, bin, routerInstall, routerUninstall []byte, version string) []pkgFile {
	return []pkgFile{
		{"usr/bin/fengardd", bin, 0o755},
		{"usr/share/fengard/router-install.sh", routerInstall, 0o755},
		{"usr/share/fengard/router-uninstall.sh", routerUninstall, 0o755},
		{"usr/share/fengard/version", []byte(version + "\n"), 0o644},
		{"usr/libexec/fengard-luci", lf(read(root, "install/luci/fengard-luci")), 0o755},
		{"usr/share/luci/menu.d/luci-app-fengard.json", lf(read(root, "install/luci/menu.json")), 0o644},
		{"usr/share/rpcd/acl.d/luci-app-fengard.json", lf(read(root, "install/luci/acl.json")), 0o644},
		{"www/luci-static/resources/view/fengard.js", lf(read(root, "install/luci/fengard.js")), 0o644},
		{"www/luci-static/resources/fengard/logo.svg", read(root, "internal/web/static/img/logo.svg"), 0o644},
	}
}

func pkgName(name, version, target, ext string) string {
	return fmt.Sprintf("%s_%s_%s.%s", name, version, strings.TrimPrefix(target, "linux-"), ext)
}
