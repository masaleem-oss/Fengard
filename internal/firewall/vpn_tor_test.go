package firewall

import (
	"strings"
	"testing"
)

func torVPN() Params {
	p := sample()
	p.TorIPs = []string{"128.31.0.39", "2001:858:2:2:aabb:0:563b:1526"}
	p.TorMACs = []string{"aa:bb:cc:dd:ee:03"}
	p.TorSrcIPs = []string{"10.66.0.2"}
	p.OfflineIPs = []string{"10.66.0.3"}
	p.Tunnels = []VPNParams{{Iface: "fgwg0", Port: 51820, Subnet: "10.66.0.0/24"}, {Iface: "tailscale0", Subnet: "100.64.0.0/10"}}
	return p
}

func TestRenderTorAndVPN(t *testing.T) {
	rs, err := Render(torVPN())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"set tor4", "elements = { 128.31.0.39 }",
		"set tor6", "2001:858:2:2:aabb:0:563b:1526",
		"set tor_devs", "aa:bb:cc:dd:ee:03",
		"ether saddr @tor_devs ip daddr @tor4 counter reject",
		"ip saddr @tor_src4 ip daddr @tor4 counter reject",
		"ip saddr @offline4 counter reject",
		"chain srcnat", "ip saddr 10.66.0.0/24 masquerade",
		`iifname { "wan" } udp dport 51820 accept`,
		`iifname "fgwg0" accept`,
		`oifname "fgwg0" ct state established,related accept`,
		`iifname { "br-lan", "fgwg0", "tailscale0" }`,
		"ip saddr 100.64.0.0/10 masquerade",
		`iifname "tailscale0" accept`,
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("nft ruleset missing %q", want)
		}
	}
	plain, _ := Render(sample())
	if strings.Contains(plain, "srcnat") || strings.Contains(plain, "51820") {
		t.Error("VPN rules rendered without a VPN")
	}

	ipt := renderIPTables(torVPN(), true, true, true, nil)
	for _, want := range []string{
		":FENGARD_SRCNAT - [0:0]",
		"-A FENGARD_SRCNAT -s 10.66.0.0/24 -o wan -j MASQUERADE",
		"-m set --match-set fengard_tordev src -m set --match-set fengard_tor4 dst -j REJECT",
		"-m set --match-set fengard_torsrc src -m set --match-set fengard_tor4 dst -j REJECT",
		"-m set --match-set fengard_off4 src -j REJECT",
		"-A FENGARD_INPUT -i wan -p udp --dport 51820 -j ACCEPT",
		"-A FENGARD_FORWARD -i fgwg0 -j ACCEPT",
		"-A FENGARD_INPUT -i fgwg0 -j ACCEPT",
		"-i fgwg0 -p udp --dport 53 ! -d 192.168.8.1 -j DNAT --to-destination 192.168.8.1:53",
		"-i fgwg0 -d 192.168.8.2 -p tcp --dport 443 -j DNAT",
		"-A FENGARD_SRCNAT -s 100.64.0.0/10 -o wan -j MASQUERADE",
		"-i tailscale0 -p udp --dport 53 ! -d 192.168.8.1 -j DNAT",
		"-A FENGARD_SRCNAT -s 100.64.0.0/10 -o wan -j MASQUERADE",
		"-i tailscale0 -p udp --dport 53 ! -d 192.168.8.1 -j DNAT",
	} {
		if !strings.Contains(ipt, want) {
			t.Errorf("iptables ruleset missing %q", want)
		}
	}
	// no ipset so the rule just gets left out
	lite := renderIPTables(torVPN(), false, false, false, nil)
	if strings.Contains(lite, "fengard_tor") {
		t.Error("tor set rules rendered without ipset")
	}
}
