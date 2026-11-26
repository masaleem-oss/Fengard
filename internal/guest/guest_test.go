package guest

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
)

func TestParseShow(t *testing.T) {
	p := ParseShow("wireless", []byte(`wireless.radio0=wifi-device
wireless.radio0.channel='36'
wireless.home=wifi-iface
wireless.home.ssid='Sam'\''s "Wi-Fi"'
wireless.home.maclist='aa:bb' 'cc:dd'
wireless.home.note='two words'
network.other.x='not ours'
garbage
`))
	if len(p.Sections) != 2 || p.Section("radio0").Type != "wifi-device" {
		t.Fatalf("sections %+v", p.Sections)
	}
	h := p.Section("home")
	if got := h.Get("ssid"); got != `Sam's "Wi-Fi"` {
		t.Errorf("ssid %q", got)
	}
	if got := h.Opts["maclist"]; !slices.Equal(got, []string{"aa:bb", "cc:dd"}) {
		t.Errorf("list %q", got)
	}
	if got := h.Opts["note"]; !slices.Equal(got, []string{"two words"}) {
		t.Errorf("spaces %q", got)
	}
	// and back again for uci batch
	if got := set("wireless.home.ssid", `Sam's "Wi-Fi"`).String(); got != `set wireless.home.ssid='Sam'\''s "Wi-Fi"'` {
		t.Errorf("quoted %s", got)
	}
}

func TestQR(t *testing.T) {
	cases := map[[2]string]string{
		{"Guests", "k7pq-m3zt-x9wd-4fhn"}: `WIFI:T:WPA;S:Guests;P:k7pq-m3zt-x9wd-4fhn;;`,
		{`Sam;s "Net"`, `a\b:c,d`}:        `WIFI:T:WPA;S:Sam\;s \"Net\";P:a\\b\:c\,d;;`,
		{"Café ☕", "x;;y"}:                `WIFI:T:WPA;S:Café ☕;P:x\;\;y;;`,
	}
	for in, want := range cases {
		if got := QR(in[0], in[1]); got != want {
			t.Errorf("QR(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	a, b := NewPassword(), NewPassword()
	if a == b || len(a) != 19 || strings.Count(a, "-") != 3 || strings.ContainsAny(a, "01ilo") {
		t.Errorf("passwords %q %q", a, b)
	}
}

func TestPickSubnet(t *testing.T) {
	pfx := func(s ...string) (out []netip.Prefix) {
		for _, x := range s {
			out = append(out, netip.MustParsePrefix(x))
		}
		return
	}
	cases := []struct {
		used []string
		want string
	}{
		{nil, "192.168.77.0/24"},
		{[]string{"192.168.77.0/24", "192.168.78.0/24"}, "192.168.79.0/24"},
		// a /16 lan covers every 192.168 network
		{[]string{"192.168.0.0/16"}, ""},
		{[]string{"192.168.64.0/18"}, "192.168.128.0/24"},
		{[]string{"10.0.0.0/8", "192.168.77.5/32"}, "192.168.78.0/24"},
	}
	for _, c := range cases {
		got, ok := PickSubnet(pfx(c.used...))
		if c.want == "" {
			if ok {
				t.Errorf("%v: got %v, want none", c.used, got)
			}
			continue
		}
		if !ok || got.String() != c.want {
			t.Errorf("%v: got %v, want %s", c.used, got, c.want)
		}
	}
}

func routerOf(show map[string]string) Router {
	return Router{
		Network: ParseShow("network", []byte(show["network"])), Wireless: ParseShow("wireless", []byte(show["wireless"])),
		Firewall: ParseShow("firewall", []byte(show["firewall"])), DHCP: ParseShow("dhcp", []byte(show["dhcp"])),
	}
}

var want = Want{SSID: "Sam's Guests", Password: "k7pq-m3zt-x9wd-4fhn", BlockIP: "192.168.8.2", Ports: []int{8880, 8843}, NewSyntax: true}

func lines(ops []Op) []string {
	var out []string
	for _, o := range ops {
		out = append(out, o.String())
	}
	return out
}

func TestPlanExisting(t *testing.T) {
	p, err := MakePlan(routerOf(glinet), want)
	if err != nil {
		t.Fatal(err)
	}
	st := p.State
	if st.Mode != "existing" || st.Network != "guest" || st.Zone != "guest" || st.Device != "br-guest" ||
		st.Subnet != "192.168.9.0/24" || st.Gateway != "192.168.9.1" || !slices.Equal(st.WiFi, []string{"guest2g", "guest5g"}) {
		t.Fatalf("state %+v", st)
	}
	do := strings.Join(lines(p.Do), "\n")
	for _, w := range []string{
		`set wireless.guest2g.ssid='Sam'\''s Guests'`,
		`set wireless.guest2g.encryption='psk2'`,
		`set wireless.guest2g.key='k7pq-m3zt-x9wd-4fhn'`,
		`set wireless.guest2g.disabled='0'`,
		`set wireless.guest5g.hidden='0'`,
		`delete network.guest.ip6assign`,
		`add_list dhcp.guest.dhcp_option='6,192.168.9.1'`,
		`set firewall.fengard_guest.src='guest'`,
		`set firewall.fengard_guest.dest_port='8880 8843'`,
		`set firewall.fengard_guest.dest_ip='192.168.8.2'`,
	} {
		if !strings.Contains(do, w) {
			t.Errorf("missing %s", w)
		}
	}
	// guest5g already does psk2 and guest2g already isolates
	for _, no := range []string{"guest5g.encryption", "guest2g.isolate", "default_radio0", "wireless.mt7981", "network.lan", "network.wan", ".lan."} {
		if strings.Contains(do, no) {
			t.Errorf("touches %s:\n%s", no, do)
		}
	}
	undo := lines(st.Undo)
	for _, w := range []string{
		`set wireless.guest2g.ssid='GL-MT3000-a1b-Guest'`,
		`set wireless.guest2g.encryption='none'`,
		`delete wireless.guest2g.key`,
		`set wireless.guest2g.disabled='1'`,
		`set wireless.guest5g.key='oldguestpw'`,
		`set wireless.guest5g.hidden='1'`,
		`delete dhcp.guest.dhcp_option`,
		`delete firewall.fengard_guest`,
		`set network.guest.ip6assign='60'`,
	} {
		if !slices.Contains(undo, w) {
			t.Errorf("undo missing %s", w)
		}
	}
	if !slices.Equal(st.Reload, []string{"network", "dnsmasq", "wifi", "firewall"}) {
		t.Errorf("reload %v", st.Reload)
	}

	// a guest zone that forwards to the lan isn't a guest network, fengard makes its own
	leaky := routerOf(glinet)
	leaky.Firewall.Sections = append(leaky.Firewall.Sections, &Section{Name: "x", Type: "forwarding", Opts: map[string][]string{"src": {"guest"}, "dest": {"lan"}}})
	if p, err := MakePlan(leaky, want); err != nil || p.State.Mode != "own" {
		t.Errorf("leaky guest zone used: %v %+v", err, p.State)
	}
}

func TestPlanOwn(t *testing.T) {
	p, err := MakePlan(routerOf(plain), want)
	if err != nil {
		t.Fatal(err)
	}
	st := p.State
	// 192.168.77 and 78 are taken by the lan and iot
	if st.Mode != "own" || st.Network != "fgguest" || st.Device != "br-fgguest" || st.Subnet != "192.168.79.0/24" ||
		!slices.Equal(st.WiFi, []string{"fgguest_radio0", "fgguest_radio1"}) || len(st.Notes) != 1 {
		t.Fatalf("state %+v", st)
	}
	do := strings.Join(lines(p.Do), "\n")
	for _, w := range []string{
		`set network.fgguest_dev='device'`,
		`set network.fgguest_dev.type='bridge'`,
		`set network.fgguest.device='br-fgguest'`,
		`set network.fgguest.ipaddr='192.168.79.1'`,
		`add_list dhcp.fgguest.dhcp_option='6,192.168.79.1'`,
		`set firewall.fgguest.input='REJECT'`,
		`set firewall.fgguest.forward='REJECT'`,
		`set firewall.fgguest_wan.dest='wan'`,
		`set firewall.fgguest_dns.dest_port='53'`,
		`set firewall.fgguest_dhcp.family='ipv4'`,
		`set firewall.fengard_guest.dest_port='8880 8843'`,
		`set wireless.fgguest_radio0.network='fgguest'`,
		`set wireless.fgguest_radio1.isolate='1'`,
		`set wireless.fgguest_radio1.encryption='psk2'`,
	} {
		if !strings.Contains(do, w) {
			t.Errorf("missing %s", w)
		}
	}
	for _, o := range p.Do {
		parts := strings.SplitN(o.Key, ".", 3)
		if !strings.HasPrefix(parts[1], "fgguest") && parts[1] != webRule {
			t.Errorf("changes something that isn't fengard's: %s", o)
		}
	}
	for _, o := range st.Undo {
		if o.Cmd != "delete" || strings.Count(o.Key, ".") != 1 {
			t.Errorf("undo should only delete fengard's own sections: %s", o)
		}
	}
	// openwrt 19.07 bridges with type bridge on the interface
	old := want
	old.NewSyntax = false
	p, _ = MakePlan(routerOf(plain), old)
	if do := strings.Join(lines(p.Do), "\n"); strings.Contains(do, "fgguest_dev") || !strings.Contains(do, `set network.fgguest.type='bridge'`) {
		t.Errorf("19.07 bridge:\n%s", do)
	}
	// nothing to put guests on
	none := routerOf(plain)
	none.Wireless = ParseShow("wireless", nil)
	if _, err := MakePlan(none, want); err == nil {
		t.Error("planned guest wi-fi without radios")
	}
}

func manager(t *testing.T, f *fakeRouter) *Manager {
	return &Manager{
		Enabled: true, Dir: t.TempDir(), BlockIP: "192.168.8.2", Ports: []int{8880, 8843}, Exec: f.exec,
		Addrs: func() []netip.Prefix { return nil }, NewSyntax: func() bool { return true },
	}
}

// turning it on and off again leaves every package exactly as it was, for both kinds of router
func TestEnableDisableRestores(t *testing.T) {
	for name, show := range map[string]map[string]string{"glinet": glinet, "plain": plain} {
		f := newFake(show)
		f.ubus["network.interface.guest"] = `{"up": true, "l3_device": "br-guest"}`
		f.ubus["network.interface.fgguest"] = `{"up": true, "l3_device": "br-fgguest"}`
		before := f.snapshot()
		m := manager(t, f)
		changed := 0
		m.OnChange = func() { changed++ }
		ctx := context.Background()
		g := config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"}
		m.Want(g)
		m.Sync(ctx)
		st := m.Status()
		if st.State != "on" || st.Error != "" {
			t.Fatalf("%s: status %+v", name, st)
		}
		if after := f.snapshot(); after["wireless"] == before["wireless"] || after["firewall"] == before["firewall"] {
			t.Errorf("%s: nothing changed", name)
		}
		if !strings.Contains(f.snapshot()["wireless"], "ssid='Guests'") {
			t.Errorf("%s: ssid not set", name)
		}
		if len(m.Ifaces()) != 1 || !m.IsGuest(strings.TrimSuffix(st.Gateway, "1")+"23") || m.IsGuest("192.168.8.20") || changed != 1 {
			t.Errorf("%s: ifaces %v nets %v changed %d", name, m.Ifaces(), m.Nets(), changed)
		}
		undo, err := os.ReadFile(filepath.Join(m.Dir, UndoFile))
		if err != nil || !strings.Contains(string(undo), "commit wireless") {
			t.Errorf("%s: undo file %q %v", name, undo, err)
		}

		// a new password goes to the same wi-fi, the undo still has the original
		g.Password = "aaaa-bbbb-cccc-dddd"
		m.Want(g)
		m.Sync(ctx)
		if !strings.Contains(f.snapshot()["wireless"], "key='aaaa-bbbb-cccc-dddd'") {
			t.Errorf("%s: password not changed", name)
		}

		// a fengard restart picks up where it was from the state file
		m2 := manager(t, f)
		m2.Dir = m.Dir
		m2.Want(g)
		m2.Sync(ctx)
		if m2.Status().State != "on" || len(f.calls("uci batch")) != 2 {
			t.Errorf("%s: restart re-applied: %d batches, %+v", name, len(f.calls("uci batch")), m2.Status())
		}

		g.Enabled = false
		m2.Want(g)
		m2.Sync(ctx)
		after := f.snapshot()
		for pkg := range before {
			if after[pkg] != before[pkg] {
				t.Errorf("%s: %s not restored\nbefore:\n%s\nafter:\n%s", name, pkg, before[pkg], after[pkg])
			}
		}
		if m2.Status().State != "off" || len(m2.Ifaces()) != 0 {
			t.Errorf("%s: after off %+v %v", name, m2.Status(), m2.Ifaces())
		}
		if _, err := os.Stat(filepath.Join(m.Dir, UndoFile)); err == nil {
			t.Errorf("%s: undo file left behind", name)
		}
		if w := f.calls("wifi reload"); len(w) != 3 {
			t.Errorf("%s: wifi reloads %v", name, w)
		}
	}
}

// something deleted by hand meanwhile doesn't stop the rest going back
func TestDisableAfterHandEdit(t *testing.T) {
	f := newFake(plain)
	before := f.snapshot()
	m := manager(t, f)
	ctx := context.Background()
	m.Want(config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"})
	m.Sync(ctx)
	f.exec(ctx, []byte("delete wireless.fgguest_radio1\ndelete firewall.fengard_guest\ncommit wireless\ncommit firewall\n"), "uci", "batch")
	m.Want(config.Guest{SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"})
	m.Sync(ctx)
	if after := f.snapshot(); after["network"] != before["network"] || after["wireless"] != before["wireless"] || after["firewall"] != before["firewall"] {
		t.Errorf("not restored:\n%s", after["wireless"])
	}
}

// a failed apply leaves nothing behind and isn't tried again until something changes
func TestEnableFails(t *testing.T) {
	f := newFake(glinet)
	f.failOn = "commit wireless"
	before := f.snapshot()
	m := manager(t, f)
	ctx := context.Background()
	m.Want(config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"})
	m.Sync(ctx)
	if st := m.Status(); st.State != "error" || st.Error == "" {
		t.Fatalf("status %+v", st)
	}
	after := f.snapshot()
	for pkg := range before {
		if after[pkg] != before[pkg] {
			t.Errorf("%s changed after a failed apply:\n%s", pkg, after[pkg])
		}
	}
	n := len(f.calls("uci batch"))
	m.Sync(ctx)
	if len(f.calls("uci batch")) != n {
		t.Error("retried the same failing settings")
	}
	if _, err := os.Stat(filepath.Join(m.Dir, StateFile)); err == nil {
		t.Error("state file left after a failed apply")
	}
}

// a computer install never runs anything
func TestNotRouter(t *testing.T) {
	f := newFake(glinet)
	m := manager(t, f)
	m.Enabled = false
	m.Want(config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"})
	m.Sync(context.Background())
	if len(f.ran) != 0 || m.Status().Router {
		t.Errorf("ran %v", f.ran)
	}
}

func TestDisconnect(t *testing.T) {
	status := []byte(`{"radio0":{"up":true,"interfaces":[{"section":"default_radio0","ifname":"wlan0"},{"section":"fgguest_radio0","ifname":"wlan0-1"}]},
		"radio1":{"up":false,"interfaces":[{"section":"fgguest_radio1"}]}}`)
	if got := WiFiIfnames(status, []string{"fgguest_radio0", "fgguest_radio1"}); !slices.Equal(got, []string{"wlan0-1"}) {
		t.Errorf("ifnames %v", got)
	}
	f := newFake(plain)
	f.ubus["network.wireless"] = string(status)
	f.hostapds = []string{"hostapd.wlan0", "hostapd.wlan0-1"}
	m := manager(t, f)
	m.Want(config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"})
	m.Sync(context.Background())
	if !m.Status().Kick || !m.Disconnect("AA:BB:CC:DD:EE:FF") {
		t.Fatal("no disconnect")
	}
	// only the guest wi-fi, never the household one
	if got := f.calls("ubus call hostapd."); len(got) != 1 || !strings.HasPrefix(got[0], "ubus call hostapd.wlan0-1 del_client") || !strings.Contains(got[0], `"aa:bb:cc:dd:ee:ff"`) {
		t.Errorf("calls %v", got)
	}
	// mediatek on gl.inet has no hostapd objects, the firewall block is all there is
	f.hostapds = nil
	m.Refresh()
	m.Sync(context.Background())
	if m.Status().Kick || m.Disconnect("aa:bb:cc:dd:ee:ff") {
		t.Error("disconnect without hostapd")
	}
}

// a batch that quietly drops a line doesn't count as on, and what did go in comes back out
func TestEnableVerifies(t *testing.T) {
	f := newFake(glinet)
	f.skip = "set wireless.guest5g.key="
	before := f.snapshot()
	m := manager(t, f)
	m.Want(config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"})
	m.Sync(context.Background())
	if st := m.Status(); st.State != "error" || !strings.Contains(st.Error, "guest5g.key") {
		t.Fatalf("status %+v", st)
	}
	after := f.snapshot()
	for pkg := range before {
		if after[pkg] != before[pkg] {
			t.Errorf("%s changed:\n%s", pkg, after[pkg])
		}
	}
	if len(f.calls("wifi reload")) != 0 {
		t.Error("reloaded wi-fi for settings that didn't stick")
	}
}

// fengard stopping halfway, before the commit was checked or between reloads
func TestRestartMidway(t *testing.T) {
	ctx := context.Background()
	g := config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"}

	// committed but never checked, so it's undone and done again from the start
	f := newFake(plain)
	before := f.snapshot()
	m := manager(t, f)
	m.Want(g)
	m.Sync(ctx)
	var st State
	data, _ := os.ReadFile(filepath.Join(m.Dir, StateFile))
	json.Unmarshal(data, &st)
	st.Done, st.Pending = false, nil
	data, _ = json.Marshal(st)
	os.WriteFile(filepath.Join(m.Dir, StateFile), data, 0o600)
	m2 := manager(t, f)
	m2.Dir = m.Dir
	m2.Want(config.Guest{SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"})
	m2.Sync(ctx)
	if after := f.snapshot(); after["wireless"] != before["wireless"] || after["network"] != before["network"] || m2.Status().State != "off" {
		t.Errorf("unfinished enable not undone: %+v\n%s", m2.Status(), after["wireless"])
	}

	// done but the firewall reload never ran
	f = newFake(plain)
	m = manager(t, f)
	m.Want(g)
	m.Sync(ctx)
	data, _ = os.ReadFile(filepath.Join(m.Dir, StateFile))
	json.Unmarshal(data, &st)
	st.Pending = []string{"firewall"}
	data, _ = json.Marshal(st)
	os.WriteFile(filepath.Join(m.Dir, StateFile), data, 0o600)
	n := len(f.calls("/etc/init.d/firewall reload"))
	m2 = manager(t, f)
	m2.Dir = m.Dir
	m2.Want(g)
	m2.Sync(ctx)
	if len(f.calls("/etc/init.d/firewall reload")) != n+1 || len(f.calls("wifi reload")) != 1 || m2.Status().State != "on" {
		t.Errorf("pending reload: %v %+v", f.ran, m2.Status())
	}
	data, _ = os.ReadFile(filepath.Join(m.Dir, StateFile))
	if strings.Contains(string(data), "pending") {
		t.Errorf("pending left in %s", data)
	}
}

// on a gl-mt3000 the mediatek driver brought the guest radios up open as HT_AP1 instead of the guest
// network, fengard has to notice and turn guest wi-fi straight back off, and not retry every minute
func TestOpenRadiosTurnGuestOff(t *testing.T) {
	f := newFake(glinet)
	f.ubus["network.interface.guest"] = `{"up": true, "l3_device": "br-guest"}`
	f.essid = map[string]string{"ra1": "HT_AP1", "rax1": "Guests"}
	before := f.snapshot()
	m := manager(t, f)
	m.RadioWait = 50 * time.Millisecond
	ctx := context.Background()
	g := config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"}
	m.Want(g)
	m.Sync(ctx)
	st := m.Status()
	if st.State != "error" || !strings.Contains(st.Error, `"HT_AP1"`) {
		t.Fatalf("status %+v", st)
	}
	after := f.snapshot()
	for pkg := range before {
		if after[pkg] != before[pkg] {
			t.Errorf("%s not put back after the radios came up wrong", pkg)
		}
	}
	batches := len(f.calls("uci batch"))
	m.Sync(ctx)
	if n := len(f.calls("uci batch")); n != batches {
		t.Errorf("tried again with the same settings, %d batches", n-batches)
	}

	// radios that come up right leave it on
	f.essid["ra1"] = "Guests"
	g.SSID = "Guests"
	g.Password = "a-different-password"
	m.Want(g)
	m.Sync(ctx)
	if st := m.Status(); st.State != "on" || st.Error != "" {
		t.Errorf("good radios: %+v", st)
	}
}

// iwinfo from a gl-mt3000 repeating a 2.4 GHz network on apcli0
const mt3000Repeating = `apcli0    ESSID: "NoEscape"
          Mode: Client  Channel: 6 (2.437 GHz) HT Mode: HE20
          Encryption: unknown
apclix0   ESSID: unknown
          Mode: Client  Channel: 44 (5.220 GHz) HT Mode: HE20
ra0       ESSID: "Fengard"
          Mode: Master  Channel: 6 (2.437 GHz) HT Mode: HE20
ra1       ESSID: "HT_AP1"
          Mode: Master  Channel: 6 (2.437 GHz) HT Mode: HE20
rax0      ESSID: "Fengard"
          Mode: Master  Channel: 44 (5.220 GHz) HT Mode: HE20
rax1      ESSID: "HT_AP3"
          Mode: Master  Channel: 44 (5.220 GHz) HT Mode: HE20
`

func TestBusyRadios(t *testing.T) {
	w := ParseShow("wireless", []byte(glinet["wireless"]))
	if got := BusyRadios([]byte(mt3000Repeating), w); !slices.Equal(got, []string{"mt798111"}) {
		t.Errorf("busy %v, want the 2.4 GHz radio only", got)
	}
	// apclix0 isn't joined to anything, so 5 GHz is free
	if got := BusyRadios([]byte(strings.ReplaceAll(mt3000Repeating, `"NoEscape"`, "unknown")), w); len(got) != 0 {
		t.Errorf("busy %v with no uplink", got)
	}
}

// like gl.inet's own guest wi-fi, a repeating band gets no guest network and its radio isn't touched,
// so the uplink and the internet stay up
func TestRepeatingBandLeftAlone(t *testing.T) {
	f := newFake(glinet)
	f.ubus["network.interface.guest"] = `{"up": true, "l3_device": "br-guest"}`
	f.iwinfo = mt3000Repeating
	f.essid = map[string]string{"rax1": "Guests"}
	// the driver brings the 2.4 GHz guest radio up open as HT_AP1 regardless
	f.up = map[string]bool{"ra1": true}
	m := manager(t, f)
	m.RadioWait = 50 * time.Millisecond
	g := config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn"}
	m.Want(g)
	m.Sync(context.Background())
	st := m.Status()
	if st.State != "on" || !slices.Equal(st.WiFi, []string{"guest5g"}) {
		t.Fatalf("status %+v", st)
	}
	if !slices.ContainsFunc(st.Notes, func(n string) bool { return strings.Contains(n, "2.4 GHz") }) {
		t.Errorf("no note about 2.4 GHz: %v", st.Notes)
	}
	w := f.snapshot()["wireless"]
	if strings.Contains(w, "guest2g.ssid='Guests'") || !strings.Contains(w, "guest2g.disabled='1'") {
		t.Errorf("the repeating band's guest radio was changed:\n%s", w)
	}
	if f.up["ra1"] {
		t.Error("ra1 was left up, an open network on the repeating band")
	}
	// it comes back on its own later, the next sync takes it down again
	f.up["ra1"] = true
	m.Sync(context.Background())
	if f.up["ra1"] {
		t.Error("ra1 coming back up wasn't noticed")
	}
}
