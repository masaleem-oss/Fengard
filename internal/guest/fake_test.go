package guest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// fakeRouter answers uci, ubus and the reload commands from memory
type fakeRouter struct {
	mu       sync.Mutex
	pkgs     map[string]*Package
	staged   map[string]*Package // uci keeps uncommitted changes apart until commit
	ran      []string
	failOn   string // a batch containing this line fails before committing
	skip     string // a line uci batch drops without saying so
	ubus     map[string]string
	hostapds []string
}

func newFake(show map[string]string) *fakeRouter {
	f := &fakeRouter{pkgs: map[string]*Package{}, staged: map[string]*Package{}, ubus: map[string]string{}}
	for name, out := range show {
		f.pkgs[name] = ParseShow(name, []byte(out))
	}
	return f
}

func clonePkg(p *Package) *Package {
	n := &Package{Name: p.Name}
	for _, s := range p.Sections {
		c := &Section{Name: s.Name, Type: s.Type, Opts: map[string][]string{}}
		for k, v := range s.Opts {
			c.Opts[k] = slices.Clone(v)
		}
		n.Sections = append(n.Sections, c)
	}
	return n
}

// show renders a package the way uci -X show does
func show(p *Package) string {
	var b strings.Builder
	for _, s := range p.Sections {
		fmt.Fprintf(&b, "%s.%s=%s\n", p.Name, s.Name, s.Type)
		var keys []string
		for k := range s.Opts {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			var q []string
			for _, v := range s.Opts[k] {
				q = append(q, quote(v))
			}
			fmt.Fprintf(&b, "%s.%s.%s=%s\n", p.Name, s.Name, k, strings.Join(q, " "))
		}
	}
	return b.String()
}

func (f *fakeRouter) work(name string) *Package {
	if p := f.staged[name]; p != nil {
		return p
	}
	p := f.pkgs[name]
	if p == nil {
		p = &Package{Name: name}
	}
	f.staged[name] = clonePkg(p)
	return f.staged[name]
}

func (f *fakeRouter) apply(line string) error {
	cmd, rest, _ := strings.Cut(line, " ")
	if cmd == "commit" {
		if p := f.staged[rest]; p != nil {
			f.pkgs[rest] = p
			delete(f.staged, rest)
		}
		return nil
	}
	key, val, hasVal := strings.Cut(rest, "=")
	parts := strings.SplitN(key, ".", 3)
	p := f.work(parts[0])
	s := p.Section(parts[1])
	v := ""
	if hasVal {
		v = strings.Join(words(val), " ")
	}
	switch {
	case cmd == "set" && len(parts) == 2:
		if s == nil {
			p.Sections = append(p.Sections, &Section{Name: parts[1], Type: v, Opts: map[string][]string{}})
		} else {
			s.Type = v
		}
	case s == nil:
		return fmt.Errorf("uci: Entry not found: %s", key)
	case cmd == "set":
		s.Opts[parts[2]] = []string{v}
	case cmd == "add_list":
		s.Opts[parts[2]] = append(s.Opts[parts[2]], v)
	case cmd == "del_list":
		if !slices.Contains(s.Opts[parts[2]], v) {
			return fmt.Errorf("uci: Entry not found: %s", key)
		}
		s.Opts[parts[2]] = slices.DeleteFunc(s.Opts[parts[2]], func(x string) bool { return x == v })
	case cmd == "delete" && len(parts) == 2:
		p.Sections = slices.DeleteFunc(p.Sections, func(x *Section) bool { return x == s })
	case cmd == "delete":
		if !s.Has(parts[2]) {
			return fmt.Errorf("uci: Entry not found: %s", key)
		}
		delete(s.Opts, parts[2])
	default:
		return fmt.Errorf("uci: unknown command %s", cmd)
	}
	return nil
}

func (f *fakeRouter) exec(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.ran = append(f.ran, call)
	switch {
	case call == "uci batch":
		for _, l := range strings.Split(strings.TrimSpace(string(stdin)), "\n") {
			if f.failOn != "" && l == f.failOn {
				return nil, fmt.Errorf("uci: Invalid argument")
			}
			if f.skip != "" && strings.HasPrefix(l, f.skip) {
				continue
			}
			// like uci batch, the first bad line stops the rest
			if err := f.apply(l); err != nil {
				return nil, err
			}
		}
		return nil, nil
	case strings.HasPrefix(call, "uci revert "):
		delete(f.staged, args[1])
		return nil, nil
	case strings.HasPrefix(call, "uci -q -X show "):
		p := f.pkgs[args[3]]
		if p == nil {
			return nil, fmt.Errorf("exit status 1")
		}
		return []byte(show(p)), nil
	case call == "ubus list hostapd.*":
		return []byte(strings.Join(f.hostapds, "\n")), nil
	case strings.HasPrefix(call, "ubus call hostapd."):
		return nil, nil
	case strings.HasPrefix(call, "ubus call "):
		if out, ok := f.ubus[args[1]]; ok {
			return []byte(out), nil
		}
		return nil, fmt.Errorf("Not found")
	}
	return nil, nil
}

func (f *fakeRouter) snapshot() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for n, p := range f.pkgs {
		out[n] = show(p)
	}
	return out
}

func (f *fakeRouter) calls(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.ran {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// trimmed from a gl.inet gl-mt3000 on firmware 4.x
var glinet = map[string]string{
	"network": `network.loopback=interface
network.loopback.device='lo'
network.loopback.proto='static'
network.loopback.ipaddr='127.0.0.1'
network.loopback.netmask='255.0.0.0'
network.cfg030f15=device
network.cfg030f15.name='br-lan'
network.cfg030f15.type='bridge'
network.cfg030f15.ports='eth1'
network.lan=interface
network.lan.device='br-lan'
network.lan.proto='static'
network.lan.ipaddr='192.168.8.1'
network.lan.netmask='255.255.255.0'
network.wan=interface
network.wan.device='eth0'
network.wan.proto='dhcp'
network.guest=interface
network.guest.proto='static'
network.guest.ipaddr='192.168.9.1'
network.guest.netmask='255.255.255.0'
network.guest.type='bridge'
network.guest.ip6assign='60'
`,
	"wireless": `wireless.mt798111=wifi-device
wireless.mt798111.type='mtwifi'
wireless.mt798111.band='2g'
wireless.mt798112=wifi-device
wireless.mt798112.type='mtwifi'
wireless.mt798112.band='5g'
wireless.default_radio0=wifi-iface
wireless.default_radio0.device='mt798111'
wireless.default_radio0.network='lan'
wireless.default_radio0.mode='ap'
wireless.default_radio0.ssid='GL-MT3000-a1b'
wireless.default_radio0.encryption='psk2'
wireless.default_radio0.key='householdpass'
wireless.guest2g=wifi-iface
wireless.guest2g.device='mt798111'
wireless.guest2g.network='guest'
wireless.guest2g.mode='ap'
wireless.guest2g.ssid='GL-MT3000-a1b-Guest'
wireless.guest2g.encryption='none'
wireless.guest2g.isolate='1'
wireless.guest2g.disabled='1'
wireless.guest5g=wifi-iface
wireless.guest5g.device='mt798112'
wireless.guest5g.network='guest'
wireless.guest5g.mode='ap'
wireless.guest5g.ssid='GL-MT3000-a1b-5G-Guest'
wireless.guest5g.encryption='psk2'
wireless.guest5g.key='oldguestpw'
wireless.guest5g.disabled='1'
wireless.guest5g.hidden='1'
`,
	"firewall": `firewall.cfg01e63d=defaults
firewall.cfg01e63d.input='ACCEPT'
firewall.cfg02dc81=zone
firewall.cfg02dc81.name='lan'
firewall.cfg02dc81.network='lan' 'fgwg0'
firewall.cfg02dc81.input='ACCEPT'
firewall.cfg03dc81=zone
firewall.cfg03dc81.name='wan'
firewall.cfg03dc81.network='wan' 'wan6'
firewall.cfg03dc81.masq='1'
firewall.cfg03dc81.input='REJECT'
firewall.cfg04ad58=forwarding
firewall.cfg04ad58.src='lan'
firewall.cfg04ad58.dest='wan'
firewall.guest_zone=zone
firewall.guest_zone.name='guest'
firewall.guest_zone.network='guest'
firewall.guest_zone.input='REJECT'
firewall.guest_zone.output='ACCEPT'
firewall.guest_zone.forward='REJECT'
firewall.guest_forward=forwarding
firewall.guest_forward.src='guest'
firewall.guest_forward.dest='wan'
firewall.guest_dhcp=rule
firewall.guest_dhcp.name='Allow-DHCP'
firewall.guest_dhcp.src='guest'
firewall.guest_dhcp.dest_port='67-68'
firewall.guest_dhcp.target='ACCEPT'
firewall.guest_dns=rule
firewall.guest_dns.name='Allow-DNS'
firewall.guest_dns.src='guest'
firewall.guest_dns.dest_port='53'
firewall.guest_dns.target='ACCEPT'
`,
	"dhcp": `dhcp.cfg01411c=dnsmasq
dhcp.cfg01411c.port='0'
dhcp.lan=dhcp
dhcp.lan.interface='lan'
dhcp.lan.start='100'
dhcp.lan.limit='150'
dhcp.lan.dhcp_option='6,192.168.8.1'
dhcp.guest=dhcp
dhcp.guest.interface='guest'
dhcp.guest.start='100'
dhcp.guest.limit='150'
`,
}

// a plain openwrt 23.05 router with two radios and no guest network
var plain = map[string]string{
	"network": `network.loopback=interface
network.loopback.device='lo'
network.loopback.proto='static'
network.loopback.ipaddr='127.0.0.1'
network.loopback.netmask='255.0.0.0'
network.cfg030f15=device
network.cfg030f15.name='br-lan'
network.cfg030f15.type='bridge'
network.cfg030f15.ports='lan1' 'lan2'
network.lan=interface
network.lan.device='br-lan'
network.lan.proto='static'
network.lan.ipaddr='192.168.77.1/24'
network.iot=interface
network.iot.proto='static'
network.iot.ipaddr='192.168.78.1'
network.iot.netmask='255.255.255.0'
network.wan=interface
network.wan.device='wan'
network.wan.proto='dhcp'
`,
	"wireless": `wireless.radio0=wifi-device
wireless.radio0.type='mac80211'
wireless.radio0.band='2g'
wireless.radio1=wifi-device
wireless.radio1.type='mac80211'
wireless.radio1.band='5g'
wireless.radio1.disabled='1'
wireless.default_radio0=wifi-iface
wireless.default_radio0.device='radio0'
wireless.default_radio0.network='lan'
wireless.default_radio0.mode='ap'
wireless.default_radio0.ssid='OpenWrt'
wireless.default_radio0.encryption='sae-mixed'
wireless.default_radio0.key='it'\''s home'
`,
	"firewall": `firewall.cfg01e63d=defaults
firewall.cfg02dc81=zone
firewall.cfg02dc81.name='lan'
firewall.cfg02dc81.network='lan'
firewall.cfg03dc81=zone
firewall.cfg03dc81.name='wan'
firewall.cfg03dc81.network='wan' 'wan6'
firewall.cfg03dc81.masq='1'
firewall.wgserver=zone
firewall.wgserver.name='wgserver'
firewall.wgserver.masq='1'
`,
	"dhcp": `dhcp.cfg01411c=dnsmasq
dhcp.lan=dhcp
dhcp.lan.interface='lan'
`,
}
