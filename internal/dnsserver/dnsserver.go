package dnsserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sync/singleflight"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	"github.com/masaleem-oss/Fengard/internal/localdns"
	"github.com/masaleem-oss/Fengard/internal/policy"
	"github.com/masaleem-oss/Fengard/internal/querylog"
	"github.com/masaleem-oss/Fengard/internal/ratelimit"
)

type Server struct {
	Policy  *policy.Engine
	Devices *devices.Tracker
	Log     *querylog.Log
	Local   *localdns.Resolver

	BlockIP    net.IP // points at this box
	BlockIPv6  net.IP
	LocalNames []string       // eg fengard.lan
	Allowed    []netip.Prefix // empty means private ranges
	Screen     ScreenTime

	upstreams atomic.Pointer[[]config.Upstream]
	ipv4Only  atomic.Pointer[[]netip.Prefix] // no aaaa for these since vpn tunnels only carry ipv4
	bypass    atomic.Bool                    // answer the firefox doh and icloud relay canaries
	dnssec    atomic.Bool
	doh       *http.Client
	cache     *cache
	flight    singleflight.Group
	sem       chan struct{}
	perClient *ratelimit.Limiter
	global    *ratelimit.Limiter

	Counters Counters
}

type ScreenTime interface {
	Note(identity, group, domain string, targets []string, now time.Time)
}

type Counters struct {
	Queries     atomic.Uint64
	RateLimited atomic.Uint64
	Refused     atomic.Uint64
	CacheHits   atomic.Uint64
	Upstream    atomic.Uint64
	UpstreamErr atomic.Uint64
	Stale       atomic.Uint64
	Overloaded  atomic.Uint64
}

const (
	maxInflight  = 512
	cacheEntries = 20000 // about 10 mb worst case
	globalQPS    = 20000 // backstop for all clients
	maxClients   = 4096
	clientTTL    = 60 // short so rule changes apply within a minute
	blockTTL     = 10
)

var defaultAllowed = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), // cgnat and vpn clients
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// call before Configure
func (s *Server) Init() {
	s.cache = newCache(cacheEntries)
	s.sem = make(chan struct{}, maxInflight)
	s.perClient = ratelimit.New(100, 300, maxClients)
	s.global = ratelimit.New(globalQPS, globalQPS*2, 1)
	s.doh = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		MaxIdleConns: 8, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}}
	if len(s.Allowed) == 0 {
		s.Allowed = defaultAllowed
	}
	if s.upstreams.Load() == nil {
		s.upstreams.Store(&[]config.Upstream{})
	}
}

// ipv4 only tunnels get empty aaaa so devices dont time out on ipv6 first
func (s *Server) SetIPv4Only(nets []netip.Prefix) { s.ipv4Only.Store(&nets) }

func (s *Server) isIPv4Only(ip netip.Addr) bool {
	p := s.ipv4Only.Load()
	if p == nil {
		return false
	}
	for _, n := range *p {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) Configure(set config.Settings) {
	var ups []config.Upstream
	for _, u := range set.Upstreams {
		if p, err := config.ParseUpstream(u); err == nil {
			ups = append(ups, p)
		}
	}
	s.upstreams.Store(&ups)
	s.bypass.Store(set.BlockBypass)
	s.dnssec.Store(set.DNSSEC)
	if s.perClient != nil {
		s.perClient.SetRate(float64(set.ClientRateQPS), float64(set.ClientRateQPS*3))
	}
}

func (s *Server) FlushCache() { s.cache.flush() }

func (s *Server) CacheLen() int { return s.cache.len() }

func (s *Server) ListenAndServe(ctx context.Context, addrs ...string) error {
	if s.cache == nil {
		s.Init()
	}
	// SO_REUSEPORT on linux so the kernel spreads packets over several sockets
	readers := 1
	if runtime.GOOS == "linux" {
		readers = min(runtime.NumCPU(), 4)
	}
	var servers []*dns.Server
	for _, addr := range addrs {
		for _, network := range []string{"udp", "tcp"} {
			n := 1
			if network == "udp" {
				n = readers
			}
			for range n {
				servers = append(servers, &dns.Server{
					Addr: addr, Net: network, Handler: s, ReusePort: n > 1,
					ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
					// cap idle tcp conns so they cant be hoarded
					IdleTimeout:   func() time.Duration { return 8 * time.Second },
					MaxTCPQueries: 100,
				})
			}
		}
	}
	errc := make(chan error, len(servers))
	for _, srv := range servers {
		go func() { errc <- srv.ListenAndServe() }()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		for _, srv := range servers {
			srv.Shutdown()
		}
		return nil
	}
}

func (s *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()
	s.Counters.Queries.Add(1)

	addr, _ := netip.ParseAddrPort(w.RemoteAddr().String())
	ip := addr.Addr().Unmap()
	_, isUDP := w.RemoteAddr().(*net.UDPAddr)

	if !s.allowedClient(ip) {
		// stay silent for strangers or we turn into an amplifier
		s.Counters.Refused.Add(1)
		return
	}
	client := ip.String()
	if !s.perClient.Allow(client) || !s.global.Allow("") {
		s.Counters.RateLimited.Add(1)
		if !isUDP {
			s.reply(w, r, dns.RcodeRefused)
		}
		return
	}
	if r.Opcode != dns.OpcodeQuery || len(r.Question) != 1 || r.Response {
		s.reply(w, r, dns.RcodeFormatError)
		return
	}
	q := r.Question[0]
	if q.Qclass != dns.ClassINET {
		s.reply(w, r, dns.RcodeRefused)
		return
	}
	if q.Qtype == dns.TypeANY {
		s.reply(w, r, dns.RcodeNotImplemented) // rfc 8482 no amplification via ANY
		return
	}
	domain := strings.TrimSuffix(strings.ToLower(q.Name), ".")

	// answers that dont depend on the device
	if resp := s.special(r, q, domain); resp != nil {
		w.WriteMsg(resp)
		return
	}

	info, _ := s.Devices.Lookup(client)
	if resp := s.local(r, q, domain); resp != nil {
		if isUDP {
			resp.Truncate(udpSize(r))
		}
		w.WriteMsg(resp)
		s.Log.Add(querylog.Entry{Time: start, Client: client, MAC: info.MAC, Device: info.Hostname, Domain: domain,
			Type: dns.TypeToString[q.Qtype], Action: "allowed", Reason: "Local network",
			Ms: float64(time.Since(start).Microseconds()) / 1000})
		return
	}
	dec := s.Policy.Decide(info.MAC, domain, start)

	entry := querylog.Entry{
		Time: start, Client: client, MAC: info.MAC, Device: dec.Device, Group: dec.Group,
		Domain: domain, Type: dns.TypeToString[q.Qtype],
		Action: dec.Action.String(), Reason: dec.Reason, Category: dec.Category,
	}
	if entry.Device == "" {
		entry.Device = info.Hostname
	}

	var resp *dns.Msg
	switch dec.Action {
	case policy.Allow:
		if q.Qtype == dns.TypeAAAA && s.isIPv4Only(ip) {
			resp = new(dns.Msg)
			resp.SetReply(r)
		} else {
			resp, entry.Cached = s.resolve(r, q)
		}
	case policy.SafeSearch:
		resp = s.safeSearch(r, q, dec.Target)
	default:
		resp = s.blocked(r, q)
	}
	if isUDP {
		resp.Truncate(udpSize(r))
	}
	w.WriteMsg(resp)

	entry.Ms = float64(time.Since(start).Microseconds()) / 1000
	s.Log.Add(entry)
	if dec.Person && s.Screen != nil && (dec.Action == policy.Allow || dec.Action == policy.SafeSearch) {
		s.Screen.Note(info.MAC, dec.GroupID, domain, s.Policy.Targets(domain), start)
	}
}

func (s *Server) allowedClient(ip netip.Addr) bool {
	for _, p := range s.Allowed {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) reply(w dns.ResponseWriter, r *dns.Msg, rcode int) {
	m := new(dns.Msg)
	m.SetRcode(r, rcode)
	w.WriteMsg(m)
}

func udpSize(r *dns.Msg) int {
	if o := r.IsEdns0(); o != nil {
		return int(min(o.UDPSize(), 1232))
	}
	return dns.MinMsgSize
}

func (s *Server) special(r *dns.Msg, q dns.Question, domain string) *dns.Msg {
	for _, n := range s.LocalNames {
		if domain == n {
			return s.pointAtUs(r, q, 300)
		}
	}
	if !s.bypass.Load() {
		return nil
	}
	switch domain {
	case "use-application-dns.net", // tells firefox to turn off its own doh
		"mask.icloud.com", "mask-h2.icloud.com", // turns off icloud private relay
		"doh.test":
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeNameError)
		m.RecursionAvailable = true
		return m
	}
	return nil
}

// names under the local zone never go upstream
func (s *Server) local(r *dns.Msg, q dns.Question, domain string) *dns.Msg {
	if s.Local == nil {
		return nil
	}
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true
	m.Authoritative = true
	hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: 60}

	if q.Qtype == dns.TypePTR {
		ip := reverseIP(domain)
		if ip == nil {
			return nil
		}
		if !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return nil // public reverse lookups go upstream
		}
		if name, ok := s.Local.Reverse(ip.String()); ok {
			hdr.Rrtype = dns.TypePTR
			m.Answer = append(m.Answer, &dns.PTR{Hdr: hdr, Ptr: dns.Fqdn(name)})
		} else {
			m.Rcode = dns.RcodeNameError
		}
		return m
	}

	a, ok := s.Local.Lookup(domain)
	if !ok {
		if strings.HasSuffix(domain, "."+s.Local.Zone()) {
			m.Rcode = dns.RcodeNameError
			return m
		}
		return nil
	}
	switch {
	case a.CNAME != "":
		m.Answer = append(m.Answer, &dns.CNAME{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60}, Target: dns.Fqdn(a.CNAME)})
		if q.Qtype == dns.TypeA || q.Qtype == dns.TypeAAAA {
			sub := new(dns.Msg)
			sub.SetQuestion(dns.Fqdn(a.CNAME), q.Qtype)
			if resolved, _ := s.resolve(sub, sub.Question[0]); resolved != nil && resolved.Rcode == dns.RcodeSuccess {
				m.Answer = append(m.Answer, resolved.Answer...)
			}
		}
	case q.Qtype == dns.TypeA:
		for _, ip := range a.A {
			m.Answer = append(m.Answer, &dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: ip})
		}
	case q.Qtype == dns.TypeAAAA:
		for _, ip := range a.AAAA {
			m.Answer = append(m.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}, AAAA: ip})
		}
	}
	return m
}

// eg 1.8.168.192.in-addr.arpa or ip6.arpa back to an ip
func reverseIP(name string) net.IP {
	if rest, ok := strings.CutSuffix(name, ".in-addr.arpa"); ok {
		parts := strings.Split(rest, ".")
		if len(parts) != 4 {
			return nil
		}
		for i, j := 0, 3; i < j; i, j = i+1, j-1 {
			parts[i], parts[j] = parts[j], parts[i]
		}
		return net.ParseIP(strings.Join(parts, "."))
	}
	if rest, ok := strings.CutSuffix(name, ".ip6.arpa"); ok {
		nibbles := strings.Split(rest, ".")
		if len(nibbles) != 32 {
			return nil
		}
		var b strings.Builder
		for i := 31; i >= 0; i-- {
			b.WriteString(nibbles[i])
			if i%4 == 0 && i > 0 {
				b.WriteByte(':')
			}
		}
		return net.ParseIP(b.String())
	}
	return nil
}

func (s *Server) blocked(r *dns.Msg, q dns.Question) *dns.Msg {
	return s.pointAtUs(r, q, blockTTL)
}

// no https or svcb records so browsers dont learn real ips or ech keys
func (s *Server) pointAtUs(r *dns.Msg, q dns.Question, ttl uint32) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true
	hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: ttl}
	switch {
	case q.Qtype == dns.TypeA && s.BlockIP != nil:
		hdr.Rrtype = dns.TypeA
		m.Answer = append(m.Answer, &dns.A{Hdr: hdr, A: s.BlockIP.To4()})
	case q.Qtype == dns.TypeAAAA && s.BlockIPv6 != nil:
		hdr.Rrtype = dns.TypeAAAA
		m.Answer = append(m.Answer, &dns.AAAA{Hdr: hdr, AAAA: s.BlockIPv6})
	}
	return m
}

func (s *Server) resolve(r *dns.Msg, q dns.Question) (*dns.Msg, bool) {
	do := s.dnssec.Load()
	if o := r.IsEdns0(); o != nil && o.Do() {
		do = true
	}
	key := cacheKey(q, do)
	now := time.Now()
	if m := s.cache.get(key, now, false); m != nil {
		s.Counters.CacheHits.Add(1)
		return s.finish(r, m), true
	}

	v, err, _ := s.flight.Do(key, func() (any, error) {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			s.Counters.Overloaded.Add(1)
			return nil, errOverloaded
		}
		req := new(dns.Msg)
		req.SetQuestion(q.Name, q.Qtype)
		req.SetEdns0(1232, do)
		resp, err := s.exchange(req)
		if err != nil {
			return nil, err
		}
		s.cache.put(key, resp, time.Now())
		return resp, nil
	})
	if err != nil {
		if m := s.cache.get(key, now, true); m != nil {
			s.Counters.Stale.Add(1)
			return s.finish(r, m), true
		}
		m := new(dns.Msg)
		m.SetRcode(r, dns.RcodeServerFailure)
		return m, false
	}
	return s.finish(r, v.(*dns.Msg).Copy()), false
}

var errOverloaded = errors.New("too many queries in flight")

func (s *Server) finish(r, m *dns.Msg) *dns.Msg {
	m.Id = r.Id
	m.Response = true
	m.RecursionDesired = r.RecursionDesired
	m.RecursionAvailable = true
	for _, rrs := range [][]dns.RR{m.Answer, m.Ns} {
		for _, rr := range rrs {
			if h := rr.Header(); h.Ttl > clientTTL {
				h.Ttl = clientTTL
			}
		}
	}
	return m
}

func (s *Server) exchange(req *dns.Msg) (*dns.Msg, error) {
	ups := *s.upstreams.Load()
	if len(ups) == 0 {
		return nil, errors.New("no upstream DNS servers configured")
	}
	var lastErr error
	for _, u := range ups {
		s.Counters.Upstream.Add(1)
		if u.Net == "https" {
			resp, err := s.exchangeDoH(req, u.URL)
			if err == nil {
				return resp, nil
			}
			s.Counters.UpstreamErr.Add(1)
			lastErr = err
			continue
		}
		c := &dns.Client{Net: u.Net, Timeout: 2 * time.Second}
		if u.Net == "tcp-tls" {
			c.TLSConfig = &tls.Config{ServerName: u.TLSName, MinVersion: tls.VersionTLS12}
			c.Timeout = 4 * time.Second
		}
		resp, _, err := c.Exchange(req, u.Addr)
		if err == nil && resp.Truncated && u.Net == "udp" {
			c.Net = "tcp"
			resp, _, err = c.Exchange(req, u.Addr)
		}
		if err == nil {
			return resp, nil
		}
		s.Counters.UpstreamErr.Add(1)
		lastErr = err
	}
	return nil, lastErr
}

// rfc 8484 post wire format
func (s *Server) exchangeDoH(req *dns.Msg, url string) (*dns.Msg, error) {
	req.Id = 0 // fixed id is fine for doh and helps http caches
	body, err := req.Pack()
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/dns-message")
	hreq.Header.Set("Accept", "application/dns-message")
	resp, err := s.doh.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("DoH upstream returned HTTP " + resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if err != nil {
		return nil, err
	}
	m := new(dns.Msg)
	if err := m.Unpack(data); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Server) safeSearch(r *dns.Msg, q dns.Question, target string) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true
	if q.Qtype != dns.TypeA && q.Qtype != dns.TypeAAAA {
		return m // eg https records get nothing and the browser falls back to a and aaaa
	}
	m.Answer = append(m.Answer, &dns.CNAME{
		Hdr:    dns.RR_Header{Name: q.Name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: clientTTL},
		Target: dns.Fqdn(target),
	})
	sub := new(dns.Msg)
	sub.SetQuestion(dns.Fqdn(target), q.Qtype)
	resolved, _ := s.resolve(sub, sub.Question[0])
	if resolved != nil && resolved.Rcode == dns.RcodeSuccess {
		m.Answer = append(m.Answer, resolved.Answer...)
	}
	return m
}
