package guest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// names of what fengard adds, so they never clash with the router's own
const (
	ownNet  = "fgguest"
	ownDev  = "br-fgguest"
	webRule = "fengard_guest"
)

// Want is what the guest wi-fi should look like
type Want struct {
	SSID, Password string
	BlockIP        string         // fengard's own address, guests get the approval page there
	Ports          []int          // the block page ports after the redirect from 80 and 443
	Used           []netip.Prefix // addresses on the router right now so a new subnet doesn't clash
	NewSyntax      bool           // openwrt 21 and newer, bridges are their own device sections
	// radios that also carry a client link, like a repeater uplink. mediatek drivers can't run a
	// guest network beside it, even gl.inet's own one comes up open as HT_AP1, so those stay off
	Busy []string
}

// State is what fengard set up, saved so turning it off puts back exactly what was there
type State struct {
	Mode    string   `json:"mode"` // existing or own
	Network string   `json:"network"`
	Zone    string   `json:"zone"`
	Device  string   `json:"device"`
	Subnet  string   `json:"subnet"`
	Gateway string   `json:"gateway"`
	WiFi    []string `json:"wifi"` // wifi-iface sections fengard looks after
	SSID    string   `json:"ssid"`
	KeySum  string   `json:"keySum"`
	Undo    []Op     `json:"undo"`
	Reload  []string `json:"reload"` // what to reload after changing it, in order
	Notes   []string `json:"notes,omitempty"`
	// false until the changes are committed and checked, a restart before that undoes them
	Done bool `json:"done,omitempty"`
	// reloads not run yet, a restart halfway through runs the rest
	Pending []string `json:"pending,omitempty"`
}

type Plan struct {
	State State
	Do    []Op
}

func keySum(pw string) string {
	h := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(h[:8])
}

// MakePlan works out the uci changes for turning guest wi-fi on, and the ones that undo them.
// a guest network the router already has is used when its kept off the lan, else fengard makes its own
func MakePlan(r Router, w Want) (Plan, error) {
	if r.Wireless == nil || len(r.Wireless.OfType("wifi-device")) == 0 {
		return Plan{}, errors.New("this router has no Wi-Fi radios")
	}
	if net, zone, ifaces := existing(r); net != "" {
		return planExisting(r, w, net, zone, ifaces)
	}
	return planOwn(r, w)
}

func fields(s *Section, opt string) []string {
	var out []string
	for _, v := range s.Opts[opt] {
		out = append(out, strings.Fields(v)...)
	}
	return out
}

func zoneOf(r Router, net string) *Section {
	for _, z := range r.Firewall.OfType("zone") {
		if slices.Contains(fields(z, "network"), net) {
			return z
		}
	}
	return nil
}

func wanZone(r Router) string {
	for _, z := range r.Firewall.OfType("zone") {
		name := z.Get("name")
		if serverZone(name) {
			continue
		}
		if z.Get("masq") == "1" || name == "wan" {
			return name
		}
	}
	return "wan"
}

// gl.inet's vpn server zones masquerade too, their clients are inside the house
func serverZone(name string) bool {
	return strings.HasPrefix(name, "wgserver") || strings.HasPrefix(name, "ovpnserver")
}

// the router's own guest network, like gl.inet's guest with guest2g and guest5g. only when its zone
// forwards nowhere but the internet and its wi-fi isn't also on the lan
func existing(r Router) (string, *Section, []*Section) {
	var cands []string
	for _, z := range r.Firewall.OfType("zone") {
		if z.Get("name") == "guest" {
			cands = append(cands, fields(z, "network")...)
		}
	}
	if r.Network.Section("guest") != nil {
		cands = append(cands, "guest")
	}
	wan := wanZone(r)
	for _, n := range cands {
		if n == "lan" || n == ownNet {
			continue
		}
		ns := r.Network.Section(n)
		if ns == nil || ns.Type != "interface" || ns.Get("proto") != "static" {
			continue
		}
		if _, _, ok := subnetOf(ns); !ok {
			continue
		}
		z := zoneOf(r, n)
		if z == nil || slices.Contains(fields(z, "network"), "lan") {
			continue
		}
		isolated := true
		for _, f := range r.Firewall.OfType("forwarding") {
			if f.Get("src") == z.Get("name") && f.Get("dest") != wan {
				isolated = false
			}
		}
		if !isolated {
			continue
		}
		var ifaces []*Section
		for _, s := range r.Wireless.OfType("wifi-iface") {
			nets := fields(s, "network")
			if slices.Contains(nets, n) && !slices.Contains(nets, "lan") && (s.Get("mode") == "" || s.Get("mode") == "ap") {
				ifaces = append(ifaces, s)
			}
		}
		if len(ifaces) > 0 {
			return n, z, ifaces
		}
	}
	return "", nil, nil
}

// the router address and subnet from ipaddr and netmask, either style
func subnetOf(s *Section) (netip.Addr, netip.Prefix, bool) {
	ip := s.Get("ipaddr")
	if f := strings.Fields(ip); len(f) > 0 {
		ip = f[0]
	}
	if p, err := netip.ParsePrefix(ip); err == nil && p.Addr().Is4() {
		return p.Addr(), p.Masked(), true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return netip.Addr{}, netip.Prefix{}, false
	}
	bits := 24
	if m, err := netip.ParseAddr(s.Get("netmask")); err == nil && m.Is4() {
		b := m.As4()
		bits = 0
		for _, o := range b {
			for i := 7; i >= 0 && o&(1<<i) != 0; i-- {
				bits++
			}
		}
	}
	p, err := a.Prefix(bits)
	return a, p, err == nil
}

func (s *Section) key(pkg, opt string) string {
	if opt == "" {
		return pkg + "." + s.Name
	}
	return pkg + "." + s.Name + "." + opt
}

// sets an option and records how to put it back
func change(do, undo *[]Op, s *Section, pkg, opt, val string) {
	if s.Get(opt) == val && s.Has(opt) {
		return
	}
	*do = append(*do, set(s.key(pkg, opt), val))
	if s.Has(opt) {
		*undo = append(*undo, set(s.key(pkg, opt), strings.Join(s.Opts[opt], " ")))
	} else {
		*undo = append(*undo, del(s.key(pkg, opt)))
	}
}

// removes an option and records how to put it back
func remove(do, undo *[]Op, s *Section, pkg, opt string) {
	if !s.Has(opt) {
		return
	}
	*do = append(*do, del(s.key(pkg, opt)))
	*undo = append(*undo, set(s.key(pkg, opt), strings.Join(s.Opts[opt], " ")))
}

func reversed(ops []Op) []Op {
	out := slices.Clone(ops)
	slices.Reverse(out)
	return out
}

func portList(ports []int) string {
	var s []string
	for _, p := range ports {
		if p > 0 && !slices.Contains(s, strconv.Itoa(p)) {
			s = append(s, strconv.Itoa(p))
		}
	}
	return strings.Join(s, " ")
}

// lets guests reach the approval page on fengard's address through the guest zone's input reject
func webRuleOps(zone string, w Want) []Op {
	k := "firewall." + webRule
	ops := []Op{
		set(k, "rule"), set(k+".name", "Fengard-Guest-Page"), set(k+".src", zone),
		set(k+".proto", "tcp"), set(k+".dest_port", portList(w.Ports)), set(k+".target", "ACCEPT"),
	}
	if w.BlockIP != "" {
		ops = append(ops, set(k+".dest_ip", w.BlockIP))
	}
	return ops
}

func wifiOps(do, undo *[]Op, s *Section, w Want) {
	change(do, undo, s, "wireless", "ssid", w.SSID)
	if enc := s.Get("encryption"); !strings.HasPrefix(enc, "psk2") {
		change(do, undo, s, "wireless", "encryption", "psk2")
	}
	change(do, undo, s, "wireless", "key", w.Password)
	change(do, undo, s, "wireless", "isolate", "1")
	for _, opt := range []string{"disabled", "hidden"} {
		if s.Get(opt) == "1" {
			change(do, undo, s, "wireless", opt, "0")
		}
	}
}

func planExisting(r Router, w Want, net string, zone *Section, ifaces []*Section) (Plan, error) {
	ns := r.Network.Section(net)
	gw, sub, _ := subnetOf(ns)
	st := State{
		Mode: "existing", Network: net, Zone: zone.Get("name"), Subnet: sub.String(), Gateway: gw.String(),
		SSID: w.SSID, KeySum: keySum(w.Password), Device: "br-" + net,
	}
	if d := ns.Get("device"); d != "" && !strings.HasPrefix(d, "@") {
		st.Device = d
	}
	var do, undo []Op
	reload := []string{}
	nchanged := false
	if ns.Get("disabled") == "1" {
		change(&do, &undo, ns, "network", "disabled", "0")
		nchanged = true
	}
	// ipv4 only, the iptables firewall can't hold unapproved guests back on ipv6
	if ns.Has("ip6assign") {
		remove(&do, &undo, ns, "network", "ip6assign")
		nchanged = true
	}
	if nchanged {
		reload = append(reload, "network")
	}
	for _, s := range ifaces {
		if slices.Contains(w.Busy, s.Get("device")) {
			// kept off so it can never come up open with the driver's default name
			if s.Get("disabled") != "1" {
				change(&do, &undo, s, "wireless", "disabled", "1")
			}
			st.Notes = append(st.Notes, busyNote(r, s.Get("device")))
			continue
		}
		st.WiFi = append(st.WiFi, s.Name)
		wifiOps(&do, &undo, s, w)
		if radio := r.Wireless.Section(s.Get("device")); radio != nil && radio.Get("disabled") == "1" {
			st.Notes = append(st.Notes, fmt.Sprintf("The %s radio is switched off in the router's settings, so %s stays off.", s.Get("device"), s.Name))
		}
	}
	var dh *Section
	for _, s := range r.DHCP.OfType("dhcp") {
		if s.Get("interface") == net {
			dh = s
			break
		}
	}
	if dh == nil {
		do = append(do, dhcpOps(ownNet, net, gw)...)
		undo = append(undo, del("dhcp."+ownNet))
		reload = append(reload, "dnsmasq")
	} else {
		dchanged := false
		if dh.Get("ignore") == "1" {
			change(&do, &undo, dh, "dhcp", "ignore", "0")
			dchanged = true
		}
		// dnsmasq doesn't answer dns so guests have to be told where to ask
		if !slices.ContainsFunc(fields(dh, "dhcp_option"), func(o string) bool { return strings.HasPrefix(o, "6,") }) {
			do = append(do, addList(dh.key("dhcp", "dhcp_option"), "6,"+gw.String()))
			if dh.Has("dhcp_option") {
				undo = append(undo, delList(dh.key("dhcp", "dhcp_option"), "6,"+gw.String()))
			} else {
				undo = append(undo, del(dh.key("dhcp", "dhcp_option")))
			}
			dchanged = true
		}
		if dchanged {
			reload = append(reload, "dnsmasq")
		}
	}
	if len(st.WiFi) == 0 {
		return Plan{}, errors.New("every radio on this router is busy repeating another network, so guest Wi-Fi has no radio to run on")
	}
	do = append(do, webRuleOps(st.Zone, w)...)
	undo = append(undo, del("firewall."+webRule))
	st.Undo = reversed(undo)
	st.Reload = append(reload, "wifi", "firewall")
	return Plan{State: st, Do: do}, nil
}

// busyNote says which band stays without guest wi-fi and why
func busyNote(r Router, device string) string {
	band := device
	if d := r.Wireless.Section(device); d != nil {
		band = bandName(d)
	}
	return "Guest Wi-Fi is off on " + band + " because the router uses that band to repeat another network, and the Wi-Fi chip can't run a guest network beside it."
}

func bandName(d *Section) string {
	switch b := d.Get("band"); {
	case b == "2g" || d.Get("hwmode") == "11g" || d.Get("hwmode") == "11b":
		return "2.4 GHz"
	case b == "5g" || d.Get("hwmode") == "11a":
		return "5 GHz"
	case b == "6g":
		return "6 GHz"
	}
	return d.Name
}

func dhcpOps(section, net string, gw netip.Addr) []Op {
	k := "dhcp." + section
	return []Op{
		set(k, "dhcp"), set(k+".interface", net), set(k+".start", "100"), set(k+".limit", "150"),
		set(k+".leasetime", "2h"), addList(k+".dhcp_option", "6,"+gw.String()),
	}
}

// PickSubnet finds a 192.168.x.0/24 that doesn't overlap anything the router has
func PickSubnet(used []netip.Prefix) (netip.Prefix, bool) {
	var order []int
	for n := 77; n <= 254; n++ {
		order = append(order, n)
	}
	for n := 2; n < 77; n++ {
		order = append(order, n)
	}
	for _, n := range order {
		p := netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 168, byte(n), 0}), 24)
		if !slices.ContainsFunc(used, p.Overlaps) {
			return p, true
		}
	}
	return netip.Prefix{}, false
}

func planOwn(r Router, w Want) (Plan, error) {
	used := slices.Clone(w.Used)
	for _, s := range r.Network.OfType("interface") {
		if s.Name == ownNet {
			continue
		}
		if _, p, ok := subnetOf(s); ok {
			used = append(used, p)
		}
	}
	sub, ok := PickSubnet(used)
	if !ok {
		return Plan{}, errors.New("no free 192.168.x.0/24 network for guests")
	}
	gw := sub.Addr().Next()
	st := State{
		Mode: "own", Network: ownNet, Zone: ownNet, Device: ownDev, Subnet: sub.String(), Gateway: gw.String(),
		SSID: w.SSID, KeySum: keySum(w.Password),
	}
	var do, undo []Op
	n := "network." + ownNet
	if w.NewSyntax {
		d := "network." + ownNet + "_dev"
		do = append(do, set(d, "device"), set(d+".name", ownDev), set(d+".type", "bridge"), set(d+".bridge_empty", "1"))
		undo = append(undo, del(d))
		do = append(do, set(n, "interface"), set(n+".device", ownDev))
	} else {
		do = append(do, set(n, "interface"), set(n+".type", "bridge"), set(n+".bridge_empty", "1"))
	}
	do = append(do, set(n+".proto", "static"), set(n+".ipaddr", gw.String()), set(n+".netmask", "255.255.255.0"))
	undo = append(undo, del(n))

	do = append(do, dhcpOps(ownNet, ownNet, gw)...)
	undo = append(undo, del("dhcp."+ownNet))

	z := "firewall." + ownNet
	do = append(do, set(z, "zone"), set(z+".name", ownNet), addList(z+".network", ownNet),
		set(z+".input", "REJECT"), set(z+".output", "ACCEPT"), set(z+".forward", "REJECT"))
	f := z + "_wan"
	do = append(do, set(f, "forwarding"), set(f+".src", ownNet), set(f+".dest", wanZone(r)))
	for _, rule := range []struct{ id, name, proto, port string }{
		{"dhcp", "Fengard-Guest-DHCP", "udp", "67"},
		{"dns", "Fengard-Guest-DNS", "tcp udp", "53"},
	} {
		k := z + "_" + rule.id
		do = append(do, set(k, "rule"), set(k+".name", rule.name), set(k+".src", ownNet),
			set(k+".proto", rule.proto), set(k+".dest_port", rule.port), set(k+".target", "ACCEPT"))
		undo = append(undo, del(k))
	}
	do = append(do, set(z+"_dhcp.family", "ipv4"))
	undo = append(undo, del(f), del(z))
	do = append(do, webRuleOps(ownNet, w)...)
	undo = append(undo, del("firewall."+webRule))

	for _, radio := range r.Wireless.OfType("wifi-device") {
		if slices.Contains(w.Busy, radio.Name) {
			st.Notes = append(st.Notes, busyNote(r, radio.Name))
			continue
		}
		s := "wireless." + ownNet + "_" + safeName(radio.Name)
		st.WiFi = append(st.WiFi, ownNet+"_"+safeName(radio.Name))
		do = append(do, set(s, "wifi-iface"), set(s+".device", radio.Name), set(s+".mode", "ap"),
			set(s+".network", ownNet), set(s+".ssid", w.SSID), set(s+".encryption", "psk2"),
			set(s+".key", w.Password), set(s+".isolate", "1"))
		undo = append(undo, del(s))
		if radio.Get("disabled") == "1" {
			st.Notes = append(st.Notes, fmt.Sprintf("The %s radio is switched off in the router's settings, so guests can't use it.", radio.Name))
		}
	}
	if len(st.WiFi) == 0 {
		return Plan{}, errors.New("every radio on this router is busy repeating another network, so guest Wi-Fi has no radio to run on")
	}
	st.Undo = reversed(undo)
	st.Reload = []string{"network", "wifi", "dnsmasq", "firewall"}
	return Plan{State: st, Do: do}, nil
}

// uci section names are letters digits and underscores
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// Rename is the changes for a new name or password while guest wi-fi is on. they need no undo,
// turning it off puts back what was there before fengard
func Rename(st State, w Want) []Op {
	var ops []Op
	for _, s := range st.WiFi {
		ops = append(ops, set("wireless."+s+".ssid", w.SSID), set("wireless."+s+".key", w.Password))
	}
	return ops
}

type iwBlock struct {
	iface, essid, mode string
	ghz                float64
}

var iwChannel = regexp.MustCompile(`Channel: \S+ \(([0-9.]+) GHz\)`)

// BusyRadios reads iwinfo for client links with a real network, like a repeater uplink, and gives
// the radios on the same band. it goes by band since gl.inet keeps its repeater outside uci
func BusyRadios(iwinfo []byte, wireless *Package) []string {
	var blocks []iwBlock
	for _, line := range strings.Split(string(iwinfo), "\n") {
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			b := iwBlock{iface: strings.Fields(line)[0]}
			if m := essidRe.FindStringSubmatch(line); m != nil {
				b.essid = m[1]
			}
			blocks = append(blocks, b)
			continue
		}
		if len(blocks) == 0 {
			continue
		}
		b := &blocks[len(blocks)-1]
		if i := strings.Index(line, "Mode: "); i >= 0 && b.mode == "" {
			b.mode = strings.Fields(line[i+6:])[0]
		}
		if m := iwChannel.FindStringSubmatch(line); m != nil {
			b.ghz, _ = strconv.ParseFloat(m[1], 64)
		}
	}
	bands := map[string]bool{}
	for _, b := range blocks {
		if b.mode != "Client" || b.essid == "" || b.essid == "unknown" || b.ghz == 0 {
			continue
		}
		switch {
		case b.ghz < 3:
			bands["2.4 GHz"] = true
		case b.ghz < 5.9:
			bands["5 GHz"] = true
		default:
			bands["6 GHz"] = true
		}
	}
	var out []string
	if wireless == nil {
		return nil
	}
	for _, d := range wireless.OfType("wifi-device") {
		if bands[bandName(d)] {
			out = append(out, d.Name)
		}
	}
	return out
}
