package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/identity"
	"github.com/masaleem-oss/Fengard/internal/store"
)

// a new device on the guest wi-fi is recorded as a guest waiting for approval, even with
// quarantine new devices off, and a new one on the lan isn't
func TestRecordGuests(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cs, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cs.Update("test", "guest on", func(c *config.Config) error {
		c.Settings.QuarantineNew = false
		c.Groups = append(c.Groups, config.Group{ID: "guests", Name: "Guests"})
		c.Guest = config.Guest{Enabled: true, SSID: "Guests", Password: "k7pq-m3zt-x9wd-4fhn", Group: "guests"}
		return nil
	})
	al := alerts.New(must(db.Log("alerts")))
	rec := &identity.Recognizer{Habits: identity.NewHabits(must(db.KV("habits"))), Prints: identity.NewPrints(must(db.KV("prints"))),
		Config: cs.Get, Seen: func() map[string]devices.Seen { return nil }, KV: must(db.KV("recognize"))}
	isGuest := func(ip string) bool { return strings.HasPrefix(ip, "192.168.9.") }

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan devices.Info, 4)
	done := make(chan struct{})
	go func() { recordNewDevices(ctx, ch, cs, al, rec, isGuest); close(done) }()
	ch <- devices.Info{MAC: "aa:bb:cc:00:00:01", IP: "192.168.9.120", Hostname: "Pixel-8"}
	ch <- devices.Info{MAC: "aa:bb:cc:00:00:02", IP: "192.168.8.120", Hostname: "laptop"}
	deadline := time.Now().Add(5 * time.Second)
	for len(cs.Get().Devices) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	c := cs.Get()
	g, l := c.Device("aa:bb:cc:00:00:01"), c.Device("aa:bb:cc:00:00:02")
	if g == nil || !g.Guest || g.Approved || g.Group != "guests" {
		t.Errorf("guest %+v", g)
	}
	if l == nil || l.Guest || !l.Approved {
		t.Errorf("lan device %+v", l)
	}
	if ok := approvedMACs(c); slices.Contains(ok, g.MAC) || !slices.Contains(ok, l.MAC) {
		t.Errorf("approved %v", ok)
	}
	found := false
	for _, a := range al.List(10, time.Time{}) {
		if a.MAC == g.MAC && strings.Contains(a.Title, "Guest waiting") && a.Severity == alerts.Warning {
			found = true
		}
	}
	if !found {
		t.Errorf("no guest alert: %+v", al.List(10, time.Time{}))
	}
}

func TestGuestMACs(t *testing.T) {
	seen := map[string]devices.Seen{
		"aa:00:00:00:00:01": {IPs: []string{"192.168.8.20"}},
		"aa:00:00:00:00:02": {IPs: []string{"fe80::1", "192.168.9.130"}},
		"aa:00:00:00:00:03": {},
	}
	got := guestMACs(seen, func(ip string) bool { return strings.HasPrefix(ip, "192.168.9.") })
	if !slices.Equal(got, []string{"aa:00:00:00:00:02"}) {
		t.Errorf("got %v", got)
	}
}
