package firewall

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
)

// for routers still on iptables like openwrt 21.02 and glinet 4.x firmware
type IPTables struct {
	Netns string

	once   sync.Once
	ipset  bool
	hashl  bool
	mac    bool
	v6     bool
	probed []string
}

const (
	chDstnat  = "FENGARD_DSTNAT"
	chSrcnat  = "FENGARD_SRCNAT"
	chForward = "FENGARD_FORWARD"
	chInput   = "FENGARD_INPUT"
	setTor4   = "fengard_tor4"
	setTorDev = "fengard_tordev"
	setTorSrc = "fengard_torsrc"
	setOff4   = "fengard_off4"
	setDoH4   = "fengard_doh4"
	setDoH6   = "fengard_doh6"
)

func NewIPTables(netns string) *IPTables { return &IPTables{Netns: netns} }

func (t *IPTables) Name() string { return "iptables" }

func (t *IPTables) probe() {
	t.once.Do(func() {
		_, err := run(t.Netns, "ipset", nil, "list", "-n")
		t.ipset = err == nil
		_, err = run(t.Netns, "iptables", nil, "-m", "hashlimit", "-h")
		t.hashl = err == nil
		_, err = run(t.Netns, "iptables", nil, "-m", "mac", "-h")
		t.mac = err == nil
		_, err = run(t.Netns, "ip6tables", nil, "-S")
		t.v6 = err == nil
		for name, ok := range map[string]bool{"ipset": t.ipset, "hashlimit": t.hashl, "mac": t.mac, "ip6tables": t.v6} {
			if !ok {
				t.probed = append(t.probed, "no "+name)
			}
		}
	})
}

func (t *IPTables) Render(p Params) (string, error) {
	t.probe()
	return renderIPTables(p, t.ipset, t.hashl, t.mac, t.probed), nil
}

func renderIPTables(p Params, ipset, hashlimit, macMatch bool, notes []string) string {
	if p.SynPerSecond <= 0 {
		p.SynPerSecond = 50
	}
	if p.ICMPPerSecond <= 0 {
		p.ICMPPerSecond = 10
	}
	if p.DNSPerSecond <= 0 {
		p.DNSPerSecond = 200
	}
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	w("# Fengard rules for iptables-restore --noflush")
	for _, n := range notes {
		w("# note: %s", n)
	}
	// sets live outside the rules so hash them or a list refresh wouldnt reapply
	h := sha256.New()
	for _, l := range [][]string{p.DoHIPs, p.TorIPs, p.TorMACs, p.TorSrcIPs, p.OfflineIPs} {
		fmt.Fprintf(h, "%d:%s;", len(l), strings.Join(l, ","))
	}
	w("# sets %x", h.Sum(nil)[:8])

	lans := p.lanIfaces()
	w("*nat")
	w(":%s - [0:0]", chDstnat)
	w(":%s - [0:0]", chSrcnat)
	tunnel := map[string]bool{}
	for _, t := range p.Tunnels {
		tunnel[t.Iface] = true
	}
	for _, lan := range lans {
		if p.BlockBypass || tunnel[lan] {
			for _, proto := range []string{"udp", "tcp"} {
				w("-A %s -i %s -p %s --dport 53 ! -d %s -j DNAT --to-destination %s:53", chDstnat, lan, proto, p.RouterIPv4, p.RouterIPv4)
			}
		}
	}
	for _, lan := range lans {
		for _, r := range p.webRedirects() {
			w("-A %s -i %s -d %s -p tcp --dport %d -j DNAT --to-destination %s:%d", chDstnat, lan, p.BlockIP, r[0], p.BlockIP, r[1])
		}
	}
	for _, wan := range p.WAN {
		for _, f := range enabledForwards(p.PortForwards) {
			for _, proto := range protos(f.Proto) {
				w("-A %s -i %s -p %s --dport %d -j DNAT --to-destination %s:%d", chDstnat, wan, proto, f.ExtPort, f.DestIP, f.DestPort)
			}
		}
	}
	for _, t := range p.Tunnels {
		for _, wan := range p.WAN {
			w("-A %s -s %s -o %s -j MASQUERADE", chSrcnat, t.Subnet, wan)
		}
	}
	w("COMMIT")

	w("*filter")
	w(":%s - [0:0]", chForward)
	w(":%s - [0:0]", chInput)
	w("-A %s -m conntrack --ctstate INVALID -j DROP", chForward)
	for _, lan := range lans {
		if macMatch {
			for _, m := range macs(p.Quarantined) {
				w("-A %s -i %s -m mac --mac-source %s -j REJECT --reject-with icmp-admin-prohibited", chForward, lan, m)
			}
		}
		for _, wan := range p.WAN {
			if macMatch {
				for _, m := range macs(p.Offline) {
					w("-A %s -i %s -o %s -m mac --mac-source %s -j REJECT --reject-with icmp-admin-prohibited", chForward, lan, wan, m)
				}
			}
			if ipset {
				if len(p.OfflineIPs) > 0 {
					w("-A %s -i %s -o %s -m set --match-set %s src -j REJECT --reject-with icmp-admin-prohibited", chForward, lan, wan, setOff4)
				}
				if len(p.TorIPs) > 0 && macMatch && len(p.TorMACs) > 0 {
					w("-A %s -i %s -o %s -m set --match-set %s src -m set --match-set %s dst -j REJECT --reject-with icmp-admin-prohibited", chForward, lan, wan, setTorDev, setTor4)
				}
				if len(p.TorIPs) > 0 && len(p.TorSrcIPs) > 0 {
					w("-A %s -i %s -o %s -m set --match-set %s src -m set --match-set %s dst -j REJECT --reject-with icmp-admin-prohibited", chForward, lan, wan, setTorSrc, setTor4)
				}
			}
			if p.BlockBypass {
				if ipset {
					for _, proto := range []string{"tcp", "udp"} {
						w("-A %s -i %s -o %s -p %s --dport 443 -m set --match-set %s dst -j REJECT --reject-with icmp-admin-prohibited", chForward, lan, wan, proto, setDoH4)
					}
				}
				for _, proto := range []string{"tcp", "udp"} {
					w("-A %s -i %s -o %s -p %s --dport 853 -j REJECT --reject-with icmp-admin-prohibited", chForward, lan, wan, proto)
				}
			}
		}
	}
	if len(enabledForwards(p.PortForwards)) > 0 {
		for _, wan := range p.WAN {
			w("-A %s -i %s -m conntrack --ctstate DNAT -j ACCEPT", chForward, wan)
		}
	}
	for _, t := range p.Tunnels {
		w("-A %s -i %s -j ACCEPT", chForward, t.Iface)
		w("-A %s -o %s -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT", chForward, t.Iface)
		w("-A %s -i %s -j ACCEPT", chInput, t.Iface)
	}
	for _, wan := range p.WAN {
		w("-A %s -i %s -m conntrack --ctstate INVALID -j DROP", chInput, wan)
		w("-A %s -i %s -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT", chInput, wan)
		for _, t := range p.Tunnels {
			if t.Port > 0 {
				w("-A %s -i %s -p udp --dport %d -j ACCEPT", chInput, wan, t.Port)
			}
		}
		if ports := servicePorts(p.ServicePorts); ports != "" {
			ports = strings.ReplaceAll(ports, " ", "")
			w("-A %s -i %s -p tcp -m multiport --dports %s -j DROP", chInput, wan, ports)
			w("-A %s -i %s -p udp -m multiport --dports %s -j DROP", chInput, wan, ports)
		}
		if hashlimit {
			w("-A %s -i %s -p tcp --syn -m hashlimit --hashlimit-above %d/sec --hashlimit-burst %d --hashlimit-mode srcip --hashlimit-name fg_syn -j DROP",
				chInput, wan, p.SynPerSecond, p.SynPerSecond*2)
		} else {
			// no hashlimit so just cap syns overall at 20x
			w("-A %s -i %s -p tcp --syn -m limit --limit %d/sec --limit-burst %d -j RETURN", chInput, wan, p.SynPerSecond*20, p.SynPerSecond*40)
			w("-A %s -i %s -p tcp --syn -j DROP", chInput, wan)
		}
		w("-A %s -i %s -p icmp --icmp-type echo-request -m limit --limit %d/sec --limit-burst %d -j RETURN", chInput, wan, p.ICMPPerSecond, p.ICMPPerSecond*2)
		w("-A %s -i %s -p icmp --icmp-type echo-request -j DROP", chInput, wan)
	}
	for _, lan := range lans {
		if hashlimit {
			w("-A %s -i %s -p udp --dport 53 -m hashlimit --hashlimit-above %d/sec --hashlimit-burst %d --hashlimit-mode srcip --hashlimit-name fg_dns -j DROP",
				chInput, lan, p.DNSPerSecond, p.DNSPerSecond*4)
		} else {
			w("-A %s -i %s -p udp --dport 53 -m limit --limit %d/sec --limit-burst %d -j RETURN", chInput, lan, p.DNSPerSecond*10, p.DNSPerSecond*40)
			w("-A %s -i %s -p udp --dport 53 -j DROP", chInput, lan)
		}
	}
	w("COMMIT")
	return b.String()
}

func (t *IPTables) jumps() [][]string {
	return [][]string{
		{"-t", "nat", "PREROUTING", "-j", chDstnat},
		{"-t", "nat", "POSTROUTING", "-j", chSrcnat},
		{"-t", "filter", "FORWARD", "-j", chForward},
		{"-t", "filter", "INPUT", "-j", chInput},
	}
}

func (t *IPTables) Apply(rs string, p Params) error {
	t.probe()
	if t.ipset {
		if p.BlockBypass {
			if err := t.syncSet(setDoH4, "hash:net", "inet", splitV4(p.DoHIPs)); err != nil {
				return err
			}
		}
		if len(p.TorIPs) > 0 {
			if err := t.syncSet(setTor4, "hash:net", "inet", splitV4(p.TorIPs)); err != nil {
				return err
			}
		}
		if len(p.TorMACs) > 0 && t.mac {
			if err := t.syncSet(setTorDev, "hash:mac", "", macs(p.TorMACs)); err != nil {
				return err
			}
		}
		if len(p.TorSrcIPs) > 0 {
			if err := t.syncSet(setTorSrc, "hash:ip", "inet", splitV4(p.TorSrcIPs)); err != nil {
				return err
			}
		}
		if len(p.OfflineIPs) > 0 {
			if err := t.syncSet(setOff4, "hash:ip", "inet", splitV4(p.OfflineIPs)); err != nil {
				return err
			}
		}
	}
	if _, err := run(t.Netns, "iptables-restore", []byte(rs), "--noflush"); err != nil {
		return err
	}
	for _, j := range t.jumps() {
		table, chain, target := j[1], j[2], j[4]
		if _, err := run(t.Netns, "iptables", nil, "-t", table, "-C", chain, "-j", target); err != nil {
			if _, err := run(t.Netns, "iptables", nil, "-t", table, "-I", chain, "1", "-j", target); err != nil {
				return err
			}
		}
	}
	return nil
}

// fw3 flushes tables on reload so the manager reapplies when this is false
func (t *IPTables) Healthy() bool {
	for _, j := range t.jumps() {
		if _, err := run(t.Netns, "iptables", nil, "-t", j[1], "-C", j[2], "-j", j[4]); err != nil {
			return false
		}
	}
	return true
}

func (t *IPTables) Check(rs string) error {
	_, err := run(t.Netns, "iptables-restore", []byte(rs), "--noflush", "--test")
	return err
}

func (t *IPTables) Remove() error {
	t.probe()
	var first error
	for _, j := range t.jumps() {
		table, chain, target := j[1], j[2], j[4]
		for {
			if _, err := run(t.Netns, "iptables", nil, "-t", table, "-D", chain, "-j", target); err != nil {
				break
			}
		}
		if _, err := run(t.Netns, "iptables", nil, "-t", table, "-F", target); err == nil {
			run(t.Netns, "iptables", nil, "-t", table, "-X", target)
		} else if first == nil {
			first = err
		}
	}
	if t.ipset {
		for _, set := range []string{setDoH4, setTor4, setTorDev, setTorSrc, setOff4} {
			run(t.Netns, "ipset", nil, "destroy", set)
		}
	}
	return first
}

func (t *IPTables) syncSet(name, typ, family string, members []string) error {
	tmp := name + "_new"
	opts := "hashsize 4096 maxelem 65536"
	if family != "" {
		opts = "family " + family + " " + opts
	}
	var b strings.Builder
	fmt.Fprintf(&b, "create %s %s %s -exist\n", name, typ, opts)
	fmt.Fprintf(&b, "create %s %s %s -exist\n", tmp, typ, opts)
	fmt.Fprintf(&b, "flush %s\n", tmp)
	for _, m := range members {
		fmt.Fprintf(&b, "add %s %s -exist\n", tmp, m)
	}
	fmt.Fprintf(&b, "swap %s %s\n", tmp, name)
	fmt.Fprintf(&b, "destroy %s\n", tmp)
	_, err := run(t.Netns, "ipset", []byte(b.String()), "restore")
	return err
}

func splitV4(in []string) []string {
	v4, _ := splitIPs(in)
	return v4
}
