package integrity

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// the dirs inside the overlay worth watching, everything else there is settings that change all the time
var fileDirs = []string{
	"bin", "sbin", "usr/bin", "usr/sbin", "usr/lib", "lib", "www/cgi-bin",
	"etc/init.d", "etc/rc.d", "etc/hotplug.d", "etc/rc.local", "etc/profile", "etc/uci-defaults",
}

// Collector reads what's on the router, Root is / outside tests
type Collector struct {
	Root string
	// Exclude are fengard's own files, absolute paths, a dir excludes all under it
	Exclude []string
	// UCI runs uci -q show firewall, nil runs the real one
	UCI func(ctx context.Context) ([]byte, error)
	// Forwards are fengard's own port forwards as text, they count as wan exposure too
	Forwards func() []string

	MaxFileSize  int64 // bigger files are listed but not hashed
	MaxFiles     int
	MaxHashBytes int64 // total hashed per check
	// Pause runs every few MB hashed so a slow router gets some air
	Pause func()
}

const (
	defaultMaxFileSize  = 16 << 20
	defaultMaxFiles     = 10000
	defaultMaxHashBytes = 256 << 20
)

func (c *Collector) path(p string) string {
	root := c.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

// Firmware is the firmware version, empty when this isn't openwrt
func (c *Collector) Firmware() string {
	var parts []string
	if b, err := os.ReadFile(c.path("/etc/openwrt_release")); err == nil {
		vals := map[string]string{}
		for line := range strings.SplitSeq(string(b), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok {
				vals[k] = strings.Trim(v, `'"`)
			}
		}
		if d := vals["DISTRIB_DESCRIPTION"]; d != "" {
			parts = append(parts, d)
		} else if r := strings.TrimSpace(vals["DISTRIB_ID"] + " " + vals["DISTRIB_RELEASE"] + " " + vals["DISTRIB_REVISION"]); r != "" {
			parts = append(parts, r)
		}
	}
	if b, err := os.ReadFile(c.path("/etc/glversion")); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" && len(v) < 64 {
			parts = append(parts, "GL.iNet "+v)
		}
	}
	return strings.Join(parts, ", ")
}

// Collect reads one watch into key to value, values are compared as text
func (c *Collector) Collect(ctx context.Context, watch string) (map[string]string, bool, error) {
	switch watch {
	case Packages:
		m, err := c.packages()
		return m, false, err
	case Files:
		return c.files(ctx)
	case SSHKeys:
		m, err := c.sshKeys()
		return m, false, err
	case Accounts:
		m, err := c.accounts()
		return m, false, err
	case Startup:
		m, err := c.startup()
		return m, false, err
	case Ports:
		m, err := c.ports()
		return m, false, err
	case Exposure:
		m, err := c.exposure(ctx)
		return m, false, err
	}
	return nil, false, fmt.Errorf("unknown watch %q", watch)
}

// packages

func (c *Collector) packages() (map[string]string, error) {
	if f, err := os.Open(c.path("/usr/lib/opkg/status")); err == nil {
		defer f.Close()
		return parseOpkg(f)
	}
	if f, err := os.Open(c.path("/lib/apk/db/installed")); err == nil {
		defer f.Close()
		return parseApk(f)
	}
	return nil, errors.New("no package list found")
}

func parseOpkg(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	var name, ver string
	installed := false
	flush := func() {
		if name != "" && installed {
			out[name] = ver
		}
		name, ver, installed = "", "", false
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "Package: "):
			name = strings.TrimSpace(line[9:])
		case strings.HasPrefix(line, "Version: "):
			ver = strings.TrimSpace(line[9:])
		case strings.HasPrefix(line, "Status: "):
			installed = strings.HasSuffix(strings.TrimSpace(line), " installed")
		}
	}
	flush()
	return out, sc.Err()
}

func parseApk(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	var name, ver string
	flush := func() {
		if name != "" {
			out[name] = ver
		}
		name, ver = "", ""
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "P:"):
			name = line[2:]
		case strings.HasPrefix(line, "V:"):
			ver = line[2:]
		}
	}
	flush()
	return out, sc.Err()
}

// owners finds which installed package a file came from, only for the paths asked about
func (c *Collector) owners(paths []string) map[string]string {
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	out := map[string]string{}
	if len(want) == 0 {
		return out
	}
	dir := c.path("/usr/lib/opkg/info")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		pkg, ok := strings.CutSuffix(e.Name(), ".list")
		if !ok {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// lines are the path then sometimes a tab and the mode
			p, _, _ := strings.Cut(sc.Text(), "\t")
			if want[p] {
				out[p] = pkg
			}
		}
		f.Close()
	}
	return out
}

// files

func (c *Collector) excluded(p string) bool {
	for _, e := range c.Exclude {
		e = path.Clean(filepath.ToSlash(e))
		if p == e || strings.HasPrefix(p, e+"/") {
			return true
		}
		// the updater leaves fengardd.new and friends next to the program for a moment
		if strings.HasPrefix(p, e+".") && path.Base(e) == "fengardd" {
			return true
		}
	}
	base := path.Base(p)
	return strings.HasPrefix(p, "/etc/rc.d/") && strings.Contains(base, "fengard")
}

// files lists the changed system files, from the overlay when there is one
func (c *Collector) files(ctx context.Context) (map[string]string, bool, error) {
	base := c.path("/overlay/upper")
	overlay := true
	if st, err := os.Stat(base); err != nil || !st.IsDir() {
		base, overlay = c.path("/"), false
	}
	maxSize, maxFiles, maxBytes := c.MaxFileSize, c.MaxFiles, c.MaxHashBytes
	if maxSize <= 0 {
		maxSize = defaultMaxFileSize
	}
	if maxFiles <= 0 {
		maxFiles = defaultMaxFiles
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxHashBytes
	}
	out := map[string]string{}
	truncated := false
	var hashed, sincePause int64
	buf := make([]byte, 32<<10)
	for _, dir := range fileDirs {
		start := filepath.Join(base, filepath.FromSlash(dir))
		err := filepath.WalkDir(start, func(full string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // missing dirs and unreadable files are just skipped
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, rerr := filepath.Rel(base, full)
			if rerr != nil {
				return nil
			}
			p := "/" + filepath.ToSlash(rel)
			if c.excluded(p) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				// without an overlay /usr/lib holds the whole firmware, the lua and share bits aren't programs
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			mode := info.Mode()
			if strings.HasPrefix(p, "/etc/rc.d/") && mode&fs.ModeSymlink != 0 {
				return nil // enabled services, the startup watch lists those
			}
			if strings.HasPrefix(p, "/usr/lib/") && !program(p, mode) {
				return nil
			}
			if len(out) >= maxFiles {
				truncated = true
				return filepath.SkipAll
			}
			switch {
			case mode&fs.ModeSymlink != 0:
				t, _ := os.Readlink(full)
				out[p] = "link " + t
			case mode&fs.ModeCharDevice != 0 && overlay:
				// overlayfs marks a file deleted from the firmware like this
				out[p] = "deleted"
			case mode.IsRegular():
				v := fmt.Sprintf("file %d %s", info.Size(), mode.Perm())
				if info.Size() > maxSize || hashed+info.Size() > maxBytes {
					if hashed+info.Size() > maxBytes {
						truncated = true
					}
					out[p] = v + " unhashed"
					return nil
				}
				sum, n, herr := hashFile(full, buf)
				if herr != nil {
					out[p] = v + " unreadable"
					return nil
				}
				hashed += n
				sincePause += n
				if sincePause >= 4<<20 && c.Pause != nil {
					sincePause = 0
					c.Pause()
				}
				out[p] = v + " " + sum
			}
			return nil
		})
		if err != nil && !errors.Is(err, filepath.SkipAll) {
			return nil, false, err
		}
		if truncated && len(out) >= maxFiles {
			break
		}
	}
	return out, truncated, nil
}

// programs and libraries, /usr/lib is mostly lua, data and kernel modules otherwise
func program(p string, mode fs.FileMode) bool {
	if mode&fs.ModeSymlink != 0 {
		return strings.Contains(path.Base(p), ".so")
	}
	return mode&0o111 != 0 || strings.Contains(path.Base(p), ".so")
}

func hashFile(name string, buf []byte) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, f, buf)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ssh keys

var keyFiles = []string{"/etc/dropbear/authorized_keys", "/root/.ssh/authorized_keys"}

func (c *Collector) sshKeys() (map[string]string, error) {
	out := map[string]string{}
	for _, file := range keyFiles {
		f, err := os.Open(c.path(file))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			typ, fp, comment, ok := parseKey(sc.Text())
			if !ok {
				continue
			}
			v := typ
			if comment != "" {
				v += " " + comment
			}
			out[fp+" "+file] = v
		}
		f.Close()
	}
	return out, nil
}

// parseKey reads one authorized_keys line, options in front are skipped
func parseKey(line string) (typ, fingerprint, comment string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	fields := strings.Fields(line)
	for i := 0; i+1 < len(fields); i++ {
		if !strings.HasPrefix(fields[i], "ssh-") && !strings.HasPrefix(fields[i], "ecdsa-") && !strings.HasPrefix(fields[i], "sk-") {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(fields[i+1])
		if err != nil {
			continue
		}
		sum := sha256.Sum256(blob)
		return fields[i], "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), strings.Join(fields[i+2:], " "), true
	}
	return
}

// accounts

var noLogin = []string{"", "/bin/false", "/sbin/nologin", "/usr/sbin/nologin", "/bin/nologin", "/usr/bin/nologin"}

func (c *Collector) accounts() (map[string]string, error) {
	f, err := os.Open(c.path("/etc/passwd"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) < 7 || p[0] == "" {
			continue
		}
		if p[2] != "0" && slices.Contains(noLogin, strings.TrimSpace(p[6])) {
			continue
		}
		out[p[0]] = "uid " + p[2] + " shell " + strings.TrimSpace(p[6])
	}
	return out, sc.Err()
}

// startup and scheduled jobs

func (c *Collector) startup() (map[string]string, error) {
	out := map[string]string{}
	if entries, err := os.ReadDir(c.path("/etc/rc.d")); err == nil {
		for _, e := range entries {
			n := e.Name()
			if len(n) < 4 || n[0] != 'S' || n[1] < '0' || n[1] > '9' || strings.Contains(n, "fengard") {
				continue
			}
			svc := strings.TrimLeft(n[1:], "0123456789")
			if svc != "" {
				out["service "+svc] = "on"
			}
		}
	}
	if entries, err := os.ReadDir(c.path("/etc/crontabs")); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := readSmall(filepath.Join(c.path("/etc/crontabs"), e.Name()), 256<<10)
			if err != nil {
				continue
			}
			for line := range strings.SplitSeq(string(b), "\n") {
				line = strings.Join(strings.Fields(line), " ")
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				out["cron "+e.Name()+": "+line] = "on"
			}
		}
	}
	return out, nil
}

func readSmall(name string, limit int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// open ports

func (c *Collector) ports() (map[string]string, error) {
	own := c.ownSockets()
	names := c.socketOwners()
	out := map[string]string{}
	read := 0
	for _, proto := range []string{"tcp", "tcp6", "udp", "udp6"} {
		f, err := os.Open(c.path("/proc/net/" + proto))
		if err != nil {
			continue
		}
		read++
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			l, ok := parseSocket(proto, sc.Text())
			if !ok || own[l.inode] {
				continue
			}
			out[l.key()] = names[l.inode]
		}
		f.Close()
	}
	if read == 0 {
		return nil, errors.New("no socket list in /proc")
	}
	return out, nil
}

type listener struct {
	proto string
	addr  netip.Addr
	port  int
	inode string
}

func (l listener) key() string {
	p := strings.TrimSuffix(l.proto, "6")
	a := l.addr.String()
	if l.addr.IsUnspecified() {
		a = "*"
	}
	if l.addr.Is6() && !l.addr.IsUnspecified() {
		a = "[" + a + "]"
	}
	if l.addr.Is6() && l.addr.IsUnspecified() {
		a = "[*]"
	}
	return p + " " + a + ":" + strconv.Itoa(l.port)
}

// parseSocket reads a /proc/net line, only listening tcp and unconnected udp count
func parseSocket(proto, line string) (listener, bool) {
	f := strings.Fields(line)
	if len(f) < 10 {
		return listener{}, false
	}
	udp := strings.HasPrefix(proto, "udp")
	if (!udp && f[3] != "0A") || (udp && f[3] != "07") {
		return listener{}, false
	}
	host, portHex, ok := strings.Cut(f[1], ":")
	if !ok {
		return listener{}, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil || port == 0 {
		return listener{}, false
	}
	// udp clients sit on random high ports, those come and go all day
	if udp && port >= 32768 {
		return listener{}, false
	}
	raw, err := hex.DecodeString(host)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return listener{}, false
	}
	// the kernel prints each 32 bit word in host order, so little endian routers need it flipped
	if littleEndian {
		for i := 0; i < len(raw); i += 4 {
			raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
		}
	}
	addr, _ := netip.AddrFromSlice(raw)
	addr = addr.Unmap()
	return listener{proto: proto, addr: addr, port: int(port), inode: f[9]}, true
}

var littleEndian = binary.NativeEndian.Uint16([]byte{1, 0}) == 1

// ownSockets are fengard's own, its dns and dashboard aren't news
func (c *Collector) ownSockets() map[string]bool {
	out := map[string]bool{}
	dir := c.path("/proc/self/fd")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if in, ok := socketInode(filepath.Join(dir, e.Name())); ok {
			out[in] = true
		}
	}
	return out
}

// socketOwners maps socket inodes to program names, best effort
func (c *Collector) socketOwners() map[string]string {
	out := map[string]string{}
	procs, err := os.ReadDir(c.path("/proc"))
	if err != nil {
		return out
	}
	for _, p := range procs {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		dir := filepath.Join(c.path("/proc"), p.Name())
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		var comm string
		for _, fd := range fds {
			in, ok := socketInode(filepath.Join(dir, "fd", fd.Name()))
			if !ok {
				continue
			}
			if comm == "" {
				b, _ := readSmall(filepath.Join(dir, "comm"), 64)
				comm = strings.TrimSpace(string(b))
			}
			if comm != "" {
				out[in] = comm
			}
		}
	}
	return out
}

func socketInode(fd string) (string, bool) {
	t, err := os.Readlink(fd)
	if err != nil {
		return "", false
	}
	in, ok := strings.CutPrefix(t, "socket:[")
	if !ok {
		return "", false
	}
	return strings.TrimSuffix(in, "]"), true
}

// wan exposure

func (c *Collector) exposure(ctx context.Context) (map[string]string, error) {
	run := c.UCI
	if run == nil {
		run = func(ctx context.Context) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, "uci", "-q", "show", "firewall").Output()
		}
	}
	out := map[string]string{}
	b, err := run(ctx)
	if err != nil && len(b) == 0 {
		return nil, fmt.Errorf("couldn't read the firewall settings: %v", err)
	}
	for k, v := range parseFirewall(string(b)) {
		out[k] = v
	}
	if c.Forwards != nil {
		for _, f := range c.Forwards() {
			out["fengard forward "+f] = "on"
		}
	}
	return out, nil
}

// parseFirewall turns uci show output into port forwards and rules that let the internet in
func parseFirewall(s string) map[string]string {
	type section struct {
		typ  string
		opts map[string]string
	}
	var order []string
	secs := map[string]*section{}
	for line := range strings.SplitSeq(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		k = strings.TrimPrefix(k, "firewall.")
		v = unquote(v)
		name, opt, isOpt := strings.Cut(k, ".")
		if !isOpt {
			secs[name] = &section{typ: v, opts: map[string]string{}}
			order = append(order, name)
			continue
		}
		if sec := secs[name]; sec != nil {
			sec.opts[opt] = v
		}
	}
	out := map[string]string{}
	for _, name := range order {
		sec := secs[name]
		o := sec.opts
		if o["enabled"] == "0" {
			continue
		}
		proto := o["proto"]
		if proto == "" {
			proto = "tcp udp"
		}
		label := o["name"]
		switch sec.typ {
		case "redirect":
			if t := o["target"]; t != "" && !strings.EqualFold(t, "DNAT") {
				continue
			}
			if src := o["src"]; src != "" && src != "wan" {
				continue
			}
			key := "forward " + proto + " " + orAny(o["src_dport"]) + " to " + orAny(o["dest_ip"]) + ":" + orAny(firstOf(o["dest_port"], o["src_dport"]))
			if o["src_ip"] != "" {
				key += " from " + o["src_ip"]
			}
			out[key] = label
		case "rule":
			if o["src"] != "wan" || !strings.EqualFold(o["target"], "ACCEPT") {
				continue
			}
			key := "allow " + proto
			if o["dest_port"] != "" {
				key += " port " + o["dest_port"]
			}
			if o["icmp_type"] != "" {
				key += " " + o["icmp_type"]
			}
			if o["family"] != "" {
				key += " " + o["family"]
			}
			if o["src_ip"] != "" {
				key += " from " + o["src_ip"]
			}
			if o["dest"] != "" {
				key += " to " + o["dest"]
				if o["dest_ip"] != "" {
					key += " " + o["dest_ip"]
				}
			}
			out[key] = label
		}
	}
	return out
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	// list options come out as 'a' 'b'
	v = strings.ReplaceAll(v, "' '", " ")
	return strings.Trim(v, "'")
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
