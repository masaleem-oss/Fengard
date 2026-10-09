package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"unknown category": func(c *Config) { c.Groups[0].Categories = []string{"nope"} },
		"bad schedule time": func(c *Config) {
			c.Groups[0].Schedules = []Schedule{{Name: "x", Days: []int{1}, Start: "25:00", End: "07:00", BlockAll: true}}
		},
		"device in no group": func(c *Config) { c.Devices = []Device{{MAC: "aa:bb:cc:dd:ee:ff", Group: "ghost"}} },
		"public forward target": func(c *Config) {
			c.PortForwards = []PortForward{{Name: "x", Proto: "tcp", ExtPort: 80, DestIP: "8.8.8.8", DestPort: 80}}
		},
		"duplicate forward": func(c *Config) {
			f := PortForward{Name: "x", Proto: "tcp", ExtPort: 80, DestIP: "192.168.8.2", DestPort: 80, Enabled: true}
			c.PortForwards = []PortForward{f, f}
		},
		"bad upstream":     func(c *Config) { c.Settings.Upstreams = []string{"dns.google"} },
		"DoT without name": func(c *Config) { c.Settings.Upstreams = []string{"tls://1.1.1.1"} },
		"missing default":  func(c *Config) { c.Settings.DefaultGroup = "nope" },
		"bad domain rule":  func(c *Config) { c.Block = []string{"not a domain"} },
	}
	for name, mod := range cases {
		c := Default()
		mod(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestNormalizes(t *testing.T) {
	c := Default()
	c.Block = []string{"https://www.Example.com/path?q=1", "example.com.", "www.example.com"}
	c.Devices = []Device{{MAC: "AA-BB-CC-DD-EE-FF", Group: "default"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Block, ",") != "www.example.com,example.com" {
		t.Errorf("Block = %v", c.Block)
	}
	if c.Devices[0].MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC = %s", c.Devices[0].MAC)
	}
}

func TestStoreHistoryAndRollback(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	notified := 0
	s.Subscribe(func(*Config) { notified++ })

	if _, err := s.Update("test", "block a", func(c *Config) error { c.Block = []string{"a.example"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("test", "invalid", func(c *Config) error { c.Settings.DefaultGroup = "nope"; return nil }); err == nil {
		t.Fatal("invalid update accepted")
	}
	if got := s.Get().Block; len(got) != 1 {
		t.Fatalf("failed update changed config: %v", got)
	}
	if _, err := s.Rollback("test", 1); err != nil {
		t.Fatal(err)
	}
	if len(s.Get().Block) != 0 || s.Get().Version != 3 {
		t.Fatalf("rollback: block=%v version=%d", s.Get().Block, s.Get().Version)
	}
	if notified != 3 { // subscribe update rollback
		t.Errorf("notified %d times, want 3", notified)
	}
	h := s.History()
	if len(h) != 3 || h[0].Summary != "Rolled back to version 1" {
		t.Errorf("history = %+v", h)
	}

	s2, err := Open(dir)
	if err != nil || s2.Get().Version != 3 {
		t.Fatalf("reopen: %v version=%d", err, s2.Get().Version)
	}
}
