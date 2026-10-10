package alerts

import (
	"fmt"
	"testing"
	"time"
)

func TestDedupeCapacityIsHardBound(t *testing.T) {
	a := New(nil)
	for i := range maxDedupeKeys + 100 {
		a.Raise(fmt.Sprint(i), time.Hour, Alert{Kind: "test"})
	}
	if len(a.last) != maxDedupeKeys || a.Dropped() != 0 {
		t.Fatalf("keys %d, dropped %d", len(a.last), a.Dropped())
	}
	if !a.Raise("intruder", time.Hour, Alert{Kind: "new_device"}) || len(a.last) != maxDedupeKeys {
		t.Fatal("a flood of keys hid a new alert")
	}
	if !a.Raise("0", 0, Alert{Kind: "critical", Severity: Critical}) {
		t.Fatal("existing important key suppressed")
	}
	for k := range a.last {
		a.last[k] = time.Now().Add(-25 * time.Hour)
	}
	a.sweep = time.Time{}
	if !a.Raise("new", time.Hour, Alert{Kind: "test"}) {
		t.Fatal("expired keys not reclaimed")
	}
}

func TestAccessRequestsHaveGlobalBudget(t *testing.T) {
	a := New(nil)
	successes := 0
	for i := range 100 {
		if a.Raise(fmt.Sprint(i), time.Hour, Alert{Kind: "access_request"}) {
			successes++
		}
	}
	if successes > 31 || a.Dropped() < 69 {
		t.Fatalf("global budget accepted %d requests", successes)
	}
	if !a.Raise("dns-flood", 0, Alert{Kind: "dns_flood", Severity: Critical}) {
		t.Fatal("requests exhausted critical alert budget")
	}
}

func TestRequestDedupeLeavesCapacityForCriticalAlerts(t *testing.T) {
	a := New(nil)
	a.requestLimit.SetRate(100000, 20000)
	for i := range maxDedupeKeys {
		a.Raise(fmt.Sprint(i), time.Hour, Alert{Kind: "access_request"})
	}
	if len(a.last) != maxDedupeKeys-128 {
		t.Fatalf("request keys %d", len(a.last))
	}
	if !a.Raise("critical-new", 0, Alert{Kind: "dns_flood", Severity: Critical}) {
		t.Fatal("requests exhausted critical capacity")
	}
}
