package main

import (
	"strings"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/alerts"
)

func TestHTTP3DownAlert(t *testing.T) {
	q9 := "https://dns.quad9.net/dns-query"
	// http/3 to another resolver works, so the connection isn't blocking udp
	a := http3DownAlert(q9, "no reply from the server over UDP", "https://cloudflare-dns.com/dns-query")
	if a.Severity != alerts.Info || strings.Contains(a.Detail, "blocking") || !strings.Contains(a.Detail, "cloudflare-dns.com works") {
		t.Errorf("works elsewhere: %+v", a)
	}
	// nothing else to compare with and nothing came back over udp
	a = http3DownAlert(q9, "no reply from the server over UDP", "")
	if a.Severity != alerts.Warning || !strings.Contains(a.Detail, "blocking UDP port 443") {
		t.Errorf("no reply: %+v", a)
	}
	// udp got through, it was just slow
	a = http3DownAlert(q9, "server took too long to answer", "")
	if strings.Contains(a.Detail, "blocking") || a.Title != "dns.quad9.net is using HTTP/2, not HTTP/3" {
		t.Errorf("slow: %+v", a)
	}
}
