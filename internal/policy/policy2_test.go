package policy

import (
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/config"
)

func TestAppsListsTempAndPause(t *testing.T) {
	c := config.Default()
	c.Settings.Timezone = "UTC"
	c.Devices = []config.Device{{MAC: kidMAC, Name: "Kid tablet", Group: "kids", Approved: true}}
	c.Groups[1].Apps = []string{"tiktok", "roblox"}
	c.Lists = []config.CustomList{
		{ID: "l1", Name: "Crypto miners", URL: "https://example.com/a.txt", Enabled: true},
		{ID: "l2", Name: "Disabled list", URL: "https://example.com/b.txt", Enabled: false},
	}
	// validate prunes against the real clock so times are relative to now
	now := time.Now()
	c.TempAllow = []config.TempAllow{
		{Domain: "roblox.com", Group: "kids", Until: now.Add(time.Hour)},
		{Domain: "expired.example", Until: now.Add(-time.Minute)},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(c.TempAllow) != 1 {
		t.Fatalf("expired temp rule should be pruned by Validate, have %d", len(c.TempAllow))
	}

	b := catalog.NewBuilder()
	b.Add("miner.example", catalog.CustomBit(0)) // first enabled custom list
	b.Add("adult.example", catalog.MaskOf([]string{"adult"}))
	e := New()
	e.Rebuild(c, b.Build())

	cases := []struct {
		domain string
		want   Action
		reason string
	}{
		{"www.tiktok.com", Block, "TikTok"},
		{"v16.tiktokcdn.com", Block, "TikTok"},
		{"www.roblox.com", Allow, "Temporarily allowed until " + now.Add(time.Hour).UTC().Format("15:04")}, // beats the app block
		{"miner.example", Block, "Blocklist: Crypto miners"},
		{"instagram.com", Allow, ""}, // app not selected for this profile
		{"adult.example", Block, "Adult content"},
	}
	for _, cs := range cases {
		d := e.Decide(kidMAC, cs.domain, now)
		if d.Action != cs.want || d.Reason != cs.reason {
			t.Errorf("Decide(%s) = %v %q, want %v %q", cs.domain, d.Action, d.Reason, cs.want, cs.reason)
		}
	}
	// temp allow expired so the app block is back
	if d := e.Decide(kidMAC, "www.roblox.com", now.Add(2*time.Hour)); d.Action != Block {
		t.Errorf("temp allow should expire: %v", d.Action)
	}
	if d := e.DecideForGroup("kids", "www.tiktok.com", now); d.Action != Block || d.GroupID != "kids" {
		t.Errorf("DecideForGroup = %+v", d)
	}
	if d := e.DecideForGroup("default", "www.tiktok.com", now); d.Action != Allow {
		t.Errorf("default profile shouldn't block TikTok: %+v", d)
	}

	// pause allows everything except device and group pauses
	c.Settings.PausedTill = now.Add(30 * time.Minute)
	c.Devices = append(c.Devices, config.Device{MAC: adultMAC, Name: "Laptop", Group: "default", Approved: true, PausedTill: now.Add(time.Hour)})
	c.Validate()
	e.Rebuild(c, b.Build())
	if d := e.Decide(kidMAC, "adult.example", now); d.Action != Allow || d.Reason != "Protection paused" {
		t.Errorf("paused protection: %+v", d)
	}
	if d := e.Decide(adultMAC, "example.com", now); d.Action != Paused {
		t.Errorf("device pause must still apply while protection is paused: %+v", d)
	}
	if !e.Paused(now) || e.Paused(now.Add(time.Hour)) {
		t.Error("Paused() wrong")
	}
}

func TestCustomListsPerProfile(t *testing.T) {
	c := config.Default()
	c.Settings.Timezone = "UTC"
	c.Devices = []config.Device{{MAC: kidMAC, Group: "kids", Approved: true}, {MAC: adultMAC, Group: "default", Approved: true}}
	c.Lists = []config.CustomList{{ID: "l1", Name: "Miners", URL: "https://example.com/a.txt", Enabled: true}}
	c.Settings.ListsPerProfile = true // a config that already has lists per profile
	c.Groups[1].Categories = append(c.Groups[1].Categories, "list:l1")
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	b := catalog.NewBuilder()
	b.Add("miner.example", catalog.CustomBit(0))
	b.Add("adult.example", catalog.MaskOf([]string{"adult"}))
	b.Add("both.example", catalog.CustomBit(0)|catalog.MaskOf([]string{"adult"}))
	b.Add("ads.example", catalog.MaskOf([]string{"ads"}))
	e := New()
	e.Rebuild(c, b.Build())
	now := time.Now()
	if d := e.Decide(kidMAC, "miner.example", now); d.Action != Block {
		t.Errorf("kids profile has the list on: %+v", d)
	}
	if d := e.Decide(adultMAC, "miner.example", now); d.Action != Allow {
		t.Errorf("default profile has it off: %+v", d)
	}
	// kids: adult category plus the list, three domains once each out of four
	if a, total := e.Coverage("kids"); a != 3 || total != 4 {
		t.Errorf("kids coverage %d of %d", a, total)
	}
	if a, _ := e.Coverage("default"); a != 0 {
		t.Errorf("default coverage %d, it only blocks malware", a)
	}
}
