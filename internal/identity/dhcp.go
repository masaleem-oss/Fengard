package identity

import (
	"cmp"
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

// ParseDHCP reads a client's discover or request out of a udp payload
func ParseDHCP(b []byte, now time.Time) (mac string, p Print, ok bool) {
	// op htype hlen hops xid secs flags ciaddr yiaddr siaddr giaddr chaddr sname file cookie
	if len(b) < 240 || b[0] != 1 || b[1] != 1 || b[2] != 6 || binary.BigEndian.Uint32(b[236:240]) != 0x63825363 {
		return "", p, false
	}
	mac = net.HardwareAddr(b[28:34]).String()
	var kind byte
	for i := 240; i < len(b); {
		code := b[i]
		if code == 255 {
			break
		}
		if code == 0 {
			i++
			continue
		}
		if i+1 >= len(b) {
			break
		}
		n := int(b[i+1])
		if i+2+n > len(b) {
			break
		}
		v := b[i+2 : i+2+n]
		switch code {
		case 53:
			if n == 1 {
				kind = v[0]
			}
		case 12:
			p.Hostname = clean(string(v))
		case 60:
			p.Vendor = clean(string(v))
		case 55:
			parts := make([]string, n)
			for j, o := range v {
				parts[j] = strconv.Itoa(int(o))
			}
			p.Params = strings.Join(parts, ",")
		}
		i += 2 + n
	}
	// discover request or inform, not a release or decline
	if kind != 1 && kind != 3 && kind != 8 {
		return "", p, false
	}
	p.At = now
	return mac, p, true
}

func clean(s string) string {
	s = strings.TrimRight(s, "\x00")
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

// Prints remembers the last dhcp fingerprint of every address
type Prints struct {
	KV *store.KV

	mu sync.Mutex
	m  map[string]Print
}

func NewPrints(kv *store.KV) *Prints {
	p := &Prints{KV: kv, m: map[string]Print{}}
	if kv != nil {
		kv.Each(func(mac string, v []byte) error {
			var pr Print
			if json.Unmarshal(v, &pr) == nil {
				p.m[mac] = pr
			}
			return nil
		})
	}
	return p
}

func (p *Prints) Set(mac string, pr Print) {
	p.mu.Lock()
	old, had := p.m[mac]
	if !had && len(p.m) >= maxTracked {
		// private addresses pile up over time so the longest quiet one goes
		var oldest string
		for m, x := range p.m {
			if oldest == "" || x.At.Before(p.m[oldest].At) {
				oldest = m
			}
		}
		delete(p.m, oldest)
		if p.KV != nil {
			p.KV.Delete(oldest)
		}
	}
	p.m[mac] = pr
	p.mu.Unlock()
	if !had {
		log.Printf("dhcp: %s asked for an address (%s)", mac, cmp.Or(pr.Hostname, "no name"))
	}
	// renewals repeat the same thing so only write changes
	if p.KV != nil && (!had || old.Kind() != pr.Kind() || old.Hostname != pr.Hostname) {
		p.KV.Put(mac, pr)
	}
}

func (p *Prints) Get(mac string) (Print, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.m[mac]
	return pr, ok
}
