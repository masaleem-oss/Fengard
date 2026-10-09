package firewall

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestRenderIPTables(t *testing.T) {
	rs := renderIPTables(sample(), true, true, true, nil)
	for _, want := range []string{
		"*nat", ":FENGARD_DSTNAT - [0:0]",
		"-i br-lan -p udp --dport 53 ! -d 192.168.8.1 -j DNAT --to-destination 192.168.8.1:53",
		"-i wan -p tcp --dport 25565 -j DNAT --to-destination 192.168.8.50:25565",
		"-i wan -p udp --dport 9987 -j DNAT --to-destination 192.168.8.51:9987",
		"-m mac --mac-source aa:bb:cc:dd:ee:01 -j REJECT",
		"-o wan -m mac --mac-source aa:bb:cc:dd:ee:02 -j REJECT",
		"--match-set fengard_doh4 dst",
		"--dport 853 -j REJECT",
		"-m multiport --dports 53,80,443,8080,8443 -j DROP",
		"-i br-lan -d 192.168.8.2 -p tcp --dport 80 -j DNAT --to-destination 192.168.8.2:8080",
		"--hashlimit-name fg_syn",
		"--hashlimit-name fg_dns",
		"COMMIT",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("iptables ruleset missing %q", want)
		}
	}
	if strings.Contains(rs, "--dport 22 -j DNAT") || strings.Contains(rs, "bogus") {
		t.Error("disabled forward or invalid MAC rendered")
	}
	lite := renderIPTables(sample(), false, false, false, []string{"no ipset"})
	if strings.Contains(lite, "match-set") || strings.Contains(lite, "hashlimit") || strings.Contains(lite, "--mac-source") {
		t.Error("optional-module rules rendered without the modules")
	}
	if !strings.Contains(lite, "# note: no ipset") {
		t.Error("missing note about degraded rules")
	}
}

// needs root on linux runs in a throwaway netns
func TestIPTablesApplies(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs root on Linux")
	}
	if _, err := exec.LookPath("iptables-restore"); err != nil {
		t.Skip("iptables not installed")
	}
	ns := "fengard-ipt-test"
	exec.Command("ip", "netns", "del", ns).Run()
	if out, err := exec.Command("ip", "netns", "add", ns).CombinedOutput(); err != nil {
		t.Skipf("can't create netns: %s", out)
	}
	defer exec.Command("ip", "netns", "del", ns).Run()

	m := &Manager{Enabled: true, Netns: ns, Backend: NewIPTables(ns), Inputs: func() (Params, error) { return sample(), nil }}
	if err := m.Sync(); err != nil {
		t.Fatalf("apply: %v\n%s", err, m.Status().Ruleset)
	}
	out, _ := exec.Command("ip", "netns", "exec", ns, "iptables", "-S", "FORWARD").CombinedOutput()
	if !strings.Contains(string(out), "-j FENGARD_FORWARD") {
		t.Errorf("jump not installed:\n%s", out)
	}
	// reapply shouldnt duplicate the jump
	m.lastSum = [32]byte{}
	if err := m.Sync(); err != nil {
		t.Fatal(err)
	}
	out, _ = exec.Command("ip", "netns", "exec", ns, "iptables", "-S", "FORWARD").CombinedOutput()
	if strings.Count(string(out), "-j FENGARD_FORWARD") != 1 {
		t.Errorf("jump duplicated:\n%s", out)
	}
	if err := m.Remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out, _ = exec.Command("ip", "netns", "exec", ns, "iptables", "-S").CombinedOutput()
	if strings.Contains(string(out), "FENGARD") {
		t.Errorf("rules left behind:\n%s", out)
	}
}
