package welcome

import (
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
)

func TestOnlyNewUnnamedPhonesGetThePage(t *testing.T) {
	cs, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := New(cs, nil)
	since := time.Now()
	if g.Want("aa:00:00:00:00:01", "Sams-iPhone") {
		t.Fatal("shown while switched off")
	}
	cs.Update("test", "setup", func(c *config.Config) error {
		c.Settings.WelcomePage, c.Settings.WelcomeSince = true, since
		def := c.Settings.DefaultGroup
		c.Devices = append(c.Devices,
			config.Device{MAC: "aa:00:00:00:00:02", Hostname: "Old-iPhone", Group: def, FirstSeen: since.Add(-time.Hour)},
			config.Device{MAC: "aa:00:00:00:00:03", Hostname: "iPhone", Group: def, Name: "Mum", FirstSeen: since.Add(time.Minute)},
			config.Device{MAC: "aa:00:00:00:00:04", Hostname: "iPad", Group: "kids", FirstSeen: since.Add(time.Minute)},
			config.Device{MAC: "aa:00:00:00:00:05", Hostname: "Sams-iPhone", Group: def, FirstSeen: since.Add(time.Minute)})
		return nil
	})
	cases := map[string]struct {
		mac, host string
		want      bool
	}{
		"brand new iphone":             {"aa:00:00:00:00:01", "Sams-iPhone", true},
		"new ipad":                     {"aa:00:00:00:00:09", "iPad", true},
		"laptop":                       {"aa:00:00:00:00:06", "Sams-MacBook-Pro", false},
		"smart plug":                   {"aa:00:00:00:00:07", "tasmota-4F2A", false},
		"no name on a real address":    {"00:11:22:00:00:08", "", false},
		"no name on a private address": {"aa:00:00:00:00:0a", "", true},
		"there before switched on":     {"aa:00:00:00:00:02", "", false},
		"already named":                {"aa:00:00:00:00:03", "", false},
		"put in a profile":             {"aa:00:00:00:00:04", "", false},
		"saved new iphone":             {"aa:00:00:00:00:05", "", true},
	}
	for name, c := range cases {
		if got := g.Want(c.mac, c.host); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
	g.Accept("aa:00:00:00:00:01")
	if g.Want("aa:00:00:00:00:01", "Sams-iPhone") {
		t.Fatal("shown again after joining")
	}
}

func TestLetsThroughAfterTenMinutesWithoutJoining(t *testing.T) {
	cs, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cs.Update("test", "on", func(c *config.Config) error {
		c.Settings.WelcomePage, c.Settings.WelcomeSince = true, time.Now()
		return nil
	})
	g := New(cs, nil)
	now := time.Now()
	g.now = func() time.Time { return now }
	if !g.Want("aa:00:00:00:00:01", "") {
		t.Fatal("not shown")
	}
	now = now.Add(11 * time.Minute)
	if g.Want("aa:00:00:00:00:01", "") {
		t.Fatal("still shown after ten minutes")
	}
}
