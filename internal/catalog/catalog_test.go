package catalog

import (
	"strings"
	"testing"
)

func TestDomainSet(t *testing.T) {
	b := NewBuilder()
	b.Add("tracker.com", 1)
	b.Add("example.org", 2)
	b.Add("tracker.com", 4) // same domain in a second list merges masks
	b.Add("a.b.c.net", 8)
	s := b.Build()

	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
	cases := map[string]Mask{
		"tracker.com":      5,
		"cdn.tracker.com":  5,
		"example.org":      2,
		"www.example.org":  2,
		"c.net":            0,
		"x.a.b.c.net":      8,
		"tracker.com.evil": 0,
		"nottracker.com":   0,
		"":                 0,
	}
	for d, want := range cases {
		if got := s.Match(d); got != want {
			t.Errorf("Match(%q) = %d, want %d", d, got, want)
		}
	}
}

func TestParseList(t *testing.T) {
	in := `# comment
0.0.0.0 hosts.example.com
127.0.0.1 localhost
plain.example.com
*.wild.example.com
||adblock.example.com^
Mixed.Case.COM.
bad domain
not_valid..com
! adblock comment
`
	var got []string
	ParseList(strings.NewReader(in), func(d string) { got = append(got, d) })
	want := []string{"hosts.example.com", "plain.example.com", "wild.example.com", "adblock.example.com", "mixed.case.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestMaskRoundTrip(t *testing.T) {
	m := MaskOf([]string{"social", "ads", "nope"})
	ids := m.IDs()
	if len(ids) != 2 || ids[0] != "ads" || ids[1] != "social" {
		t.Fatalf("IDs = %v", ids)
	}
}

func TestSeedsLoad(t *testing.T) {
	c := New(t.TempDir())
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	if c.Set().Match("www.roblox.com")&MaskOf([]string{"gaming"}) == 0 {
		t.Error("gaming seed not loaded")
	}
	if c.Set().Match("chatgpt.com")&MaskOf([]string{"ai"}) == 0 {
		t.Error("ai seed not loaded")
	}
}

func TestParseTorRelays(t *testing.T) {
	doc := `{"version":"9.0","relays":[{"n":"a","a":["1.2.3.4","[2001:db8::1]"],"r":true},{"n":"b","a":["1.2.3.4","5.6.7.8"],"r":true},{"n":"bad","a":["not-an-ip"]}],"bridges":[]}`
	ips, err := ParseTorRelays(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ips, " "); got != "1.2.3.4 2001:db8::1 5.6.7.8" {
		t.Fatalf("got %q", got)
	}
	merged := mergeTor(ips)
	if len(merged) != len(ips)+len(torAuthorities) {
		t.Fatalf("authorities missing: %d", len(merged))
	}
	if Index("tor") < 0 || MaskOf([]string{"tor"}) == 0 {
		t.Fatal("tor category not registered")
	}
}
