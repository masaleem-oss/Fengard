package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/persist"
)

// Exec runs a command with stdin, tests swap it for a fake router
type Exec func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

// StateFile and UndoFile live in the data dir, the uninstaller runs the undo file line by line
const (
	StateFile = "guest.json"
	UndoFile  = "guest.undo"
)

type Manager struct {
	Enabled bool   // only on a router install, a computer never touches anything
	Dir     string // where the state and undo files go
	BlockIP string
	Ports   []int
	Exec    Exec // nil runs the real commands
	// how long the guest radios get to come up with the guest network's name, 45 s when zero
	RadioWait time.Duration
	Addrs     func() []netip.Prefix // addresses on the router now, nil reads them
	NewSyntax func() bool           // nil reads /etc/openwrt_release
	OnChange  func()                // the guest bridge came or went, so the firewall needs a resync

	mu      sync.Mutex
	want    config.Guest
	wantSet bool
	have    *State
	loaded  bool
	failed  string // the want that last failed, tried again once it changes
	status  Status
	kickCh  chan struct{}
	kickers []string // hostapd objects for the guest wi-fi, can disconnect a guest
	// times in a row each quiet interface was found up again
	quieted map[string]int

	ifaces atomic.Pointer[[]string]
	nets   atomic.Pointer[[]netip.Prefix]
}

// Status is what the guest page shows
type Status struct {
	Router  bool     `json:"router"` // false on a computer
	State   string   `json:"state"`  // off applying on removing error
	Step    string   `json:"step,omitempty"`
	Error   string   `json:"error,omitempty"`
	Mode    string   `json:"mode,omitempty"` // existing or own
	Network string   `json:"network,omitempty"`
	Device  string   `json:"device,omitempty"`
	Subnet  string   `json:"subnet,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	WiFi    []string `json:"wifi,omitempty"`
	Notes   []string `json:"notes,omitempty"`
	Kick    bool     `json:"kick"` // the wi-fi driver can disconnect a guest
}

func (m *Manager) kick() {
	m.mu.Lock()
	if m.kickCh == nil {
		m.kickCh = make(chan struct{}, 1)
	}
	ch := m.kickCh
	m.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Want is called on every config change
func (m *Manager) Want(g config.Guest) {
	m.mu.Lock()
	same := m.wantSet && m.want == g
	m.want, m.wantSet = g, true
	m.mu.Unlock()
	if !same {
		m.kick()
	}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.Router = m.Enabled
	if s.State == "" {
		s.State = "off"
	}
	s.WiFi, s.Notes = slices.Clone(s.WiFi), slices.Clone(s.Notes)
	s.Kick = len(m.kickers) > 0
	return s
}

// Ifaces is the guest bridge while guest wi-fi is on
func (m *Manager) Ifaces() []string {
	if p := m.ifaces.Load(); p != nil {
		return *p
	}
	return nil
}

func (m *Manager) Nets() []netip.Prefix {
	if p := m.nets.Load(); p != nil {
		return *p
	}
	return nil
}

// IsGuest says if an address is on the guest network
func (m *Manager) IsGuest(ip string) bool {
	nets := m.Nets()
	if len(nets) == 0 {
		return false
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (m *Manager) run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if m.Exec != nil {
		return m.Exec(ctx, stdin, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	// uci batch carries on past a bad line and exits 0, it only says so on stderr
	if err == nil && name == "uci" && len(args) > 0 && args[0] == "batch" && strings.TrimSpace(stderr.String()) != "" {
		err = errors.New("failed")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String() + " " + out.String())
		return out.Bytes(), fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, msg)
	}
	return out.Bytes(), nil
}

// Run keeps the router matching the config, a minute apart or when kicked
func (m *Manager) Run(ctx context.Context) {
	m.kick()
	m.mu.Lock()
	ch := m.kickCh
	m.mu.Unlock()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		m.Sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ch:
		case <-t.C:
		}
	}
}

// Refresh looks again at the guest bridge, after a firewall or network reload
func (m *Manager) Refresh() { m.kick() }

func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	data, err := os.ReadFile(filepath.Join(m.Dir, StateFile))
	if err != nil {
		return
	}
	var st State
	if json.Unmarshal(data, &st) == nil && st.Network != "" {
		m.have = &st
		m.status = Status{State: "on", Mode: st.Mode, Network: st.Network, Device: st.Device, Subnet: st.Subnet, Gateway: st.Gateway, WiFi: st.WiFi, Notes: st.Notes}
	}
}

func sig(g config.Guest) string { return g.SSID + "\x00" + keySum(g.Password) }

// Sync does one round of making the router match, it's what Run calls
func (m *Manager) Sync(ctx context.Context) {
	if !m.Enabled {
		return
	}
	m.mu.Lock()
	m.load()
	want, wantSet, have, failed := m.want, m.wantSet, m.have, m.failed
	m.mu.Unlock()
	if !wantSet {
		return
	}
	on := want.Enabled && want.SSID != "" && want.Password != ""
	if have != nil && !have.Done {
		// stopped before the changes were committed and checked, put back whatever went in
		m.rollback(ctx, have)
		have = nil
	}
	if have != nil && len(have.Pending) > 0 {
		// stopped halfway through the reloads, a network reload can restart fengard
		m.reloadPending(ctx, "applying", have)
	}
	switch {
	case on && have == nil && failed != sig(want):
		m.enable(ctx, want)
	case on && have != nil && (have.SSID != want.SSID || have.KeySum != keySum(want.Password)) && failed != sig(want):
		m.rename(ctx, want)
	case !on && have != nil:
		m.disable(ctx)
	}
	if !on {
		m.mu.Lock()
		m.failed = ""
		m.mu.Unlock()
	}
	m.mu.Lock()
	still := m.have
	m.mu.Unlock()
	if on && still != nil {
		m.quiet(ctx, still)
	}
	m.refresh(ctx)
}

// quiet takes down the guest radio on a band the router repeats on if the driver brought it up
// anyway, gl.inet's mediatek driver does that with no settings, as an open network called HT_AP1
//
// it only ever touches that one interface and only when it's up. something that keeps bringing it
// back gets three goes and is then left alone, an interface flapping every minute on the uplink's
// band is worse than an open network that isn't bridged to anything
func (m *Manager) quiet(ctx context.Context, st *State) {
	for _, i := range st.Quiet {
		out, err := m.run(ctx, nil, "ip", "-o", "link", "show", i)
		if err != nil {
			continue
		}
		m.mu.Lock()
		if m.quieted == nil {
			m.quieted = map[string]int{}
		}
		if !linkUp(out) {
			delete(m.quieted, i)
			m.mu.Unlock()
			continue
		}
		m.quieted[i]++
		n := m.quieted[i]
		m.mu.Unlock()
		if n > quietTries {
			if n == quietTries+1 {
				log.Printf("guest wi-fi: %s keeps coming back up on the band the router repeats on, leaving it alone now", i)
			}
			continue
		}
		if _, err := m.run(ctx, nil, "ip", "link", "set", i, "down"); err != nil {
			log.Printf("guest wi-fi: couldn't take %s down: %v", i, err)
			continue
		}
		log.Printf("guest wi-fi: took %s down, it came up on a band the router repeats on", i)
	}
}

// how often in a row quiet takes an interface down before it gives up on it
const quietTries = 3

// linkUp reads the flags in ip -o link show, like <BROADCAST,MULTICAST,UP,LOWER_UP>
func linkUp(out []byte) bool {
	s := string(out)
	a, b := strings.Index(s, "<"), strings.Index(s, ">")
	if a < 0 || b < a {
		return false
	}
	return slices.Contains(strings.Split(s[a+1:b], ","), "UP")
}

func (m *Manager) step(state, step string) {
	m.mu.Lock()
	m.status.State, m.status.Step, m.status.Error = state, step, ""
	m.mu.Unlock()
}

func (m *Manager) fail(want config.Guest, err error) {
	log.Printf("guest wi-fi: %v", err)
	m.mu.Lock()
	m.failed = sig(want)
	m.status.Step, m.status.Error = "", err.Error()
	if m.have == nil {
		m.status.State = "error"
	}
	m.mu.Unlock()
}

func (m *Manager) read(ctx context.Context) (Router, error) {
	var r Router
	for _, pkg := range []string{"network", "wireless", "firewall", "dhcp"} {
		out, err := m.run(ctx, nil, "uci", "-q", "-X", "show", pkg)
		// no wireless or dhcp file just means none of those, the plan says so
		if err != nil && len(out) == 0 && pkg != "dhcp" && pkg != "wireless" {
			return r, fmt.Errorf("reading the router's %s settings: %w", pkg, err)
		}
		p := ParseShow(pkg, out)
		switch pkg {
		case "network":
			r.Network = p
		case "wireless":
			r.Wireless = p
		case "firewall":
			r.Firewall = p
		case "dhcp":
			r.DHCP = p
		}
	}
	return r, nil
}

func (m *Manager) saveState(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// the undo goes first, then a crash halfway still leaves a way back
	if err := persist.WriteFile(filepath.Join(m.Dir, UndoFile), []byte(Batch(st.Undo)), 0o600); err != nil {
		return err
	}
	return persist.WriteFile(filepath.Join(m.Dir, StateFile), data, 0o600)
}

func (m *Manager) clearState() {
	os.Remove(filepath.Join(m.Dir, StateFile))
	os.Remove(filepath.Join(m.Dir, UndoFile))
}

func (m *Manager) batch(ctx context.Context, ops []Op) error {
	if len(ops) == 0 {
		return nil
	}
	if _, err := m.run(ctx, []byte(Batch(ops)), "uci", "batch"); err != nil {
		// uncommitted halves of a failed batch would go in with the next commit of that package
		for _, p := range Packages(ops) {
			m.run(ctx, nil, "uci", "revert", p)
		}
		return err
	}
	return nil
}

// netifd is what a router's own guest network gets in place of network and wifi. it's netifd's own
// reload, which restarts only the interfaces and radios whose settings changed. the network init
// script and wifi reload run it too, but then go on to restart every radio of a wi-fi driver that
// still has the old style scripts, and on a repeater that drops the uplink
var reloadCmd = map[string][]string{
	"netifd":   {"ubus", "call", "network", "reload"},
	"network":  {"/etc/init.d/network", "reload"},
	"wifi":     {"wifi", "reload"},
	"dnsmasq":  {"/etc/init.d/dnsmasq", "restart"},
	"firewall": {"/etc/init.d/firewall", "reload"},
}

var reloadStep = map[string]string{
	"netifd":   "Starting the guest network, Wi-Fi on its band drops for a few seconds",
	"network":  "Starting the guest network",
	"wifi":     "Restarting Wi-Fi, it drops for a few seconds",
	"dnsmasq":  "Restarting DHCP",
	"firewall": "Reloading the firewall",
}

// reloads is the list to run for this state. one saved by a build that reloaded the whole network
// and every radio for the router's own guest network gets the netifd reload in their place
func reloads(st *State, what []string) []string {
	if st.Mode != "existing" {
		return what
	}
	out := []string{}
	for _, w := range what {
		if w == "network" || w == "wifi" {
			w = "netifd"
		}
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

func (m *Manager) reload(ctx context.Context, state string, what []string) []string {
	var notes []string
	for _, w := range what {
		if err := m.reloadOne(ctx, state, w); err != nil {
			notes = append(notes, "Couldn't reload "+w+": "+err.Error())
		}
	}
	return notes
}

func (m *Manager) reloadOne(ctx context.Context, state, what string) error {
	c, step := reloadCmd[what], reloadStep[what]
	if c == nil {
		return nil
	}
	m.step(state, step)
	_, err := m.run(ctx, nil, c[0], c[1:]...)
	if err != nil {
		log.Printf("guest wi-fi: %v", err)
	}
	return err
}

// runs the reloads still pending, saving after each so a restart picks up from the next one
func (m *Manager) reloadPending(ctx context.Context, state string, st *State) []string {
	var notes []string
	st.Pending = reloads(st, st.Pending)
	for len(st.Pending) > 0 {
		w := st.Pending[0]
		st.Pending = st.Pending[1:]
		if err := m.saveState(st); err != nil {
			log.Printf("guest wi-fi: %v", err)
		}
		if err := m.reloadOne(ctx, state, w); err != nil {
			notes = append(notes, "Couldn't reload "+w+": "+err.Error())
		}
	}
	m.mu.Lock()
	if m.status.State == state {
		m.status.State, m.status.Step = "on", ""
	}
	m.mu.Unlock()
	return notes
}

// undoes a plan that may have gone in partway, nothing was reloaded yet so the router never ran it
func (m *Manager) rollback(ctx context.Context, st *State) {
	log.Printf("guest wi-fi: undoing changes that didn't finish")
	if r, err := m.read(ctx); err == nil {
		if err := m.batch(ctx, r.Applicable(st.Undo)); err != nil {
			log.Printf("guest wi-fi: %v", err)
		}
	}
	m.clearState()
	m.mu.Lock()
	m.have = nil
	m.status = Status{State: "off"}
	m.mu.Unlock()
}

func (m *Manager) want4(g config.Guest) Want {
	w := Want{SSID: g.SSID, Password: g.Password, BlockIP: m.BlockIP, Ports: m.Ports, NewSyntax: true}
	if m.Addrs != nil {
		w.Used = m.Addrs()
	} else {
		w.Used = localNets()
	}
	if m.NewSyntax != nil {
		w.NewSyntax = m.NewSyntax()
	} else {
		w.NewSyntax = newSyntax()
	}
	return w
}

func (m *Manager) enable(ctx context.Context, g config.Guest) {
	m.step("applying", "Reading the router's settings")
	r, err := m.read(ctx)
	if err != nil {
		m.fail(g, err)
		return
	}
	w := m.want4(g)
	// a radio that also repeats another network can't carry guest wi-fi, gl.inet leaves it off too
	if out, err := m.run(ctx, nil, "iwinfo"); err == nil {
		w.Busy = BusyRadios(out, r.Wireless)
	}
	plan, err := MakePlan(r, w)
	if err != nil {
		m.fail(g, err)
		return
	}
	st := plan.State
	if err := m.saveState(&st); err != nil {
		m.fail(g, fmt.Errorf("saving what changes: %w", err))
		return
	}
	m.step("applying", "Saving the guest Wi-Fi settings")
	err = m.batch(ctx, plan.Do)
	if err == nil {
		// read it back, a half applied batch must not count as on
		var r2 Router
		if r2, err = m.read(ctx); err == nil {
			if miss := r2.Missing(plan.Do); len(miss) > 0 {
				err = fmt.Errorf("%d settings didn't stick, the first is %s", len(miss), miss[0])
			}
		}
	}
	if err != nil {
		// the commits may have gone partway, put back whatever did
		if r2, rerr := m.read(ctx); rerr == nil {
			m.batch(ctx, r2.Applicable(st.Undo))
		}
		m.clearState()
		m.fail(g, fmt.Errorf("the router didn't take the guest Wi-Fi settings: %w", err))
		return
	}
	st.Done, st.Pending = true, slices.Clone(st.Reload)
	if err := m.saveState(&st); err != nil {
		log.Printf("guest wi-fi: %v", err)
	}
	m.mu.Lock()
	m.have = &st
	m.mu.Unlock()
	notes := m.reloadPending(ctx, "applying", &st)
	m.quiet(ctx, &st)
	if err := m.radiosOK(ctx, &st); err != nil {
		m.unsafe(ctx, g, err)
		return
	}
	log.Printf("guest wi-fi: on, %s network %s %s", st.Mode, st.Network, st.Subnet)
	m.mu.Lock()
	m.status = Status{State: "on", Mode: st.Mode, Network: st.Network, Device: st.Device, Subnet: st.Subnet, Gateway: st.Gateway,
		WiFi: st.WiFi, Notes: append(slices.Clone(st.Notes), notes...)}
	m.mu.Unlock()
}

func (m *Manager) rename(ctx context.Context, g config.Guest) {
	m.mu.Lock()
	st := *m.have
	m.mu.Unlock()
	m.step("applying", "Saving the new name and password")
	if err := m.batch(ctx, Rename(st, m.want4(g))); err != nil {
		m.fail(g, err)
		return
	}
	st.SSID, st.KeySum = g.SSID, keySum(g.Password)
	if err := m.saveState(&st); err != nil {
		log.Printf("guest wi-fi: %v", err)
	}
	m.mu.Lock()
	m.have = &st
	m.mu.Unlock()
	notes := m.reload(ctx, "applying", reloads(&st, []string{"wifi"}))
	if err := m.radiosOK(ctx, &st); err != nil {
		m.unsafe(ctx, g, err)
		return
	}
	m.mu.Lock()
	m.status.State, m.status.Step, m.status.Notes = "on", "", append(slices.Clone(st.Notes), notes...)
	m.mu.Unlock()
}

func (m *Manager) disable(ctx context.Context) {
	m.mu.Lock()
	st := *m.have
	m.mu.Unlock()
	m.step("removing", "Putting the router's settings back")
	r, err := m.read(ctx)
	if err == nil {
		err = m.batch(ctx, r.Applicable(st.Undo))
	}
	if err != nil {
		log.Printf("guest wi-fi: %v", err)
		m.mu.Lock()
		m.status.State, m.status.Step, m.status.Error = "on", "", "Couldn't put the router's settings back: "+err.Error()
		m.mu.Unlock()
		return
	}
	// wi-fi first so nothing is left pointing at a network that's gone
	order := []string{}
	if slices.Contains(st.Reload, "wifi") {
		order = append(order, "wifi")
	}
	for _, w := range st.Reload {
		if w != "wifi" {
			order = append(order, w)
		}
	}
	m.reload(ctx, "removing", reloads(&st, order))
	m.clearState()
	log.Printf("guest wi-fi: off, router settings put back")
	m.mu.Lock()
	m.have = nil
	m.kickers = nil
	m.quieted = nil
	m.status = Status{State: "off"}
	m.mu.Unlock()
}

// the guest bridge from netifd, and which hostapd objects could disconnect a guest
func (m *Manager) refresh(ctx context.Context) {
	m.mu.Lock()
	have := m.have
	m.mu.Unlock()
	var ifaces []string
	var nets []netip.Prefix
	var kickers []string
	if have != nil {
		dev := have.Device
		if out, err := m.run(ctx, nil, "ubus", "call", "network.interface."+have.Network, "status"); err == nil {
			var v struct {
				Dev string `json:"l3_device"`
			}
			if json.Unmarshal(out, &v) == nil && v.Dev != "" {
				dev = v.Dev
			}
		}
		ifaces = []string{dev}
		if p, err := netip.ParsePrefix(have.Subnet); err == nil {
			nets = []netip.Prefix{p}
		}
		kickers = m.hostapds(ctx, have.WiFi)
	}
	changed := !slices.Equal(ifaces, m.Ifaces()) || !slices.Equal(nets, m.Nets())
	m.ifaces.Store(&ifaces)
	m.nets.Store(&nets)
	m.mu.Lock()
	m.kickers = kickers
	if have != nil {
		m.status.Device = ifaces[0]
	}
	m.mu.Unlock()
	if changed && m.OnChange != nil {
		m.OnChange()
	}
}

// hostapd ubus objects for these wifi-iface sections, gl.inet's mediatek driver has none
func (m *Manager) hostapds(ctx context.Context, sections []string) []string {
	out, err := m.run(ctx, nil, "ubus", "call", "network.wireless", "status")
	if err != nil {
		return nil
	}
	ifnames := WiFiIfnames(out, sections)
	if len(ifnames) == 0 {
		return nil
	}
	list, err := m.run(ctx, nil, "ubus", "list", "hostapd.*")
	if err != nil {
		return nil
	}
	var objs []string
	for _, l := range strings.Fields(string(list)) {
		if name, ok := strings.CutPrefix(l, "hostapd."); ok && slices.Contains(ifnames, name) {
			objs = append(objs, l)
		}
	}
	return objs
}

// WiFiIfnames picks the kernel names of these sections out of ubus call network.wireless status
func WiFiIfnames(status []byte, sections []string) []string {
	var radios map[string]struct {
		Interfaces []struct {
			Section string `json:"section"`
			Ifname  string `json:"ifname"`
		} `json:"interfaces"`
	}
	if json.Unmarshal(status, &radios) != nil {
		return nil
	}
	var out []string
	for _, r := range radios {
		for _, i := range r.Interfaces {
			if i.Ifname != "" && slices.Contains(sections, i.Section) {
				out = append(out, i.Ifname)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Disconnect throws a guest off the guest wi-fi when the driver can. the firewall already blocks it
// either way, this just stops it sitting there connected
func (m *Manager) Disconnect(mac string) bool {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return false
	}
	m.mu.Lock()
	objs := slices.Clone(m.kickers)
	m.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"addr": hw.String(), "reason": 5, "deauth": true, "ban_time": 60000})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ok := false
	for _, o := range objs {
		if _, err := m.run(ctx, nil, "ubus", "call", o, "del_client", string(body)); err == nil {
			ok = true
		}
	}
	return ok
}

// every address on the router now, so a new guest subnet clashes with nothing even upstream
func localNets() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() {
			out = append(out, p.Masked())
		}
	}
	return out
}

// openwrt 21.02 moved bridges to device sections, older ones use type bridge on the interface
func newSyntax() bool {
	data, err := os.ReadFile("/etc/openwrt_release")
	if err != nil {
		return true
	}
	for _, l := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(l, "DISTRIB_RELEASE="); ok {
			v = strings.Trim(v, `'"`)
			major, _, _ := strings.Cut(v, ".")
			var n int
			if _, err := fmt.Sscan(major, &n); err == nil {
				return n >= 21
			}
		}
	}
	return true
}

// ErrNotRouter is what turning guest wi-fi on says on a computer install
var ErrNotRouter = errors.New("guest Wi-Fi needs Fengard installed on the router")

var essidRe = regexp.MustCompile(`ESSID: "(.*)"`)

// radiosOK checks the guest radios really broadcast the guest network. gl.inet's mediatek driver
// has brought them up open and unnamed (HT_AP1) when it didn't take the settings, so a radio that
// shows any other name means guest wi-fi isn't safe to leave on. radios iwinfo can't read are skipped
func (m *Manager) radiosOK(ctx context.Context, st *State) error {
	r, err := m.read(ctx)
	if err != nil || r.Wireless == nil {
		return nil
	}
	var ifs []string
	for _, n := range st.WiFi {
		if s := r.Wireless.Section(n); s != nil && s.Get("ifname") != "" {
			ifs = append(ifs, s.Get("ifname"))
		}
	}
	if len(ifs) == 0 {
		return nil
	}
	m.step("applying", "Checking the guest Wi-Fi came up with its name and password")
	wait := m.RadioWait
	if wait == 0 {
		wait = 45 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		bad := ""
		for _, i := range ifs {
			out, err := m.run(ctx, nil, "iwinfo", i, "info")
			match := essidRe.FindSubmatch(out)
			if err != nil || match == nil {
				continue
			}
			if got := string(match[1]); got != st.SSID {
				bad = fmt.Sprintf("%s came up as %q instead of %q", i, got, st.SSID)
			}
		}
		if bad == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New(bad)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(2*time.Second, wait/4)):
		}
	}
}

// unsafe turns guest wi-fi straight back off when the radios came up wrong, an open network
// with the driver's default name is worse than no guest wi-fi
// with the same settings it isn't tried again until the owner changes them, each try restarts the wi-fi
func (m *Manager) unsafe(ctx context.Context, want config.Guest, err error) {
	log.Printf("guest wi-fi: %v, turning it off again", err)
	m.disable(ctx)
	m.mu.Lock()
	m.failed = sig(want)
	m.status.State, m.status.Error = "error", "The router's Wi-Fi didn't take the guest network's name and password ("+err.Error()+
		"), so Fengard turned guest Wi-Fi off again rather than leave an open network up."
	m.mu.Unlock()
}
