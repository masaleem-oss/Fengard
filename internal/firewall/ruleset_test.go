package firewall

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/config"
)

func sample() Params {
	return Params{
		LAN: []string{"br-lan"}, WAN: []string{"wan"},
		RouterIPv4: "192.168.8.1", RouterIPv6: "fd00::1",
		BlockIP: "192.168.8.2", HTTPPort: 8080, HTTPSPort: 8443,
		ServicePorts: []int{53, 80, 443, 8080, 8443},
		BlockBypass:  true,
		DoHIPs:       []string{"1.1.1.1", "1.0.0.0/24", "1.0.0.1", "2606:4700:4700::1111"},
		PortForwards: []config.PortForward{
			{Name: "Minecraft", Proto: "tcp", ExtPort: 25565, DestIP: "192.168.8.50", DestPort: 25565, Enabled: true},
			{Name: "Voice", Proto: "both", ExtPort: 9987, DestIP: "192.168.8.51", DestPort: 9987, Enabled: true},
			{Name: "Off", Proto: "tcp", ExtPort: 22, DestIP: "192.168.8.52", DestPort: 22, Enabled: false},
		},
		Quarantined: []string{"AA:BB:CC:DD:EE:01"},
		Offline:     []string{"aa:bb:cc:dd:ee:02", "bogus"},
	}
}

func TestRenderContents(t *testing.T) {
	rs, err := Render(sample())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"delete table inet fengard",
		`dnat ip to 192.168.8.1:53`,
		`dnat ip6 to [fd00::1]:53`,
		`tcp dport 25565 dnat ip to 192.168.8.50:25565`,
		`udp dport 9987 dnat ip to 192.168.8.51:9987`,
		"elements = { aa:bb:cc:dd:ee:01 }",
		"elements = { aa:bb:cc:dd:ee:02 }",
		"th dport 853",
		"th dport { 53, 80, 443, 8080, 8443 } drop",
		`ip daddr 192.168.8.2 tcp dport 80 dnat ip to 192.168.8.2:8080`,
		`ip daddr 192.168.8.2 tcp dport 443 dnat ip to 192.168.8.2:8443`,
		"update @syn4",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset missing %q", want)
		}
	}
	if strings.Contains(rs, "dport 22 dnat") {
		t.Error("disabled port forward was rendered")
	}
	if strings.Contains(rs, "bogus") {
		t.Error("invalid MAC was rendered")
	}
}

func TestRenderWithoutBypassBlocking(t *testing.T) {
	p := sample()
	p.BlockBypass = false
	rs, _ := Render(p)
	if strings.Contains(rs, "dport 53 ip daddr") || strings.Contains(rs, "@doh4 meta") {
		t.Error("bypass rules rendered while disabled")
	}
}

// needs root on linux runs in a throwaway netns
func TestNftAccepts(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs root on Linux")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
	ns := "fengard-test"
	exec.Command("ip", "netns", "del", ns).Run()
	if out, err := exec.Command("ip", "netns", "add", ns).CombinedOutput(); err != nil {
		t.Skipf("can't create netns: %s", out)
	}
	defer exec.Command("ip", "netns", "del", ns).Run()

	m := &Manager{Enabled: true, Netns: ns, Inputs: func() (Params, error) { return sample(), nil }}
	if err := m.Sync(); err != nil {
		t.Fatalf("apply: %v\n%s", err, m.Status().Ruleset)
	}
	p := sample()
	p.Offline = nil
	m.Inputs = func() (Params, error) { return p, nil }
	if err := m.Sync(); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	out, _ := exec.Command("ip", "netns", "exec", ns, "nft", "list", "set", "inet", "fengard", "offline").CombinedOutput()
	if strings.Contains(string(out), "aa:bb:cc:dd:ee:02") {
		t.Errorf("re-apply did not replace the offline set:\n%s", out)
	}
	if err := m.Remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}
}
