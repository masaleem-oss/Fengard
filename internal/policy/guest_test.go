package policy

import (
	"slices"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/config"
)

const (
	guestMAC   = "aa:aa:aa:aa:aa:10"
	deniedMAC  = "aa:aa:aa:aa:aa:11"
	walkinMAC  = "aa:aa:aa:aa:aa:12"
	approvedGu = "aa:aa:aa:aa:aa:13"
)

// guests wait for the admin even with quarantine new devices off
func TestGuestNeedsApproval(t *testing.T) {
	e := setup(t, func(c *config.Config) {
		c.Settings.QuarantineNew = false
		c.Devices = append(c.Devices,
			config.Device{MAC: guestMAC, Group: "default", Guest: true},
			config.Device{MAC: deniedMAC, Group: "default", Guest: true, Denied: true},
			config.Device{MAC: approvedGu, Group: "default", Guest: true, Approved: true},
		)
	})
	e.SetGuests([]string{guestMAC, deniedMAC, walkinMAC, approvedGu})
	cases := map[string]Action{
		guestMAC:   Quarantine, // recorded, not approved yet
		deniedMAC:  Quarantine,
		walkinMAC:  Quarantine, // on the guest wi-fi before fengard wrote it down
		approvedGu: Allow,
		newMAC:     Allow, // a new device on the main lan, quarantine is off
		kidMAC:     Allow,
	}
	for mac, want := range cases {
		if got := e.Decide(mac, "example.com", monday10am).Action; got != want {
			t.Errorf("%s: got %v, want %v", mac, got, want)
		}
	}
	q, _ := e.Offline(monday10am)
	if !slices.Contains(q, guestMAC) || !slices.Contains(q, deniedMAC) || slices.Contains(q, approvedGu) {
		t.Errorf("quarantined %v", q)
	}

	// approving keeps it, kicking takes it away again
	e = setup(t, func(c *config.Config) {
		c.Devices = append(c.Devices, config.Device{MAC: guestMAC, Group: "default", Guest: true, Approved: true})
	})
	e.SetGuests([]string{guestMAC})
	if got := e.Decide(guestMAC, "example.com", monday10am).Action; got != Allow {
		t.Errorf("approved guest: %v", got)
	}
	e = setup(t, func(c *config.Config) {
		c.Devices = append(c.Devices, config.Device{MAC: guestMAC, Group: "default", Guest: true, Denied: true})
	})
	if got := e.Decide(guestMAC, "example.com", monday10am).Action; got != Quarantine {
		t.Errorf("kicked guest: %v", got)
	}
	// the router itself never counts as a guest
	e.SetGuests([]string{""})
	if got := e.Decide("", "example.com", monday10am).Action; got != Allow {
		t.Errorf("router: %v", got)
	}

	// one that just turned up is held back from its very first lookup
	e = setup(t, func(c *config.Config) { c.Settings.QuarantineNew = false })
	if got := e.Decide(walkinMAC, "example.com", monday10am).Action; got != Allow {
		t.Errorf("not on the guest wi-fi yet: %v", got)
	}
	e.AddGuest(walkinMAC)
	e.AddGuest(walkinMAC)
	if got := e.Decide(walkinMAC, "example.com", monday10am).Action; got != Quarantine {
		t.Errorf("walk-in after AddGuest: %v", got)
	}
}
