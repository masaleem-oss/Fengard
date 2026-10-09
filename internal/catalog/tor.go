package catalog

import (
	"encoding/json"
	"io"
	"net"
	"sort"
	"strings"
)

// tor connects to relays by ip so these get blocked in the firewall
const TorIPsSource = "https://onionoo.torproject.org/summary?running=true"

// for networks where torproject.org is unreachable
const TorIPsMirror = "https://raw.githubusercontent.com/SecOps-Institute/Tor-IP-Addresses/master/tor-nodes.lst"

// baked in so a fresh install blocks tor before the relay list downloads
var torAuthorities = []string{
	"128.31.0.39",    // moria1
	"131.188.40.189", // gabelmoo
	"193.23.244.244", // dannenberg
	"171.25.193.9",   // maatuska
	"199.58.81.140",  // longclaw
	"204.13.164.118", // bastet
	"45.66.35.11",    // dizum
	"217.196.147.77", // tor26
	"216.218.219.41", // faravahar
	"154.35.175.225", // serge bridge authority
	"2001:858:2:2:aabb:0:563b:1526",
	"2001:67c:289c::9",
	"2001:638:a000:4140::ffff:189",
	"2620:13:4000:6000::1000:118",
	"2001:678:558:1000::244",
}

func ParseTorRelays(r io.Reader) ([]string, error) {
	var doc struct {
		Relays []struct {
			Addresses []string `json:"a"`
			Running   bool     `json:"r"`
		} `json:"relays"`
	}
	if err := json.NewDecoder(io.LimitReader(r, maxDownload)).Decode(&doc); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, rel := range doc.Relays {
		for _, a := range rel.Addresses {
			a = strings.Trim(a, "[]")
			if ip := net.ParseIP(a); ip != nil && !seen[a] {
				seen[a] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for ip := range seen {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out, nil
}

func mergeTor(relays []string) []string {
	seen := make(map[string]bool, len(relays)+len(torAuthorities))
	out := make([]string, 0, len(relays)+len(torAuthorities))
	for _, ip := range append(append([]string{}, torAuthorities...), relays...) {
		if !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	return out
}
