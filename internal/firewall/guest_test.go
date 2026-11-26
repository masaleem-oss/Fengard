package firewall

import (
	"strings"
	"testing"
)

// guest wi-fi: dns and the approval page like the lan, internet only for approved macs, and
// nothing on iptables that accepts more than the approval page
func TestGuests(t *testing.T) {
	for _, bypass := range []bool{true, false} {
		p := sample()
		p.BlockBypass = bypass
		p.Guests = []string{"br-fgguest"}
		p.GuestOK = []string{"AA:BB:CC:DD:EE:10", "bogus", "aa:bb:cc:dd:ee:11"}
		nft, err := Render(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`elements = { aa:bb:cc:dd:ee:10, aa:bb:cc:dd:ee:11 }`,
			`iifname { "br-fgguest" } ether saddr != @guest_ok counter reject with icmpx admin-prohibited`,
			`iifname { "br-fgguest" } ip daddr 192.168.8.2 tcp dport { 8080, 8443 } accept`,
			`iifname { "br-lan", "br-fgguest" } ip daddr 192.168.8.2 tcp dport 80 dnat ip to 192.168.8.2:8080`,
			`iifname { "br-lan", "br-fgguest" } meta nfproto ipv4 udp dport 53 update @dns4`,
		} {
			if !strings.Contains(nft, want) {
				t.Errorf("bypass %v: nft missing %q", bypass, want)
			}
		}
		if !bypass && !strings.Contains(nft, `iifname "br-fgguest" meta nfproto ipv4 meta l4proto { tcp, udp } th dport 53 ip daddr != 192.168.8.1 dnat ip to 192.168.8.1:53`) {
			t.Error("guest dns to the guest router address isn't sent to fengard with bypass blocking off")
		}

		ipt := renderIPTables(p, true, true, true, nil)
		for _, want := range []string{
			"-A FENGARD_DSTNAT -i br-fgguest -p udp --dport 53 ! -d 192.168.8.1 -j DNAT --to-destination 192.168.8.1:53",
			"-A FENGARD_DSTNAT -i br-fgguest -d 192.168.8.2 -p tcp --dport 80 -j DNAT --to-destination 192.168.8.2:8080",
			"-A FENGARD_FORWARD -i br-fgguest -m mac --mac-source aa:bb:cc:dd:ee:10 -j RETURN",
			"-A FENGARD_FORWARD -i br-fgguest -j REJECT --reject-with icmp-admin-prohibited",
			"-A FENGARD_INPUT -i br-fgguest -d 192.168.8.2 -p tcp -m multiport --dports 8080,8443 -j ACCEPT",
		} {
			if !strings.Contains(ipt, want) {
				t.Errorf("bypass %v: iptables missing %q", bypass, want)
			}
		}
		// the reject comes after every approved mac, and nothing after it could undo it
		fwd := lines(ipt, "-A FENGARD_FORWARD")
		if n := len(fwd); n < 3 || !strings.HasSuffix(fwd[n-3], "aa:bb:cc:dd:ee:10 -j RETURN") || !strings.HasSuffix(fwd[n-2], "aa:bb:cc:dd:ee:11 -j RETURN") ||
			fwd[n-1] != "-A FENGARD_FORWARD -i br-fgguest -j REJECT --reject-with icmp-admin-prohibited" {
			t.Errorf("bypass %v: guest forward rules aren't last: %q", bypass, fwd)
		}
		for _, l := range strings.Split(ipt, "\n") {
			if strings.Contains(l, "-i br-fgguest") && strings.HasSuffix(l, "-j ACCEPT") && !strings.Contains(l, "-d 192.168.8.2 -p tcp -m multiport --dports 8080,8443") {
				t.Errorf("bypass %v: broad accept for guests: %q", bypass, l)
			}
		}
		if strings.Contains(nft, `oifname "br-fgguest"`) || strings.Contains(ipt, "-o br-fgguest") {
			t.Errorf("bypass %v: guest bridge treated as wan", bypass)
		}
	}

	// no mac match means no telling guests apart, so none get out
	p := sample()
	p.Guests, p.GuestOK = []string{"br-guest"}, []string{"aa:bb:cc:dd:ee:10"}
	ipt := renderIPTables(p, true, true, false, nil)
	if strings.Contains(ipt, "--mac-source aa:bb:cc:dd:ee:10 -j RETURN") || !strings.Contains(ipt, "-A FENGARD_FORWARD -i br-guest -j REJECT") {
		t.Error("guests let out without a mac match")
	}

	// the guest bridge passed in -lan too only shows up once
	p = sample()
	p.LAN = []string{"br-lan", "br-guest"}
	p.Guests = []string{"br-guest"}
	nft, _ := Render(p)
	if strings.Contains(nft, `"br-guest", "br-guest"`) {
		t.Error("guest bridge listed twice")
	}
	// guest wi-fi off leaves the ruleset as it was
	off, _ := Render(sample())
	if strings.Contains(off, "guest") {
		t.Error("guest rules without a guest network")
	}
}

func lines(s, prefix string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}
