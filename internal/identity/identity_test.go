package identity

import (
	"encoding/binary"
	"testing"
	"time"
)

var (
	t0      = time.Date(2026, 10, 11, 18, 0, 0, 0, time.UTC)
	iosKind = Print{Params: "1,121,3,6,15,108,114,119,252,95,44,46"}
	andKind = Print{Params: "1,3,6,15,26,28,51,58,59,43,114", Vendor: "android-dhcp-14"}
)

func set(ds ...string) map[string]bool {
	m := map[string]bool{}
	for _, d := range ds {
		m[d] = true
	}
	return m
}

// apps a phone uses that nobody else in the house does
var asadApps = set("tiktokv.com", "duolingo.com", "strava.com", "revolut.com", "monzo.com", "spotify.com", "reddit.com")

func house() ([]Candidate, map[string]int) {
	cands := []Candidate{
		{MAC: "aa:aa:aa:aa:aa:01", Name: "Asad's iPhone", Hostnames: []string{"Asads-iPhone"}, Print: iosKind,
			LastSeen: t0.Add(-2 * time.Minute), FirstSeen: t0.Add(-90 * 24 * time.Hour), Domains: asadApps},
		{MAC: "aa:aa:aa:aa:aa:02", Name: "Sam's iPhone", Hostnames: []string{"Sams-iPhone"}, Print: iosKind,
			LastSeen: t0.Add(-3 * time.Hour), FirstSeen: t0.Add(-90 * 24 * time.Hour), Domains: set("roblox.com", "minecraft.net", "snapchat.com", "youtube.com", "twitch.tv", "discord.gg")},
		{MAC: "aa:aa:aa:aa:aa:03", Name: "Living room TV", Hostnames: []string{"Samsung"}, Print: Print{Params: "1,3,6"},
			LastSeen: t0.Add(-time.Minute), FirstSeen: t0.Add(-90 * 24 * time.Hour), Domains: set("samsungcloud.com")},
	}
	df := map[string]int{}
	for _, c := range cands {
		for d := range c.Domains {
			df[d]++
		}
	}
	return cands, df
}

func TestRotatedIPhoneIsRecognizedStraightAway(t *testing.T) {
	cands, df := house()
	n := Newcomer{MAC: "ce:11:22:33:44:55", Hostname: "Asads-iPhone", Print: iosKind, FirstSeen: t0}
	m, ok := Best(n, cands, df, 3)
	if !ok || m.MAC != "aa:aa:aa:aa:aa:01" || !m.Sure {
		t.Fatalf("got %+v %v", m, ok)
	}
}

// an android with a private address and no name only gets placed once its habits show
func TestNamelessPhoneNeedsHabits(t *testing.T) {
	cands, df := house()
	cands[0].Print = andKind
	n := Newcomer{MAC: "ce:11:22:33:44:56", Print: andKind, FirstSeen: t0}
	m, ok := Best(n, cands, df, 3)
	if !ok || m.Sure {
		t.Fatalf("without habits it should only be a suggestion: %+v %v", m, ok)
	}
	n.Domains = set("tiktokv.com", "duolingo.com", "strava.com", "revolut.com", "monzo.com", "apple.com")
	for d := range n.Domains {
		df[d]++
	}
	m, ok = Best(n, cands, df, 4)
	if !ok || m.MAC != "aa:aa:aa:aa:aa:01" || !m.Sure {
		t.Fatalf("habits should settle it: %+v %v", m, ok)
	}
}

// a kid renaming their phone to a parents name doesnt get the parents profile
func TestCopiedNameIsNotEnough(t *testing.T) {
	cands, df := house()
	cands[0].LastSeen = t0.Add(-5 * time.Hour) // the parent is out
	n := Newcomer{MAC: "ce:11:22:33:44:57", Hostname: "Asads-iPhone", Print: iosKind, FirstSeen: t0,
		Domains: set("roblox.com", "minecraft.net", "snapchat.com", "twitch.tv", "discord.gg", "fortnite.com")}
	for d := range n.Domains {
		df[d]++
	}
	if m, ok := Best(n, cands, df, 4); ok && m.Sure {
		t.Fatalf("merged a renamed phone: %+v", m)
	}
}

func TestTwoDevicesOnlineAtOnceArentTheSame(t *testing.T) {
	cands, df := house()
	cands[0].LastSeen = t0.Add(5 * time.Minute)
	n := Newcomer{MAC: "ce:11:22:33:44:58", Hostname: "Asads-iPhone", Print: iosKind, FirstSeen: t0}
	if m, ok := Best(n, cands, df, 3); ok && m.MAC == "aa:aa:aa:aa:aa:01" {
		t.Fatalf("matched a device that is online right now: %+v", m)
	}
}

func TestGenericNameAloneSaysNothing(t *testing.T) {
	cands, df := house()
	cands[0].Hostnames = []string{"iPhone"}
	cands[1].Hostnames = []string{"iPhone"}
	n := Newcomer{MAC: "ce:11:22:33:44:59", Hostname: "iPhone", Print: iosKind, FirstSeen: t0.Add(4 * time.Hour)}
	if m, ok := Best(n, cands, df, 3); ok {
		t.Fatalf("guessed from a generic name: %+v", m)
	}
}

func TestPrivate(t *testing.T) {
	if !Private("ce:11:22:33:44:55") || Private("a4:83:e7:00:00:01") {
		t.Fatal("private bit")
	}
}

func TestSite(t *testing.T) {
	for in, want := range map[string]string{
		"api16-normal-c-useast1a.tiktokv.com": "tiktokv.com", "www.bbc.co.uk": "bbc.co.uk", "bbc.co.uk": "bbc.co.uk",
		"example.com.": "example.com", "printer.lan": "", "1.8.168.192.in-addr.arpa": "", "localhost": "", "x.io": "x.io",
	} {
		if got := Site(in); got != want {
			t.Errorf("Site(%q) = %q want %q", in, got, want)
		}
	}
}

func dhcpPacket(kind byte, opts ...[]byte) []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 1, 1, 6
	copy(b[28:], []byte{0xce, 0x11, 0x22, 0x33, 0x44, 0x55})
	binary.BigEndian.PutUint32(b[236:], 0x63825363)
	b = append(b, 53, 1, kind)
	for _, o := range opts {
		b = append(b, o...)
	}
	return append(b, 255)
}

func TestParseDHCP(t *testing.T) {
	pkt := dhcpPacket(3,
		[]byte{55, 4, 1, 121, 3, 6},
		[]byte{12, 12, 'A', 's', 'a', 'd', 's', '-', 'i', 'P', 'h', 'o', 'n', 'e'},
		[]byte{60, 4, 'M', 'S', 'F', 'T'})
	mac, p, ok := ParseDHCP(pkt, t0)
	if !ok || mac != "ce:11:22:33:44:55" || p.Hostname != "Asads-iPhone" || p.Params != "1,121,3,6" || p.Vendor != "MSFT" {
		t.Fatalf("%v %q %+v", ok, mac, p)
	}
	if _, _, ok := ParseDHCP(dhcpPacket(7), t0); ok {
		t.Fatal("a release isnt a fingerprint")
	}
	if _, _, ok := ParseDHCP(pkt[:100], t0); ok {
		t.Fatal("short packet")
	}
	// option length running past the end
	bad := dhcpPacket(1, []byte{12, 200, 'x'})
	if _, p, ok := ParseDHCP(bad, t0); !ok || p.Hostname != "" {
		t.Fatalf("truncated option: %+v %v", p, ok)
	}
}
