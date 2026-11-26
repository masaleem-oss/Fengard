package main

import (
	"context"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/guest"
	"github.com/masaleem-oss/Fengard/internal/policy"
)

// macs the admin let online. a household device that joins the guest wi-fi is one of them too
func approvedMACs(c *config.Config) []string {
	var out []string
	for _, d := range c.Devices {
		if d.Approved && !d.Denied {
			out = append(out, d.MAC)
		}
	}
	return out
}

// guestMACs is every mac with an address on the guest network
func guestMACs(seen map[string]devices.Seen, isGuest func(ip string) bool) []string {
	var out []string
	for mac, s := range seen {
		for _, ip := range s.IPs {
			if isGuest(ip) {
				out = append(out, mac)
				break
			}
		}
	}
	return out
}

// keeps the policy's list of macs on the guest wi-fi current, new ones are added the moment
// they show up and this drops the ones that left
func watchGuests(ctx context.Context, t *devices.Tracker, e *policy.Engine, g *guest.Manager) {
	tk := time.NewTicker(15 * time.Second)
	defer tk.Stop()
	for {
		e.SetGuests(guestMACs(t.Seen(), g.IsGuest))
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}
