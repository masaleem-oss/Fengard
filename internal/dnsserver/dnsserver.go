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
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sync/singleflight"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/devices"
	h3 "github.com/masaleem-oss/Fengard/internal/fengard-h3"
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

	BlockIP       net.IP // points at this box
	BlockIPv6     net.IP
	LocalNames    []string       // eg fengard.lan
	Allowed       []netip.Prefix // empty means private ranges
	Screen        ScreenTime
	Welcome       func(mac, hostname string) bool        // true sends this phones captive check to us
	TrustLoopback bool                                   // on the router loopback is the router itself
	Note          func(mac, domain string, at time.Time) // what each device looks up, for recognizing it later

	upstreams atomic.Pointer[[]config.Upstream]
	ipv4Only  atomic.Pointer[[]netip.Prefix] // no aaaa for these since vpn tunnels only carry ipv4
	bypass    atomic.Bool                    // answer the firefox doh and icloud relay canaries
	dnssec    atomic.Bool
	boot      atomic.Pointer[[]string] // plain dns for looking up doh server names
	doh       *http.Client
	doh3      atomic.Bool
	parallel  atomic.Bool
	h3        *h3.Client
	h3Health  sync.Map // url to *h3Health
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
	DoH3        atomic.Uint64 // answered over http/3
	DoH3Fell    atomic.Uint64 // http/3 failed and http/2 took over
}

const (
	maxInflight  = 512
	cacheEntries = 20000 // about 10 mb worst case
	globalQPS    = 20000 // backstop for all clients
	maxClients   = 4096
	clientTTL    = 60 // short so rule changes apply within a minute
	blockTTL     = 10
)

var defaultBoot = []string{"9.9.9.9:53", "1.1.1.1:53", "8.8.8.8:53"}

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
	if s.boot.Load() == nil {
		s.boot.Store(&defaultBoot)
	}
	// doh names like dns.quad9.net cant be looked up through ourselves, that needs doh working first
	boot := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		var err error
		for _, a := range *s.boot.Load() {
			var c net.Conn
			if c, err = d.DialContext(ctx, network, a); err == nil {
				return c, nil
			}
		}
		return nil, err
	}}
	dial := &net.Dialer{Timeout: 5 * time.Second, Resolver: boot}
	s.h3 = &h3.Client{TLSConfig: &tls.Config{}, Resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return boot.LookupNetIP(ctx, "ip", host)
	}}
	s.doh = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext:  dial.DialContext,
		MaxIdleConns: 8, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second,
		// a custom tls config turns http/2 off and quad9 refuses doh over http/1.1
		ForceAttemptHTTP2: true,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		// a connection whose route went away otherwise hangs every lookup until tcp gives up, about 15 min
		HTTP2: &http.HTTP2Config{SendPingTimeout: 10 * time.Second, PingTimeout: 3 * time.Second},
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
	// plain upstreams the user picked go first for looking up doh names
	var boot []string
	for _, u := range ups {
		if u.Net == "udp" {
			boot = append(boot, u.Addr)
		}
	}
	boot = append(boot, defaultBoot...)
	s.boot.Store(&boot)
	s.bypass.Store(set.BlockBypass)
	s.dnssec.Store(set.DNSSEC)
	// off means no connections and nothing held in memory
	if !set.DoH3 && s.doh3.Swap(false) && s.h3 != nil {
		go s.h3.Close()
	}
	s.doh3.Store(set.DoH3)
	s.parallel.Store(set.Parallel)
	s.warm()
	if s.perClient != nil {
		s.perClient.SetRate(float64(set.ClientRateQPS), float64(set.ClientRateQPS*3))
	}
}

func (s *Server) FlushCache() { s.cache.flush() }

// NetworkChanged drops the resolver connections after an interface came up or went down. a vpn client
// moving the default route leaves them sending out the old way where nothing answers
func (s *Server) NetworkChanged() {
	if t, ok := s.doh.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	if s.h3 != nil {
		s.h3.Reset()
	}
	s.h3Health.Range(func(_, v any) bool {
		h := v.(*h3Health)
		h.mu.Lock()
		h.waitUntil = time.Time{}
		h.mu.Unlock()
		return true
	})
	s.warm()
}

// the first lookup shouldn't have to wait for the handshake, asking all at once uses every one
func (s *Server) warm() {
	if !s.doh3.Load() || s.h3 == nil {
		return
	}
	for _, u := range *s.upstreams.Load() {
		if u.Net == "https" {
			go s.h3.Warm(u.URL)
			if !s.parallel.Load() {
				break
			}
		}
	}
}

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
	// udp sockets open first so a port someone else holds fails straight away
	var conns []net.PacketConn
	closeAll := func() {
		for _, c := range conns {
			c.Close()
		}
	}
	for _, addr := range addrs {
		for range readers {
			c, err := listenUDP(ctx, addr, readers > 1)
			if err != nil {
				closeAll()
				return err
			}
			conns = append(conns, c)
		}
	}
	var servers []*dns.Server
	for _, addr := range addrs {
		servers = append(servers, &dns.Server{
			Addr: addr, Net: "tcp", Handler: s,
			ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
			// cap idle tcp conns so they cant be hoarded
			IdleTimeout:   func() time.Duration { return 8 * time.Second },
			MaxTCPQueries: 100,
		})
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, len(servers)+len(conns))
	slots := make(chan struct{}, maxInFlight)
	for _, c := range conns {
		go func() { errc <- s.serveUDP(ctx, c, slots) }()
	}
	for _, srv := range servers {
		go func() { errc <- srv.ListenAndServe() }()
	}
	var err error
	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	cancel()
	closeAll()
	for _, srv := range servers {
		srv.Shutdown()
	}
	return err
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
	// a short ttl so the phone checks again right after it joins
	if domain == "captive.apple.com" && s.Welcome != nil && s.Welcome(info.MAC, info.Hostname) {
		w.WriteMsg(s.pointAtUs(r, q, 1))
		return
	}
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
	if s.Note != nil && info.MAC != "" {
		s.Note(info.MAC, domain, start)
	}
	// the routers own lookups (tailscale, updates, ntp) never get filtered, only it can send from loopback
	if s.TrustLoopback && ip.IsLoopback() {
		dec = policy.Decision{Action: policy.Allow, Reason: "The router itself", Device: dec.Device}
	}

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
	if len(ups) > 1 && s.parallel.Load() {
		return s.exchangeAll(req, ups)
	}
	var lastErr error
	for _, u := range ups {
		resp, err := s.ask(req, u)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// exchangeAll asks every upstream at once for people whose servers are safe but slow or flaky. the
// first real answer wins, a quick SERVFAIL only counts if nobody does better. the slower ones finish
// in the background within their own timeouts
func (s *Server) exchangeAll(req *dns.Msg, ups []config.Upstream) (*dns.Msg, error) {
	type result struct {
		resp *dns.Msg
		err  error
	}
	results := make(chan result, len(ups))
	for _, u := range ups {
		q := req.Copy()
		go func() {
			r, err := s.ask(q, u)
			results <- result{r, err}
		}()
	}
	var fallback *dns.Msg
	var lastErr error
	for range ups {
		r := <-results
		switch {
		case r.err != nil:
			lastErr = r.err
		case r.resp.Rcode == dns.RcodeServerFailure:
			fallback = r.resp
		default:
			return r.resp, nil
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, lastErr
}

func (s *Server) ask(req *dns.Msg, u config.Upstream) (*dns.Msg, error) {
	s.Counters.Upstream.Add(1)
	var resp *dns.Msg
	var err error
	if u.Net == "https" {
		resp, err = s.exchangeDoH(req, u.URL)
	} else {
		c := &dns.Client{Net: u.Net, Timeout: 2 * time.Second}
		if u.Net == "tcp-tls" {
			c.TLSConfig = &tls.Config{ServerName: u.TLSName, MinVersion: tls.VersionTLS12}
			c.Timeout = 4 * time.Second
		}
		resp, _, err = c.Exchange(req, u.Addr)
		if err == nil && resp.Truncated && u.Net == "udp" {
			c.Net = "tcp"
			resp, _, err = c.Exchange(req, u.Addr)
		}
	}
	if err != nil {
		s.Counters.UpstreamErr.Add(1)
	}
	return resp, err
}

// rfc 8484 post wire format
func (s *Server) exchangeDoH(req *dns.Msg, url string) (*dns.Msg, error) {
	req.Id = 0 // fixed id is fine for doh and helps http caches
	body, err := req.Pack()
	if err != nil {
		return nil, err
	}
	if s.doh3.Load() {
		if m, err := s.exchangeDoH3(body, url); err == nil {
			return m, nil
		}
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

// h3Health remembers how http/3 is going for one server so a path that blocks udp only costs one timeout
type h3Health struct {
	mu        sync.Mutex
	worked    bool
	fails     int
	round     int // bumped on every failure so lookups that shared one bad connection count once
	probing   bool
	waitUntil time.Time
	lastErr   string
	lastOK    time.Time
}

const (
	h3FirstTry   = 1500 * time.Millisecond // handshake and answer, a server that's never answered
	h3Try        = 2 * time.Second
	h3MinBackoff = 30 * time.Second
	h3MaxBackoff = 30 * time.Minute
)

func (s *Server) health(url string) *h3Health {
	h, _ := s.h3Health.LoadOrStore(url, &h3Health{})
	return h.(*h3Health)
}

func (s *Server) exchangeDoH3(body []byte, url string) (*dns.Msg, error) {
	h := s.health(url)
	h.mu.Lock()
	now := time.Now()
	// after a failure one lookup at a time finds out whether http/3 is back, the rest use http/2 meanwhile
	if now.Before(h.waitUntil) || h.probing {
		h.mu.Unlock()
		return nil, errors.New("http/3 backing off")
	}
	probe := h.fails > 0
	h.probing = probe
	round := h.round
	wait := h3FirstTry
	if h.worked {
		wait = h3Try
	}
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	data, err := s.h3.Exchange(ctx, url, body)
	var m *dns.Msg
	if err == nil {
		m = new(dns.Msg)
		err = m.Unpack(data)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if probe {
		h.probing = false
	}
	if err != nil {
		s.Counters.DoH3Fell.Add(1)
		if h.round != round {
			return nil, err // another lookup on the same connection already counted this
		}
		h.round++
		h.fails++
		h.lastErr = err.Error()
		// one slow answer from a server that works is the resolver, not http/3
		if !h.worked || h.fails >= 2 {
			h.waitUntil = time.Now().Add(min(h3MinBackoff<<min(h.fails-1, 10), h3MaxBackoff))
		}
		return nil, err
	}
	s.Counters.DoH3.Add(1)
	h.worked, h.fails, h.lastErr, h.lastOK = true, 0, "", time.Now()
	return m, nil
}

// Transport is how a dns-over-https upstream is being reached
type Transport struct {
	URL       string    `json:"url"`
	Using     string    `json:"using"` // HTTP/3, HTTP/2, or empty before the first lookup
	Error     string    `json:"error,omitempty"`
	RetryAt   time.Time `json:"retryAt,omitzero"`
	LastHTTP3 time.Time `json:"lastHttp3,omitzero"`
}

func (s *Server) Transports() []Transport {
	// an empty list, not null, when no resolver is DoH
	out := []Transport{}
	for _, u := range *s.upstreams.Load() {
		if u.Net != "https" {
			continue
		}
		t := Transport{URL: u.URL, Using: "HTTP/2"}
		if s.doh3.Load() {
			h := s.health(u.URL)
			h.mu.Lock()
			switch {
			case h.fails > 0:
				t.Error, t.RetryAt = strings.TrimPrefix(h.lastErr, "h3: "), h.waitUntil
			case h.worked:
				t.Using = "HTTP/3"
			default:
				t.Using = ""
			}
			t.LastHTTP3 = h.lastOK
			h.mu.Unlock()
		}
		out = append(out, t)
	}
	return out
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
