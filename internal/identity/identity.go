// package identity spots a phone or laptop that came back with a new private mac address.
// it compares the dhcp name and fingerprint, when the old address went quiet and what the
// device looks up, so a phone that rotates its address lands back in its own profile
package identity

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/devices"
)

// the locally administered bit is set on private wifi addresses
func Private(mac string) bool { return devices.IsRandomized(mac) }

// what a device says about itself when it asks for an address
type Print struct {
	Hostname string    `json:"hostname,omitempty"`
	Params   string    `json:"params,omitempty"` // dhcp option 55 in the order it was sent
	Vendor   string    `json:"vendor,omitempty"` // dhcp option 60
	At       time.Time `json:"at"`
}

// same os and version ask for the same options in the same order
func (p Print) Kind() string {
	if p.Params == "" {
		return ""
	}
	return p.Params + "|" + p.Vendor
}

// a device that showed up with a new private address
type Newcomer struct {
	MAC       string
	Hostname  string
	Print     Print
	FirstSeen time.Time
	Domains   map[string]bool
}

// a device fengard already knows
type Candidate struct {
	MAC       string // its current address
	Name      string
	Hostnames []string
	Print     Print
	LastSeen  time.Time // last lookup from any of its addresses
	FirstSeen time.Time
	Domains   map[string]bool
}

type Match struct {
	MAC     string   `json:"mac"`
	Name    string   `json:"name"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
	Sure    bool     `json:"sure"` // strong enough to merge without asking
}

const (
	handover    = 15 * time.Minute
	minRare     = 5 // rare lookups needed before habits count
	suggestAt   = 3
	sureAt      = 6
	sureLead    = 3
	suggestLead = 2
)

// Best picks the known device the newcomer most likely is. df is how many devices
// looked up each domain and devices how many devices there are, so common names count less
func Best(n Newcomer, cands []Candidate, df map[string]int, devices int) (Match, bool) {
	type scored struct {
		m      Match
		strong bool // handover or habits, the parts a renamed phone cant fake
		vetoed bool
	}
	var all []scored
	for _, c := range cands {
		if c.MAC == n.MAC || (!c.FirstSeen.IsZero() && c.FirstSeen.After(n.FirstSeen)) {
			continue
		}
		s := scored{m: Match{MAC: c.MAC, Name: c.Name}}
		add := func(pts int, why string) {
			s.m.Score += pts
			if why != "" {
				s.m.Reasons = append(s.m.Reasons, why)
			}
		}

		// two devices online at once cant be the same device
		if !c.LastSeen.IsZero() && c.LastSeen.After(n.FirstSeen.Add(30*time.Second)) {
			s.vetoed = true
		}

		if host := normName(n.Hostname); host != "" {
			same, differ := false, false
			for _, h := range c.Hostnames {
				switch nh := normName(h); {
				case nh == "":
				case nh == host:
					same = true
				default:
					differ = true
				}
			}
			switch {
			case same && generic(host):
				add(1, "same generic name")
			case same:
				add(3, fmt.Sprintf("same name (%s)", n.Hostname))
			case differ:
				add(-2, "")
			}
		}

		if a, b := n.Print.Kind(), c.Print.Kind(); a != "" && b != "" {
			if a == b {
				add(2, "same kind of device")
			} else {
				add(-3, "")
			}
		}

		if !c.LastSeen.IsZero() && !s.vetoed {
			switch gap := n.FirstSeen.Sub(c.LastSeen); {
			case gap >= -30*time.Second && gap <= handover:
				add(2, fmt.Sprintf("its old address went quiet %s before", roughly(gap)))
				s.strong = true
			case gap > 0 && gap <= 6*time.Hour:
				add(1, "its old address went quiet earlier today")
			}
		}

		switch share, rare := habits(n.Domains, c.Domains, df, devices); {
		case rare < minRare:
		case share >= 0.6:
			add(4, "looks up the same sites")
			s.strong = true
		case share >= 0.35:
			add(2, "looks up many of the same sites")
			s.strong = true
		case share <= 0.1 && rare >= 2*minRare && len(c.Domains) >= 2*minRare:
			add(-3, "")
		}
		all = append(all, s)
	}
	var ok []scored
	for _, s := range all {
		if !s.vetoed {
			ok = append(ok, s)
		}
	}
	if len(ok) == 0 {
		return Match{}, false
	}
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].m.Score > ok[j].m.Score })
	best, next := ok[0], math.MinInt
	if len(ok) > 1 {
		next = ok[1].m.Score
	}
	lead := best.m.Score - next
	if best.m.Score < suggestAt || lead < suggestLead {
		return Match{}, false
	}
	// a matching name alone is easy to fake so merging on its own needs timing or habits too
	best.m.Sure = best.m.Score >= sureAt && lead >= sureLead && best.strong
	return best.m, true
}

// how much of what the newcomer looks up only it and few others look up, weighted by rarity
func habits(nd, cd map[string]bool, df map[string]int, devices int) (share float64, rare int) {
	limit := max(2, devices/5)
	var have, total float64
	for d := range nd {
		n := df[d]
		if n == 0 || n > limit {
			continue
		}
		w := math.Log(1 + float64(devices)/float64(n))
		total += w
		rare++
		if cd[d] {
			have += w
		}
	}
	if total == 0 {
		return 0, rare
	}
	return have / total, rare
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(strings.TrimSuffix(s, ".lan"), ".local")
	return nonAlnum.ReplaceAllString(s, "")
}

// names lots of devices share say little about which one it is
var genericRe = regexp.MustCompile(`^(iphone|ipad|ipod|android|localhost|unknown|none|mac|imac|macbook(air|pro)?|watch|applewatch|espressif|esp[0-9a-f]*|(galaxy|pixel|oneplus|redmi|xiaomi|kindle|echo|fire|amazon)[a-z0-9]*)$`)

func generic(norm string) bool { return genericRe.MatchString(norm) }

func roughly(d time.Duration) string {
	switch {
	case d < 90*time.Second:
		return "a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Round(time.Minute).Minutes()))
	default:
		return "over an hour"
	}
}
