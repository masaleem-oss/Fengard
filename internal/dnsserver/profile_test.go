package dnsserver

import (
	"testing"

	"github.com/miekg/dns"

	"github.com/masaleem-oss/Fengard/internal/config"
)

// a profile with its own resolver, like a family dns for kids, asks only that one and keeps its
// answers apart from everyone else's
func TestProfileUpstreams(t *testing.T) {
	normal, family := startUpstream(t), startUpstream(t)
	s, addr := startServer(t, normal, nil)
	own, _ := config.ParseUpstream(family.addr)
	s.SetProfileUpstreams(map[string][]config.Upstream{"default": {own}})
	if r, err := query(addr, "site.example", dns.TypeA); err != nil || firstA(r) == "" {
		t.Fatalf("lookup: %v %v", r, err)
	}
	if family.hits.Load() != 1 || normal.hits.Load() != 0 {
		t.Fatalf("family %d normal %d", family.hits.Load(), normal.hits.Load())
	}
	// back on the normal resolvers the family answer isn't reused
	s.SetProfileUpstreams(nil)
	query(addr, "site.example", dns.TypeA)
	if normal.hits.Load() != 1 {
		t.Fatalf("normal resolver asked %d times", normal.hits.Load())
	}
}
