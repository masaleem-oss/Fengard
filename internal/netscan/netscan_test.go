package netscan

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFindsOpenRiskyPortsAndAlertsOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	open := ln.Addr().(*net.TCPAddr).Port
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	shut := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	leases := filepath.Join(t.TempDir(), "leases")
	os.WriteFile(leases, []byte("TCP:8080:127.0.0.1:80:1791641764:camera remote view\nbroken line\n"), 0o644)
	var alerts []Finding
	s := &Scanner{
		Targets:    func() []Target { return []Target{{MAC: "tv", IP: "127.0.0.1"}} },
		Ports:      []Finding{{Port: open, Severity: High, Title: "open"}, {Port: shut, Severity: High, Title: "shut"}},
		UPnPLeases: []string{leases},
		OnFinding:  func(_ string, f Finding) { alerts = append(alerts, f) },
	}
	r := s.Scan(context.Background())
	if len(r.Devices) != 1 || len(r.Devices[0].Findings) != 2 {
		t.Fatalf("report %+v", r)
	}
	got := map[string]bool{}
	for _, f := range r.Devices[0].Findings {
		got[strconv.Itoa(f.Port)] = true
	}
	if !got[strconv.Itoa(open)] || !got["8080"] || got[strconv.Itoa(shut)] {
		t.Fatalf("findings %+v", r.Devices[0].Findings)
	}
	if len(alerts) != 2 {
		t.Fatalf("alerts %+v", alerts)
	}
	s.Scan(context.Background())
	if len(alerts) != 2 {
		t.Fatal("the same finding alerted twice")
	}
}
