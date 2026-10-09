// own table next to fw4 so a firewall reload doesnt touch it
// all in kernel so if the daemon dies the rules still work
package firewall

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/masaleem-oss/Fengard/internal/config"
)

type Params struct {
	LAN, WAN     []string
	RouterIPv4   string
	RouterIPv6   string
	ServicePorts []int // never reachable from wan

	BlockBypass bool
	DoHIPs      []string

	PortForwards []config.PortForward
	// lan traffic to BlockIP 80 443 gets redirected here when something else owns those ports
	BlockIP     string
	HTTPPort    int
	HTTPSPort   int
	Quarantined []string
	Offline     []string
	OfflineIPs  []string // vpn clients have no mac on the tunnel

	TorIPs    []string
	TorMACs   []string
	TorSrcIPs []string

	// wireguard and tailscale ifaces get treated like lan
	Tunnels []VPNParams

	// per source per second
	SynPerSecond  int
	ICMPPerSecond int
	// in kernel so one flooding device cant starve the rest
	DNSPerSecond int
}

type VPNParams struct {
	Iface  string
	Port   int // 0 if it dials out itself
	Subnet string
}

func (p Params) lanIfaces() []string {
	out := append([]string{}, p.LAN...)
	for _, t := range p.Tunnels {
		if t.Iface != "" {
			out = append(out, t.Iface)
		}
	}
	return out
}

const Table = "fengard"

// create then delete the table so nft -f swaps it in one go
func Render(p Params) (string, error) {
	if len(p.LAN) == 0 || len(p.WAN) == 0 {
		return "", fmt.Errorf("LAN and WAN interfaces are required")
	}
	if ip := net.ParseIP(p.RouterIPv4); ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("invalid router IPv4 %q", p.RouterIPv4)
	}
	if p.SynPerSecond <= 0 {
		p.SynPerSecond = 50
	}
	if p.ICMPPerSecond <= 0 {
		p.ICMPPerSecond = 10
	}
	if p.DNSPerSecond <= 0 {
		p.DNSPerSecond = 200
	}
	lan, wan := ifset(p.lanIfaces()), ifset(p.WAN)
	v4, v6 := splitIPs(p.DoHIPs)
	tor4, tor6 := splitIPs(p.TorIPs)
	torSrc4, _ := splitIPs(p.TorSrcIPs)
	off4, _ := splitIPs(p.OfflineIPs)

	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	w("table inet %s {}", Table)
	w("delete table inet %s", Table)
	w("table inet %s {", Table)

	set := func(name, typ, flags string, elems []string) {
		w("\tset %s {", name)
		w("\t\ttype %s", typ)
		if flags != "" {
			w("\t\tflags %s", flags)
		}
		if flags == "interval" {
			w("\t\tauto-merge") // blocklists overlap a lot
		}
		if len(elems) > 0 {
			w("\t\telements = { %s }", strings.Join(elems, ", "))
		}
		w("\t}")
	}
	set("quarantined", "ether_addr", "", macs(p.Quarantined))
	set("offline", "ether_addr", "", macs(p.Offline))
	set("doh4", "ipv4_addr", "interval", v4)
	set("doh6", "ipv6_addr", "interval", v6)
	set("tor4", "ipv4_addr", "interval", tor4)
	set("tor6", "ipv6_addr", "interval", tor6)
	set("tor_devs", "ether_addr", "", macs(p.TorMACs))
	set("tor_src4", "ipv4_addr", "", torSrc4)
	set("offline4", "ipv4_addr", "", off4)
	w("\tset syn4 {\n\t\ttype ipv4_addr\n\t\tflags dynamic, timeout\n\t\ttimeout 1m\n\t\tsize 65536\n\t}")
	w("\tset syn6 {\n\t\ttype ipv6_addr\n\t\tflags dynamic, timeout\n\t\ttimeout 1m\n\t\tsize 65536\n\t}")
	w("\tset dns4 {\n\t\ttype ipv4_addr\n\t\tflags dynamic, timeout\n\t\ttimeout 1m\n\t\tsize 8192\n\t}")
	w("\tset dns6 {\n\t\ttype ipv6_addr\n\t\tflags dynamic, timeout\n\t\ttimeout 1m\n\t\tsize 8192\n\t}")

	w("")
	w("\tchain dstnat {")
	w("\t\ttype nat hook prerouting priority dstnat - 5; policy accept;")
	if p.BlockBypass {
		w("\t\t# Devices can't bypass filtering with their own DNS server")
		w("\t\tiifname %s meta nfproto ipv4 meta l4proto { tcp, udp } th dport 53 ip daddr != %s dnat ip to %s:53", lan, p.RouterIPv4, p.RouterIPv4)
		if p.RouterIPv6 != "" {
			w("\t\tiifname %s meta nfproto ipv6 meta l4proto { tcp, udp } th dport 53 ip6 daddr != %s dnat ip6 to [%s]:53", lan, p.RouterIPv6, p.RouterIPv6)
		}
	}
	for _, t := range p.Tunnels {
		if !p.BlockBypass {
			w("\t\tiifname %q meta l4proto { tcp, udp } th dport 53 dnat ip to %s:53", t.Iface, p.RouterIPv4)
		}
	}
	for _, r := range p.webRedirects() {
		w("\t\tiifname %s ip daddr %s tcp dport %d dnat ip to %s:%d comment %q", lan, p.BlockIP, r[0], p.BlockIP, r[1], "fengard web")
	}
	fwds := enabledForwards(p.PortForwards)
	for _, f := range fwds {
		for _, proto := range protos(f.Proto) {
			w("\t\tiifname %s %s dport %d dnat ip to %s:%d comment %q", wan, proto, f.ExtPort, f.DestIP, f.DestPort, "fwd "+f.Name)
		}
	}
	w("\t}")

	if len(p.Tunnels) > 0 {
		w("")
		w("\tchain srcnat {")
		w("\t\ttype nat hook postrouting priority srcnat - 5; policy accept;")
		for _, t := range p.Tunnels {
			w("\t\toifname %s ip saddr %s masquerade comment %q", wan, t.Subnet, "fengard "+t.Iface)
		}
		w("\t}")
	}

	w("")
	w("\tchain forward {")
	w("\t\ttype filter hook forward priority filter - 5; policy accept;")
	w("\t\tct state invalid drop")
	w("\t\tiifname %s ether saddr @quarantined counter reject with icmpx admin-prohibited", lan)
	w("\t\tiifname %s oifname %s ether saddr @offline counter reject with icmpx admin-prohibited", lan, wan)
	w("\t\tiifname %s oifname %s ip saddr @offline4 counter reject with icmpx admin-prohibited", lan, wan)
	w("\t\t# Tor relays, for profiles that block Tor")
	w("\t\tiifname %s oifname %s ether saddr @tor_devs ip daddr @tor4 counter reject with icmpx admin-prohibited", lan, wan)
	w("\t\tiifname %s oifname %s ether saddr @tor_devs ip6 daddr @tor6 counter reject with icmpx admin-prohibited", lan, wan)
	w("\t\tiifname %s oifname %s ip saddr @tor_src4 ip daddr @tor4 counter reject with icmpx admin-prohibited", lan, wan)
	if p.BlockBypass {
		w("\t\t# Encrypted DNS (DoH/DoT/DoQ) would skip Fengard's filtering")
		w("\t\tiifname %s oifname %s ip daddr @doh4 meta l4proto { tcp, udp } th dport 443 counter reject with icmpx admin-prohibited", lan, wan)
		w("\t\tiifname %s oifname %s ip6 daddr @doh6 meta l4proto { tcp, udp } th dport 443 counter reject with icmpx admin-prohibited", lan, wan)
		w("\t\tiifname %s oifname %s meta l4proto { tcp, udp } th dport 853 counter reject with icmpx admin-prohibited", lan, wan)
	}
	if len(fwds) > 0 {
		w("\t\tiifname %s ct status dnat accept", wan)
	}
	for _, t := range p.Tunnels {
		w("\t\t# %s clients reach the internet and the LAN; replies come back", t.Iface)
		w("\t\tiifname %q accept", t.Iface)
		w("\t\toifname %q ct state established,related accept", t.Iface)
	}
	w("\t}")

	w("")
	w("\tchain input {")
	w("\t\ttype filter hook input priority filter - 5; policy accept;")
	w("\t\tiifname %s ct state invalid drop", wan)
	w("\t\tiifname %s ct state established,related accept", wan)
	for _, t := range p.Tunnels {
		if t.Port > 0 {
			w("\t\tiifname %s udp dport %d accept comment %q", wan, t.Port, "fengard "+t.Iface)
		}
		w("\t\tiifname %q accept", t.Iface)
	}
	if ports := servicePorts(p.ServicePorts); ports != "" {
		w("\t\t# Fengard's DNS, block page and dashboard are never exposed to the internet")
		w("\t\tiifname %s meta l4proto { tcp, udp } th dport { %s } drop", wan, ports)
	}
	w("\t\t# A device flooding DNS is dropped here, before it can fill the socket buffer")
	w("\t\tiifname %s meta nfproto ipv4 udp dport 53 update @dns4 { ip saddr limit rate over %d/second burst %d packets } counter drop", lan, p.DNSPerSecond, p.DNSPerSecond*4)
	w("\t\tiifname %s meta nfproto ipv6 udp dport 53 update @dns6 { ip6 saddr limit rate over %d/second burst %d packets } counter drop", lan, p.DNSPerSecond, p.DNSPerSecond*4)
	w("\t\t# Per-source SYN flood limit")
	w("\t\tiifname %s meta nfproto ipv4 tcp flags & (fin|syn|rst|ack) == syn update @syn4 { ip saddr limit rate over %d/second burst %d packets } counter drop", wan, p.SynPerSecond, p.SynPerSecond*2)
	w("\t\tiifname %s meta nfproto ipv6 tcp flags & (fin|syn|rst|ack) == syn update @syn6 { ip6 saddr limit rate over %d/second burst %d packets } counter drop", wan, p.SynPerSecond, p.SynPerSecond*2)
	w("\t\tiifname %s icmp type echo-request limit rate over %d/second burst %d packets drop", wan, p.ICMPPerSecond, p.ICMPPerSecond*2)
	w("\t\tiifname %s icmpv6 type echo-request limit rate over %d/second burst %d packets drop", wan, p.ICMPPerSecond, p.ICMPPerSecond*2)
	w("\t}")
	w("}")
	return b.String(), nil
}

func (p Params) webRedirects() [][2]int {
	var out [][2]int
	if p.BlockIP == "" {
		return out
	}
	if p.HTTPPort > 0 && p.HTTPPort != 80 {
		out = append(out, [2]int{80, p.HTTPPort})
	}
	if p.HTTPSPort > 0 && p.HTTPSPort != 443 {
		out = append(out, [2]int{443, p.HTTPSPort})
	}
	return out
}

func ifset(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = fmt.Sprintf("%q", n)
	}
	return "{ " + strings.Join(q, ", ") + " }"
}

func macs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, m := range in {
		if hw, err := net.ParseMAC(m); err == nil && len(hw) == 6 {
			out = append(out, hw.String())
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

func splitIPs(in []string) (v4, v6 []string) {
	for _, s := range in {
		var ip net.IP
		if strings.Contains(s, "/") {
			var n *net.IPNet
			var err error
			if ip, n, err = net.ParseCIDR(s); err != nil {
				continue
			}
			s = n.String()
		} else if ip = net.ParseIP(s); ip == nil {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, s)
		} else {
			v6 = append(v6, s)
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return dedupe(v4), dedupe(v6)
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func enabledForwards(in []config.PortForward) []config.PortForward {
	var out []config.PortForward
	for _, f := range in {
		if f.Enabled {
			out = append(out, f)
		}
	}
	return out
}

func protos(p string) []string {
	if p == "both" {
		return []string{"tcp", "udp"}
	}
	return []string{p}
}

func servicePorts(ports []int) string {
	var s []string
	seen := map[int]bool{}
	for _, p := range ports {
		if p > 0 && p < 65536 && !seen[p] {
			seen[p] = true
			s = append(s, fmt.Sprint(p))
		}
	}
	return strings.Join(s, ", ")
}
