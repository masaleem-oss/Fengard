package devices

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLeases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases")
	os.WriteFile(path, []byte(
		"1760000000 AA:BB:CC:DD:EE:01 192.168.8.20 kids-ipad 01:aa\n"+
			"1760000000 aa:bb:cc:dd:ee:02 192.168.8.21 * *\n"+
			"garbage line\n"), 0o600)
	got := ReadLeases(path)
	if len(got) != 2 || got[0].MAC != "aa:bb:cc:dd:ee:01" || got[0].Hostname != "kids-ipad" || got[1].Hostname != "" {
		t.Fatalf("ReadLeases = %+v", got)
	}
}

func TestParseNeighbors(t *testing.T) {
	out := []byte(`192.168.8.20 dev br-lan lladdr aa:bb:cc:dd:ee:01 REACHABLE
fe80::1c2d dev br-lan lladdr aa:bb:cc:dd:ee:01 STALE
192.168.8.99 dev br-lan  FAILED
192.168.8.30 dev br-lan lladdr aa:bb:cc:dd:ee:03 FAILED
`)
	got := ParseNeighbors(out)
	if len(got) != 2 || got[1].IP != "fe80::1c2d" {
		t.Fatalf("ParseNeighbors = %+v", got)
	}
}

func TestParseARP(t *testing.T) {
	windows := []byte("\r\nInterface: 192.168.1.10 --- 0x5\r\n  Internet Address      Physical Address      Type\r\n" +
		"  192.168.1.1           aa-bb-cc-dd-ee-01     dynamic   \r\n" +
		"  192.168.1.255         ff-ff-ff-ff-ff-ff     static    \r\n" +
		"  224.0.0.22            01-00-5e-00-00-16     static    \r\n")
	got := ParseARP(windows)
	if len(got) != 1 || got[0].IP != "192.168.1.1" || got[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("ParseARP(windows) = %+v", got)
	}
	mac := []byte(`? (192.168.1.1) at a:bb:cc:d:ee:2 on en0 ifscope [ethernet]
? (192.168.1.7) at (incomplete) on en0 ifscope [ethernet]
? (192.168.1.255) at ff:ff:ff:ff:ff:ff on en0 ifscope [ethernet]
`)
	got = ParseARP(mac)
	if len(got) != 1 || got[0].MAC != "0a:bb:cc:0d:ee:02" {
		t.Fatalf("ParseARP(macOS) = %+v", got)
	}
}

func TestNewDeviceCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases")
	os.WriteFile(path, []byte("0 aa:bb:cc:dd:ee:01 127.0.0.1 laptop *\n0 aa:bb:cc:dd:ee:02 127.0.0.2 known *\n"), 0o600)
	tr := NewTracker(path)
	tr.Neighbors = nil // only the lease file not this machines arp table
	tr.Known([]string{"aa:bb:cc:dd:ee:02"})
	var fresh []string
	tr.OnNew = func(mac, host, ip string) { fresh = append(fresh, mac+"/"+host) }
	info, ok := tr.Lookup("127.0.0.1")
	if !ok || info.MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("Lookup = %+v %v", info, ok)
	}
	if len(fresh) != 1 || fresh[0] != "aa:bb:cc:dd:ee:01/laptop" {
		t.Fatalf("new devices = %v", fresh)
	}
}

// the upstream gateway on the wan side showed up under Devices and got the Everyone profile
func TestNeighborsOnlyFromTheLAN(t *testing.T) {
	out := []byte(`192.168.8.110 dev br-lan lladdr 2e:f4:80:08:8a:1a REACHABLE
10.0.150.1 dev apclix0 lladdr 00:11:22:33:44:55 REACHABLE
192.168.9.20 dev br-guest lladdr 66:77:88:99:aa:bb STALE
192.168.8.2 dev br-lan FAILED
`)
	got := ParseNeighborsOn(out, []string{"br-lan", "br-guest"})
	if len(got) != 2 || got[0].IP != "192.168.8.110" || got[1].IP != "192.168.9.20" {
		t.Fatalf("got %+v", got)
	}
	if all := ParseNeighborsOn(out, nil); len(all) != 3 {
		t.Fatalf("no interfaces given should keep every neighbor, got %+v", all)
	}
}
