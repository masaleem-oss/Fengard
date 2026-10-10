package traffic

import (
	"strings"
	"testing"
	"time"
)

const sample1 = `ipv4     2 tcp      6 117 ESTABLISHED src=192.168.8.155 dst=104.20.23.154 sport=50000 dport=443 packets=10 bytes=1000 src=104.20.23.154 dst=10.0.150.193 sport=443 dport=50000 packets=20 bytes=50000 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 169 src=192.168.8.196 dst=157.240.8.1 sport=65513 dport=443 packets=427 bytes=2000 src=157.240.8.1 dst=10.0.150.193 sport=443 dport=65513 packets=292 bytes=9000 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 57 src=192.168.8.155 dst=192.168.8.1 sport=38063 dport=53 packets=2 bytes=114 src=192.168.8.1 dst=192.168.8.155 sport=53 dport=38063 packets=2 bytes=141 mark=0 zone=0 use=2
`

const sample2 = `ipv4     2 tcp      6 117 ESTABLISHED src=192.168.8.155 dst=104.20.23.154 sport=50000 dport=443 packets=10 bytes=4000 src=104.20.23.154 dst=10.0.150.193 sport=443 dport=50000 packets=20 bytes=350000 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 169 src=192.168.8.196 dst=157.240.8.1 sport=65513 dport=443 packets=427 bytes=2000 src=157.240.8.1 dst=10.0.150.193 sport=443 dport=65513 packets=292 bytes=9000 [ASSURED] mark=0 zone=0 use=2
ipv4     2 tcp      6 117 ESTABLISHED src=192.168.8.196 dst=142.250.1.1 sport=50001 dport=443 packets=10 bytes=500 src=142.250.1.1 dst=10.0.150.193 sport=443 dport=50001 packets=20 bytes=7000 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 57 src=192.168.8.155 dst=192.168.8.1 sport=38063 dport=53 packets=9 bytes=99999 src=192.168.8.1 dst=192.168.8.155 sport=53 dport=38063 packets=9 bytes=99999 mark=0 zone=0 use=2
`

func TestCountsInternetTrafficPerDevice(t *testing.T) {
	macs := map[string]string{"192.168.8.155": "pc", "192.168.8.196": "phone"}
	m := &Meter{Lookup: func(ip string) (string, bool) { mac, ok := macs[ip]; return mac, ok }}
	now := time.Now()
	m.SampleFrom(strings.NewReader(sample1), now)
	// the first look only learns whats already open
	if s := m.Snapshot(); s.Today.Down != 0 {
		t.Fatalf("counted traffic from before fengard was watching: %+v", s.Today)
	}
	m.SampleFrom(strings.NewReader(sample2), now.Add(3*time.Second))
	s := m.Snapshot()
	byMAC := map[string]Device{}
	for _, d := range s.Devices {
		byMAC[d.MAC] = d
	}
	pc, phone := byMAC["pc"], byMAC["phone"]
	if pc.Today.Down != 300000 || pc.Today.Up != 3000 {
		t.Fatalf("pc %+v", pc.Today)
	}
	// a connection that showed up between looks counts in full
	if phone.Today.Down != 7000 || phone.Today.Up != 500 {
		t.Fatalf("phone %+v", phone.Today)
	}
	if pc.DownRate != 100000 {
		t.Fatalf("pc rate %v", pc.DownRate)
	}
	if s.Devices[0].MAC != "pc" {
		t.Fatal("busiest device not first")
	}
}

func TestDnsToTheRouterIsntInternetTraffic(t *testing.T) {
	m := &Meter{Lookup: func(string) (string, bool) { return "pc", true }}
	line := `ipv4     2 udp      17 57 src=192.168.8.155 dst=192.168.8.1 sport=1 dport=53 packets=2 bytes=100 src=192.168.8.1 dst=192.168.8.155 sport=53 dport=1 packets=2 bytes=100 mark=0 zone=0 use=2` + "\n"
	m.SampleFrom(strings.NewReader(""), time.Now())
	m.SampleFrom(strings.NewReader(line), time.Now())
	if s := m.Snapshot(); s.Today.Down+s.Today.Up != 0 {
		t.Fatalf("counted lan traffic: %+v", s.Today)
	}
}
