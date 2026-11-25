package firewall

import (
	"strings"
	"testing"
)

// gl.inet's vpn servers: dns, block page and bypass blocking like the lan, but nothing that
// would let their clients past the router's own zone rules
func TestVPNServers(t *testing.T) {
	for _, bypass := range []bool{true, false} {
		p := sample()
		p.BlockBypass = bypass
		p.VPNServers = []string{"wgserver"}
		nft, err := Render(p)
		if err != nil {
			t.Fatal(err)
		}
		ipt := renderIPTables(p, true, true, true, nil)
		for _, want := range []string{
			`ip daddr 192.168.8.2 tcp dport 80 dnat ip to 192.168.8.2:8080`,
			"udp dport 53 update @dns4",
		} {
			if !strings.Contains(nft, want) {
				t.Errorf("bypass %v: nft missing %q", bypass, want)
			}
		}
		if !strings.Contains(nft, `iifname { "br-lan", "wgserver" }`) {
			t.Errorf("bypass %v: wgserver isn't on the lan side in nft", bypass)
		}
		if !bypass && !strings.Contains(nft, `iifname "wgserver" meta nfproto ipv4 meta l4proto { tcp, udp } th dport 53 ip daddr != 192.168.8.1 dnat ip to 192.168.8.1:53`) {
			t.Error("dns to the tunnel address isn't sent to fengard with bypass blocking off")
		}
		for _, want := range []string{
			"-i wgserver -p udp --dport 53 ! -d 192.168.8.1 -j DNAT --to-destination 192.168.8.1:53",
			"-i wgserver -d 192.168.8.2 -p tcp --dport 80 -j DNAT --to-destination 192.168.8.2:8080",
		} {
			if !strings.Contains(ipt, want) {
				t.Errorf("bypass %v: iptables missing %q", bypass, want)
			}
		}
		// an accept here would skip gl.inet's own rules on iptables, like its lan access switch
		if strings.Contains(nft, `iifname "wgserver" accept`) || strings.Contains(ipt, "-i wgserver -j ACCEPT") {
			t.Errorf("bypass %v: vpn server traffic accepted past the router's firewall", bypass)
		}
		if strings.Contains(nft, `oifname "wgserver"`) || strings.Contains(ipt, "-o wgserver") {
			t.Errorf("bypass %v: vpn server treated as wan", bypass)
		}
	}
}
