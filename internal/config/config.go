package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/catalog"
)

type Config struct {
	Version  int       `json:"version"`
	Updated  time.Time `json:"updated"`
	Settings Settings  `json:"settings"`

	Groups       []Group       `json:"groups"`
	Devices      []Device      `json:"devices"`
	Block        []string      `json:"block"`
	Allow        []string      `json:"allow"`
	TempAllow    []TempAllow   `json:"tempAllow"`
	Lists        []CustomList  `json:"lists"`
	Records      []DNSRecord   `json:"records"`
	PortForwards []PortForward `json:"portForwards"`
	Channels     []Channel     `json:"channels"`
	VPN          VPN           `json:"vpn"`
}

type Settings struct {
	BoxName         string    `json:"boxName"`
	Upstreams       []string  `json:"upstreams"`
	DNSSEC          bool      `json:"dnssec"`
	LocalZone       string    `json:"localZone"`
	DefaultGroup    string    `json:"defaultGroup"`
	QuarantineNew   bool      `json:"quarantineNew"`
	BlockBypass     bool      `json:"blockBypass"`
	LogRetentionDay int       `json:"logRetentionDays"`
	ClientRateQPS   int       `json:"clientRateQps"`
	Timezone        string    `json:"timezone"`
	AutoUpdate      bool      `json:"autoUpdate"`   // install new releases overnight
	RequireHTTPS    bool      `json:"requireHttps"` // off means plain http still works from the lan
	WelcomePage     bool      `json:"welcomePage"`  // new iphones see a page when they join
	WelcomeSince    time.Time `json:"welcomeSince,omitzero"`
	SpeedTestOff    bool      `json:"speedTestOff,omitempty"` // skips the nightly speed test
	PortalHTML      string    `json:"portalHtml,omitempty"`   // shown on the captive portal instead of the default
	PausedTill      time.Time `json:"protectionPausedUntil,omitzero"`
}

type Group struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Categories []string   `json:"categories"`
	Apps       []string   `json:"apps"`
	Block      []string   `json:"block"`
	Allow      []string   `json:"allow"`
	SafeSearch bool       `json:"safeSearch"`
	Schedules  []Schedule `json:"schedules"`
	PausedTill time.Time  `json:"pausedUntil,omitzero"`

	// screen time counts across all their devices
	Person     bool       `json:"person"`
	DailyLimit int        `json:"dailyLimit"` // 0 means no limit
	AppLimits  []AppLimit `json:"appLimits"`
}

type AppLimit struct {
	Target  string `json:"target"`
	Minutes int    `json:"minutes"`
}

func LimitName(target string) string {
	id, ok := strings.CutPrefix(target, "app:")
	if ok {
		if i := catalog.AppIndex(id); i >= 0 {
			return catalog.Apps[i].Name
		}
		return ""
	}
	if id, ok := strings.CutPrefix(target, "cat:"); ok {
		if i := catalog.Index(id); i >= 0 {
			return catalog.Categories[i].Name
		}
	}
	return ""
}

// windows can cross midnight
type Schedule struct {
	Name       string   `json:"name"`
	Days       []int    `json:"days"` // 0 is sunday
	Start      string   `json:"start"`
	End        string   `json:"end"`
	BlockAll   bool     `json:"blockAll"`
	Categories []string `json:"categories"`
	Enabled    bool     `json:"enabled"`
}

type Device struct {
	MAC        string    `json:"mac"`
	Name       string    `json:"name"`
	Hostname   string    `json:"hostname,omitempty"`
	Group      string    `json:"group"`
	Approved   bool      `json:"approved"`
	FirstSeen  time.Time `json:"firstSeen"`
	PausedTill time.Time `json:"pausedUntil,omitzero"`
	Presence   bool      `json:"presence,omitempty"` // alerts when it arrives or leaves
}

type TempAllow struct {
	Domain string    `json:"domain"`
	Group  string    `json:"group,omitempty"` // empty means every group
	Until  time.Time `json:"until"`
	By     string    `json:"by,omitempty"`
}

type CustomList struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type DNSRecord struct {
	Name  string `json:"name"` // zone gets appended if theres no dot
	Type  string `json:"type"`
	Value string `json:"value"`
}

type PortForward struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Proto    string `json:"proto"`
	ExtPort  int    `json:"extPort"`
	DestIP   string `json:"destIp"`
	DestPort int    `json:"destPort"`
	Enabled  bool   `json:"enabled"`
}

type VPN struct {
	Enabled    bool      `json:"enabled"`
	Port       int       `json:"port"`
	Subnet     string    `json:"subnet"`   // gateway is the first address in the subnet
	Endpoint   string    `json:"endpoint"` // empty means detect
	PrivateKey string    `json:"privateKey,omitempty"`
	PublicKey  string    `json:"publicKey,omitempty"`
	Peers      []VPNPeer `json:"peers"`
}

type VPNPeer struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	PublicKey    string    `json:"publicKey"`
	PrivateKey   string    `json:"privateKey,omitempty"` // kept so the qr code can be shown again
	PresharedKey string    `json:"presharedKey,omitempty"`
	IP           string    `json:"ip"`
	Device       string    `json:"device,omitempty"` // mac of the lan device its the same phone as
	Group        string    `json:"group,omitempty"`
	Created      time.Time `json:"created"`
	Enabled      bool      `json:"enabled"`
}

var identityRe = regexp.MustCompile(`^(vpn|ts):[a-z0-9][a-z0-9._-]{0,63}$`)

func NormIdentity(s string) (string, error) {
	s = strings.TrimSpace(s)
	if hw, err := net.ParseMAC(s); err == nil {
		return hw.String(), nil
	}
	if s = strings.ToLower(s); identityRe.MatchString(s) {
		return s, nil
	}
	return "", fmt.Errorf("invalid device address %q", s)
}

func (p VPNPeer) Identity() string {
	if p.Device != "" {
		return p.Device
	}
	return "vpn:" + p.ID
}

type Channel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	Token    string `json:"token,omitempty"`
	ChatID   string `json:"chatId,omitempty"`
	MinLevel string `json:"minLevel"`
	Enabled  bool   `json:"enabled"`
}

func Default() *Config {
	return &Config{
		Settings: Settings{
			BoxName:         "Fengard",
			Upstreams:       []string{"1.1.1.1:53", "9.9.9.9:53"},
			LocalZone:       "lan",
			DefaultGroup:    "default",
			BlockBypass:     true,
			LogRetentionDay: 14,
			ClientRateQPS:   100,
			Timezone:        "Local",
		},
		Groups: []Group{
			{ID: "default", Name: "Everyone", Categories: []string{"malware"}},
			{ID: "kids", Name: "Kids", Categories: []string{"malware", "adult", "gambling", "bypass"}, SafeSearch: true},
			{ID: "iot", Name: "Smart home", Categories: []string{"malware", "ads"}},
		},
	}
}

func (c *Config) Clone() *Config {
	n := *c
	n.Groups = make([]Group, len(c.Groups))
	for i, g := range c.Groups {
		g.Categories, g.Apps = clone(g.Categories), clone(g.Apps)
		g.Block, g.Allow = clone(g.Block), clone(g.Allow)
		g.AppLimits = append([]AppLimit(nil), c.Groups[i].AppLimits...)
		g.Schedules = make([]Schedule, len(c.Groups[i].Schedules))
		for j, s := range c.Groups[i].Schedules {
			s.Days, s.Categories = cloneInts(s.Days), clone(s.Categories)
			g.Schedules[j] = s
		}
		n.Groups[i] = g
	}
	n.Devices = append([]Device(nil), c.Devices...)
	n.Block, n.Allow = clone(c.Block), clone(c.Allow)
	n.TempAllow = append([]TempAllow(nil), c.TempAllow...)
	n.Lists = append([]CustomList(nil), c.Lists...)
	n.Records = append([]DNSRecord(nil), c.Records...)
	n.PortForwards = append([]PortForward(nil), c.PortForwards...)
	n.Channels = append([]Channel(nil), c.Channels...)
	n.VPN.Peers = append([]VPNPeer(nil), c.VPN.Peers...)
	n.Settings.Upstreams = clone(c.Settings.Upstreams)
	return &n
}

func clone(s []string) []string { return append([]string{}, s...) }
func cloneInts(s []int) []int   { return append([]int{}, s...) }

func (c *Config) Group(id string) *Group {
	for i := range c.Groups {
		if c.Groups[i].ID == id {
			return &c.Groups[i]
		}
	}
	return nil
}

func (c *Config) Device(mac string) *Device {
	for i := range c.Devices {
		if c.Devices[i].MAC == mac {
			return &c.Devices[i]
		}
	}
	return nil
}

var (
	idRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	timeRe  = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	zoneRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	labelRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?$`)
)

// every slice comes out non nil so json clients never see null
func (c *Config) Validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }

	s := &c.Settings
	if s.BoxName = strings.TrimSpace(s.BoxName); s.BoxName == "" {
		s.BoxName = "Fengard"
	}
	if len(s.BoxName) > 40 {
		bad("network name must be at most 40 characters")
	}
	if s.Upstreams == nil {
		s.Upstreams = []string{}
	}
	if len(s.Upstreams) == 0 {
		bad("at least one upstream DNS server is required")
	}
	for i, u := range s.Upstreams {
		s.Upstreams[i] = strings.TrimSpace(u)
		if _, err := ParseUpstream(s.Upstreams[i]); err != nil {
			bad("upstream %q: %v", u, err)
		}
	}
	s.LocalZone = strings.Trim(strings.ToLower(strings.TrimSpace(s.LocalZone)), ".")
	if s.LocalZone == "" {
		s.LocalZone = "lan"
	}
	if !zoneRe.MatchString(s.LocalZone) || s.LocalZone == "local" {
		bad("local zone %q must be a single label like lan or home (not local, which mDNS uses)", s.LocalZone)
	}
	if s.LogRetentionDay < 1 || s.LogRetentionDay > 365 {
		bad("log retention must be 1-365 days")
	}
	if s.ClientRateQPS < 5 || s.ClientRateQPS > 10000 {
		bad("per-device rate limit must be 5-10000 queries/second")
	}
	if s.Timezone == "" {
		s.Timezone = "Local"
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		bad("unknown timezone %q", s.Timezone)
	}

	if c.Groups == nil {
		c.Groups = []Group{}
	}
	ids := map[string]bool{}
	for i := range c.Groups {
		g := &c.Groups[i]
		if !idRe.MatchString(g.ID) {
			bad("group id %q must be lowercase letters, digits or dashes", g.ID)
		}
		if ids[g.ID] {
			bad("duplicate group id %q", g.ID)
		}
		ids[g.ID] = true
		if g.Name = strings.TrimSpace(g.Name); g.Name == "" {
			bad("group %q needs a name", g.ID)
		}
		g.Categories = checkCats(g.Categories, bad)
		g.Apps = checkApps(g.Apps, bad)
		g.Block, g.Allow = normList(g.Block, bad), normList(g.Allow, bad)
		if g.DailyLimit < 0 || g.DailyLimit > 24*60 {
			bad("profile %q: daily limit must be 0-1440 minutes", g.Name)
		}
		if g.AppLimits == nil {
			g.AppLimits = []AppLimit{}
		}
		seenLimit := map[string]bool{}
		for j := range g.AppLimits {
			l := &g.AppLimits[j]
			l.Target = strings.ToLower(strings.TrimSpace(l.Target))
			if LimitName(l.Target) == "" {
				bad("profile %q: unknown app or category %q in a time limit", g.Name, l.Target)
			}
			if seenLimit[l.Target] {
				bad("profile %q: %s has two time limits", g.Name, l.Target)
			}
			seenLimit[l.Target] = true
			if l.Minutes < 1 || l.Minutes > 24*60 {
				bad("profile %q: time limit for %s must be 1-1440 minutes", g.Name, l.Target)
			}
		}
		if g.Schedules == nil {
			g.Schedules = []Schedule{}
		}
		for j := range g.Schedules {
			sc := &g.Schedules[j]
			sc.Name = strings.TrimSpace(sc.Name)
			if !timeRe.MatchString(sc.Start) || !timeRe.MatchString(sc.End) {
				bad("schedule %q: times must be HH:MM", sc.Name)
			}
			if sc.Days == nil {
				sc.Days = []int{}
			}
			if len(sc.Days) == 0 {
				bad("schedule %q: pick at least one day", sc.Name)
			}
			for _, d := range sc.Days {
				if d < 0 || d > 6 {
					bad("schedule %q: day %d out of range", sc.Name, d)
				}
			}
			sc.Categories = checkCats(sc.Categories, bad)
			if !sc.BlockAll && len(sc.Categories) == 0 {
				bad("schedule %q: block everything or pick categories", sc.Name)
			}
		}
	}
	if !ids[s.DefaultGroup] {
		bad("default group %q does not exist", s.DefaultGroup)
	}

	if c.Devices == nil {
		c.Devices = []Device{}
	}
	macs := map[string]bool{}
	for i := range c.Devices {
		d := &c.Devices[i]
		id, err := NormIdentity(d.MAC)
		if err != nil {
			bad("device MAC %q is invalid", d.MAC)
			continue
		}
		d.MAC = id
		if macs[d.MAC] {
			bad("duplicate device %s", d.MAC)
		}
		macs[d.MAC] = true
		if !ids[d.Group] {
			bad("device %s is in unknown group %q", d.MAC, d.Group)
		}
		d.Name = strings.TrimSpace(d.Name)
	}

	c.Block, c.Allow = normList(c.Block, bad), normList(c.Allow, bad)

	v := &c.VPN
	if v.Port == 0 {
		v.Port = 51820
	}
	if v.Port < 1 || v.Port > 65535 {
		bad("VPN port must be 1-65535")
	}
	if v.Subnet = strings.TrimSpace(v.Subnet); v.Subnet == "" {
		v.Subnet = "10.66.0.0/24"
	}
	_, vnet, err := net.ParseCIDR(v.Subnet)
	if err != nil || vnet.IP.To4() == nil {
		bad("VPN subnet %q must be an IPv4 network like 10.66.0.0/24", v.Subnet)
	} else if ones, _ := vnet.Mask.Size(); ones > 29 {
		bad("VPN subnet %q is too small", v.Subnet)
	} else {
		v.Subnet = vnet.String()
	}
	v.Endpoint = strings.TrimSpace(v.Endpoint)
	if len(v.Endpoint) > 253 {
		bad("VPN endpoint is too long")
	}
	if v.Peers == nil {
		v.Peers = []VPNPeer{}
	}
	peerIDs, peerIPs := map[string]bool{}, map[string]bool{}
	for i := range v.Peers {
		p := &v.Peers[i]
		if !idRe.MatchString(p.ID) || peerIDs[p.ID] {
			bad("VPN device id %q is invalid or duplicated", p.ID)
		}
		peerIDs[p.ID] = true
		if p.Name = strings.TrimSpace(p.Name); p.Name == "" || len(p.Name) > 40 {
			bad("VPN device %q needs a name of up to 40 characters", p.ID)
		}
		if p.PublicKey == "" || p.PrivateKey == "" {
			bad("VPN device %q is missing its keys", p.Name)
		}
		ip := net.ParseIP(p.IP)
		if ip == nil || vnet == nil || !vnet.Contains(ip) || peerIPs[p.IP] {
			bad("VPN device %q has an invalid or duplicate address %q", p.Name, p.IP)
		}
		peerIPs[p.IP] = true
		if p.Device != "" {
			hw, err := net.ParseMAC(p.Device)
			if err != nil {
				bad("VPN device %q is linked to an invalid MAC %q", p.Name, p.Device)
			} else {
				p.Device = hw.String()
			}
		}
		if p.Group != "" && !ids[p.Group] {
			bad("VPN device %q is in unknown group %q", p.Name, p.Group)
		}
	}

	// expired temp rules get dropped on every save
	now := time.Now()
	kept := []TempAllow{}
	for _, t := range c.TempAllow {
		t.Domain = NormDomain(t.Domain)
		if !catalog.ValidDomain(t.Domain) {
			bad("temporary rule: invalid domain %q", t.Domain)
			continue
		}
		if t.Group != "" && !ids[t.Group] {
			bad("temporary rule for %s: unknown group %q", t.Domain, t.Group)
			continue
		}
		if t.Until.After(now) {
			kept = append(kept, t)
		}
	}
	c.TempAllow = kept

	if c.Lists == nil {
		c.Lists = []CustomList{}
	}
	if len(c.Lists) > catalog.MaxCustomLists {
		bad("at most %d custom blocklists are supported", catalog.MaxCustomLists)
	}
	listIDs := map[string]bool{}
	for i := range c.Lists {
		l := &c.Lists[i]
		if l.ID == "" {
			l.ID = NewID()
		}
		if listIDs[l.ID] {
			bad("duplicate list id %q", l.ID)
		}
		listIDs[l.ID] = true
		l.Name = strings.TrimSpace(l.Name)
		l.URL = strings.TrimSpace(l.URL)
		u, err := url.Parse(l.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			bad("blocklist %q: the URL must start with http:// or https://", l.Name)
		}
		if l.Name == "" && err == nil && u != nil {
			l.Name = u.Host
		}
	}

	if c.Records == nil {
		c.Records = []DNSRecord{}
	}
	recSeen := map[string]bool{}
	for i := range c.Records {
		r := &c.Records[i]
		r.Name = NormDomain(r.Name)
		r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
		r.Value = strings.TrimSpace(r.Value)
		if r.Name == "" || !hostnameOK(r.Name) {
			bad("DNS record: invalid name %q", r.Name)
			continue
		}
		if !strings.Contains(r.Name, ".") {
			r.Name += "." + s.LocalZone
		}
		key := r.Name + "/" + r.Type
		if recSeen[key] {
			bad("DNS record: %s already has an %s record", r.Name, r.Type)
		}
		recSeen[key] = true
		switch r.Type {
		case "A":
			if ip := net.ParseIP(r.Value); ip == nil || ip.To4() == nil {
				bad("DNS record %s: %q is not an IPv4 address", r.Name, r.Value)
			}
		case "AAAA":
			if ip := net.ParseIP(r.Value); ip == nil || ip.To4() != nil {
				bad("DNS record %s: %q is not an IPv6 address", r.Name, r.Value)
			}
		case "CNAME":
			r.Value = NormDomain(r.Value)
			if !catalog.ValidDomain(r.Value) || r.Value == r.Name {
				bad("DNS record %s: %q is not a valid target", r.Name, r.Value)
			}
		default:
			bad("DNS record %s: type must be A, AAAA or CNAME", r.Name)
		}
	}

	if c.PortForwards == nil {
		c.PortForwards = []PortForward{}
	}
	ports := map[string]bool{}
	for i := range c.PortForwards {
		p := &c.PortForwards[i]
		if p.ID == "" {
			p.ID = NewID()
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Proto != "tcp" && p.Proto != "udp" && p.Proto != "both" {
			bad("port forward %q: protocol must be tcp, udp or both", p.Name)
		}
		if p.ExtPort < 1 || p.ExtPort > 65535 || p.DestPort < 1 || p.DestPort > 65535 {
			bad("port forward %q: ports must be 1-65535", p.Name)
		}
		ip := net.ParseIP(p.DestIP)
		if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
			bad("port forward %q: destination must be a private IPv4 address", p.Name)
		}
		if p.Enabled {
			for _, proto := range protos(p.Proto) {
				k := fmt.Sprintf("%s/%d", proto, p.ExtPort)
				if ports[k] {
					bad("port %s is forwarded twice", k)
				}
				ports[k] = true
			}
		}
	}

	if c.Channels == nil {
		c.Channels = []Channel{}
	}
	chIDs := map[string]bool{}
	for i := range c.Channels {
		ch := &c.Channels[i]
		if ch.ID == "" {
			ch.ID = NewID()
		}
		if chIDs[ch.ID] {
			bad("duplicate channel id %q", ch.ID)
		}
		chIDs[ch.ID] = true
		ch.Name = strings.TrimSpace(ch.Name)
		ch.URL, ch.Token, ch.ChatID = strings.TrimSpace(ch.URL), strings.TrimSpace(ch.Token), strings.TrimSpace(ch.ChatID)
		if ch.MinLevel == "" {
			ch.MinLevel = "warning"
		}
		if ch.MinLevel != "info" && ch.MinLevel != "warning" && ch.MinLevel != "critical" {
			bad("channel %q: level must be info, warning or critical", ch.Name)
		}
		switch ch.Type {
		case "webhook", "discord", "slack", "ntfy":
			if u, err := url.Parse(ch.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				bad("channel %q: enter a full http(s) URL", ch.Name)
			}
		case "telegram":
			if ch.Token == "" || ch.ChatID == "" {
				bad("channel %q: Telegram needs a bot token and chat ID", ch.Name)
			}
			if _, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+ch.Token+"/sendMessage", nil); err != nil || strings.ContainsAny(ch.Token, "/?#&% \r\n") {
				bad("channel %q: invalid Telegram credentials", ch.Name)
			}
		default:
			bad("channel %q: unknown type %q", ch.Name, ch.Type)
		}
		if ch.Name == "" {
			ch.Name = ch.Type
		}
	}
	return errors.Join(errs...)
}

func protos(p string) []string {
	if p == "both" {
		return []string{"tcp", "udp"}
	}
	return []string{p}
}

func checkCats(ids []string, bad func(string, ...any)) []string {
	out := []string{}
	for _, id := range ids {
		if catalog.Index(id) < 0 {
			bad("unknown category %q", id)
			continue
		}
		out = append(out, id)
	}
	return out
}

func checkApps(ids []string, bad func(string, ...any)) []string {
	out := []string{}
	for _, id := range ids {
		if catalog.AppIndex(id) < 0 {
			bad("unknown app %q", id)
			continue
		}
		out = append(out, id)
	}
	return out
}

func normList(in []string, bad func(string, ...any)) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, d := range in {
		d = NormDomain(d)
		if !catalog.ValidDomain(d) {
			bad("invalid domain %q", d)
			continue
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func hostnameOK(h string) bool {
	if len(h) > 253 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if l == "" || len(l) > 63 || !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

func NormDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if h, _, err := net.SplitHostPort(d); err == nil {
		d = h
	}
	return strings.TrimSuffix(d, ".")
}

func NewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type Upstream struct {
	Net     string
	Addr    string
	TLSName string
	URL     string
}

func ParseUpstream(s string) (Upstream, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return Upstream{}, errors.New("invalid DNS-over-HTTPS URL")
		}
		return Upstream{Net: "https", URL: s}, nil
	}
	if rest, ok := strings.CutPrefix(s, "tls://"); ok {
		addr, name, _ := strings.Cut(rest, "@")
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "853")
		}
		if name == "" {
			return Upstream{}, errors.New("DNS-over-TLS needs a server name, e.g. tls://1.1.1.1:853@cloudflare-dns.com")
		}
		return Upstream{Net: "tcp-tls", Addr: addr, TLSName: name}, nil
	}
	addr := s
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "53")
	}
	host, _, _ := net.SplitHostPort(addr)
	if net.ParseIP(host) == nil {
		return Upstream{}, errors.New("must be an IP address")
	}
	return Upstream{Net: "udp", Addr: addr}, nil
}
