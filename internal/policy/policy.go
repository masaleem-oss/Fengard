package policy

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/config"
)

type Action uint8

const (
	Allow Action = iota
	Block
	SafeSearch
	Paused
	Quarantine
)

func (a Action) String() string {
	return [...]string{"allowed", "blocked", "safesearch", "paused", "quarantined"}[a]
}

type Decision struct {
	Action   Action
	Reason   string // shown on the block page and in logs
	Category string
	Target   string
	Group    string
	GroupID  string
	Device   string
	Person   bool // group is a person so screen time counts
}

type Usage interface {
	Used(groupID, target string) int // minutes and empty target means online at all
	Bonus(groupID string) int
}

type Engine struct {
	mu    sync.Mutex
	cur   atomic.Pointer[compiled]
	usage Usage
}

// call before the first Rebuild
func (e *Engine) SetUsage(u Usage) { e.usage = u }

type compiled struct {
	set          *catalog.DomainSet
	apps         *catalog.AppSet
	lists        []listInfo // in mask bit order
	loc          *time.Location
	defaultGroup *cgroup
	quarantine   bool
	pausedTill   time.Time
	groups       map[string]*cgroup
	devices      map[string]*cdevice
	allow, block set
	temp         []tempRule
	usage        Usage
}

type listInfo struct{ id, name string }

type tempRule struct {
	domain, group string
	until         time.Time
}

type cgroup struct {
	id, name   string
	mask       catalog.Mask
	apps       map[int]struct{}
	allow      set
	block      set
	safe       bool
	schedules  []cschedule
	pausedTill time.Time
	person     bool
	dailyLimit int
	limits     []climit
}

type climit struct {
	target, name string
	app          int // -1 if its not an app
	mask         catalog.Mask
	minutes      int
}

func (l *climit) matches(c *compiled, domain string) bool {
	if l.app >= 0 {
		return c.apps.Lookup(domain) == l.app
	}
	return c.set.Match(domain)&l.mask != 0
}

func (c *compiled) overDaily(g *cgroup) bool {
	return g != nil && g.person && g.dailyLimit > 0 && c.usage != nil &&
		c.usage.Used(g.id, "") >= g.dailyLimit+c.usage.Bonus(g.id)
}

type cschedule struct {
	name       string
	days       uint8 // bit d is weekday d
	start, end int   // minutes since midnight
	blockAll   bool
	mask       catalog.Mask
}

type cdevice struct {
	name       string
	group      *cgroup
	approved   bool
	pausedTill time.Time
}

type set map[string]struct{}

func newSet(list []string) set {
	s := make(set, len(list))
	for _, d := range list {
		s[d] = struct{}{}
	}
	return s
}

func (s set) match(domain string) bool {
	if len(s) == 0 {
		return false
	}
	for d := domain; d != ""; d = catalog.Parent(d) {
		if _, ok := s[d]; ok {
			return true
		}
	}
	return false
}

var appSet = catalog.BuildAppSet()

func New() *Engine {
	e := &Engine{}
	e.Rebuild(config.Default(), &catalog.DomainSet{})
	return e
}

func (e *Engine) Rebuild(cfg *config.Config, ds *catalog.DomainSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	loc, err := time.LoadLocation(cfg.Settings.Timezone)
	if err != nil {
		loc = time.Local
	}
	c := &compiled{
		set:        ds,
		apps:       appSet,
		loc:        loc,
		quarantine: cfg.Settings.QuarantineNew,
		pausedTill: cfg.Settings.PausedTill,
		groups:     map[string]*cgroup{},
		devices:    map[string]*cdevice{},
		allow:      newSet(cfg.Allow),
		block:      newSet(cfg.Block),
		usage:      e.usage,
	}
	for _, l := range cfg.Lists {
		if l.Enabled {
			c.lists = append(c.lists, listInfo{l.ID, l.Name})
		}
	}
	for _, t := range cfg.TempAllow {
		c.temp = append(c.temp, tempRule{t.Domain, t.Group, t.Until})
	}
	for _, g := range cfg.Groups {
		cg := &cgroup{
			id: g.ID, name: g.Name,
			mask:  catalog.MaskOf(g.Categories),
			apps:  map[int]struct{}{},
			allow: newSet(g.Allow), block: newSet(g.Block),
			safe: g.SafeSearch, pausedTill: g.PausedTill,
			person: g.Person, dailyLimit: g.DailyLimit,
		}
		for _, l := range g.AppLimits {
			cl := climit{target: l.Target, name: config.LimitName(l.Target), app: -1, minutes: l.Minutes}
			if id, ok := strings.CutPrefix(l.Target, "app:"); ok {
				cl.app = catalog.AppIndex(id)
			} else if id, ok := strings.CutPrefix(l.Target, "cat:"); ok {
				cl.mask = catalog.MaskOf([]string{id})
			}
			if cl.name != "" {
				cg.limits = append(cg.limits, cl)
			}
		}
		for _, id := range g.Apps {
			if i := catalog.AppIndex(id); i >= 0 {
				cg.apps[i] = struct{}{}
			}
		}
		for _, s := range g.Schedules {
			if !s.Enabled {
				continue
			}
			cs := cschedule{name: s.Name, start: minutes(s.Start), end: minutes(s.End), blockAll: s.BlockAll, mask: catalog.MaskOf(s.Categories)}
			for _, d := range s.Days {
				cs.days |= 1 << d
			}
			cg.schedules = append(cg.schedules, cs)
		}
		c.groups[g.ID] = cg
	}
	c.defaultGroup = c.groups[cfg.Settings.DefaultGroup]
	for _, d := range cfg.Devices {
		g := c.groups[d.Group]
		if g == nil {
			g = c.defaultGroup
		}
		c.devices[d.MAC] = &cdevice{name: d.Name, group: g, approved: d.Approved, pausedTill: d.PausedTill}
	}
	// unlinked vpn peers are their own devices unless theres a device entry for them
	for _, p := range cfg.VPN.Peers {
		if _, listed := c.devices[p.Identity()]; p.Device != "" || listed {
			continue
		}
		g := c.groups[p.Group]
		if g == nil {
			g = c.defaultGroup
		}
		c.devices[p.Identity()] = &cdevice{name: p.Name, group: g, approved: true}
	}
	e.cur.Store(c)
}

// firewall uses this for stuff it also blocks by ip like tor
func (e *Engine) CategoryMACs(id string, extra []string, now time.Time) []string {
	c := e.cur.Load()
	bit := catalog.MaskOf([]string{id})
	if bit == 0 || now.Before(c.pausedTill) {
		return nil
	}
	var out []string
	for mac, d := range c.devices {
		if d.group != nil && d.group.mask&bit != 0 {
			out = append(out, mac)
		}
	}
	if c.defaultGroup != nil && c.defaultGroup.mask&bit != 0 {
		for _, mac := range extra {
			if _, known := c.devices[mac]; !known {
				out = append(out, mac)
			}
		}
	}
	return out
}

func (e *Engine) Targets(domain string) []string {
	c := e.cur.Load()
	var out []string
	if i := c.apps.Lookup(domain); i >= 0 {
		out = append(out, "app:"+catalog.Apps[i].ID)
	}
	if c.set != nil {
		for _, id := range c.set.Match(domain).IDs() {
			out = append(out, "cat:"+id)
		}
	}
	return out
}

func (e *Engine) SetDomains(ds *catalog.DomainSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.cur.Load()
	n := *old
	n.set = ds
	e.cur.Store(&n)
}

func (e *Engine) Paused(now time.Time) bool { return now.Before(e.cur.Load().pausedTill) }

func minutes(hhmm string) int {
	h, _ := strconv.Atoi(hhmm[:2])
	m, _ := strconv.Atoi(hhmm[3:])
	return h*60 + m
}

func (s *cschedule) active(t time.Time) bool {
	day, now := int(t.Weekday()), t.Hour()*60+t.Minute()
	on := func(d int) bool { return s.days&(1<<((d+7)%7)) != 0 }
	if s.start <= s.end {
		return on(day) && now >= s.start && now < s.end
	}
	// crosses midnight so the morning part belongs to the day before
	return (on(day) && now >= s.start) || (on(day-1) && now < s.end)
}

func (c *compiled) device(mac string) (name string, g *cgroup, approved bool, pausedTill time.Time) {
	if d, ok := c.devices[mac]; ok {
		return d.name, d.group, d.approved, d.pausedTill
	}
	// unknown device counts as new but the router itself gets the default group
	return "", c.defaultGroup, mac == "" || !c.quarantine, time.Time{}
}

// hot path keep it alloc free
func (e *Engine) Decide(mac, domain string, now time.Time) Decision {
	c := e.cur.Load()
	name, g, approved, devPaused := c.device(mac)
	return c.decide(g, name, approved, devPaused, domain, now)
}

// for the check a site tool
func (e *Engine) DecideForGroup(groupID, domain string, now time.Time) Decision {
	c := e.cur.Load()
	g := c.groups[groupID]
	if g == nil {
		g = c.defaultGroup
	}
	return c.decide(g, "", true, time.Time{}, domain, now)
}

func (c *compiled) decide(g *cgroup, name string, approved bool, devPaused time.Time, domain string, now time.Time) Decision {
	d := Decision{Device: name}
	if g != nil {
		d.Group, d.GroupID, d.Person = g.name, g.id, g.person
	}
	switch {
	case !approved:
		d.Action, d.Reason = Quarantine, "New device waiting for approval"
		return d
	case now.Before(devPaused):
		d.Action, d.Reason = Paused, "Internet paused for this device"
		return d
	case g != nil && now.Before(g.pausedTill):
		d.Action, d.Reason = Paused, "Internet paused for "+g.name
		return d
	case now.Before(c.pausedTill):
		d.Action, d.Reason = Allow, "Protection paused"
		return d
	case c.overDaily(g):
		d.Action, d.Reason, d.Category = Paused, "Screen time is used up for today", "limit:day"
		return d
	}
	if g != nil && g.person && c.usage != nil {
		for i := range g.limits {
			l := &g.limits[i]
			if l.matches(c, domain) && c.usage.Used(g.id, l.target) >= l.minutes {
				d.Action, d.Reason, d.Category = Block, l.name+" time is used up for today", "limit:"+l.target
				return d
			}
		}
	}

	var extra catalog.Mask
	if g != nil {
		local := now.In(c.loc)
		for i := range g.schedules {
			s := &g.schedules[i]
			if !s.active(local) {
				continue
			}
			if s.blockAll {
				d.Action, d.Reason = Block, "Schedule: "+s.name
				return d
			}
			extra |= s.mask
		}
	}

	if c.allow.match(domain) || (g != nil && g.allow.match(domain)) {
		d.Action, d.Reason = Allow, "Allow rule"
		return d
	}
	for i := range c.temp {
		t := &c.temp[i]
		if now.Before(t.until) && (t.group == "" || (g != nil && t.group == g.id)) && matchDomain(t.domain, domain) {
			d.Action, d.Reason = Allow, "Temporarily allowed until "+t.until.In(c.loc).Format("15:04")
			return d
		}
	}
	if c.block.match(domain) || (g != nil && g.block.match(domain)) {
		d.Action, d.Reason = Block, "Block rule"
		return d
	}
	var mask catalog.Mask
	if g != nil {
		mask = g.mask
	}
	hit := c.set.Match(domain)
	if cats := hit & (mask | extra); cats != 0 {
		id := cats.IDs()[0]
		d.Action, d.Category = Block, id
		d.Reason = catalog.Categories[catalog.Index(id)].Name
		if extra&cats != 0 && mask&cats == 0 {
			d.Reason += " (scheduled)"
		}
		return d
	}
	if g != nil && len(g.apps) > 0 {
		if ai := c.apps.Lookup(domain); ai >= 0 {
			if _, on := g.apps[ai]; on {
				d.Action, d.Category = Block, "app:"+catalog.Apps[ai].ID
				d.Reason = catalog.Apps[ai].Name
				return d
			}
		}
	}
	if li := (hit &^ catalog.CategoryMask).CustomIndex(); li >= 0 && li < len(c.lists) {
		d.Action, d.Category = Block, "list:"+c.lists[li].id
		d.Reason = "Blocklist: " + c.lists[li].name
		return d
	}
	if g != nil && g.safe {
		if t := safeSearchTarget(domain); t != "" {
			d.Action, d.Target, d.Reason = SafeSearch, t, "SafeSearch enforced"
			return d
		}
	}
	d.Action = Allow
	return d
}

func matchDomain(rule, domain string) bool {
	return domain == rule || strings.HasSuffix(domain, "."+rule)
}

func (e *Engine) Offline(now time.Time) (quarantined, offline []string) {
	c := e.cur.Load()
	local := now.In(c.loc)
	for mac, d := range c.devices {
		switch {
		case !d.approved:
			quarantined = append(quarantined, mac)
		case now.Before(d.pausedTill), d.group != nil && now.Before(d.group.pausedTill), c.overDaily(d.group):
			offline = append(offline, mac)
		case d.group != nil:
			for i := range d.group.schedules {
				if s := &d.group.schedules[i]; s.blockAll && s.active(local) {
					offline = append(offline, mac)
					break
				}
			}
		}
	}
	return quarantined, offline
}

func safeSearchTarget(domain string) string {
	switch domain {
	case "www.bing.com", "bing.com":
		return "strict.bing.com"
	case "duckduckgo.com", "www.duckduckgo.com", "start.duckduckgo.com", "html.duckduckgo.com":
		return "safe.duckduckgo.com"
	case "www.youtube.com", "youtube.com", "m.youtube.com", "youtubei.googleapis.com",
		"youtube.googleapis.com", "www.youtube-nocookie.com":
		return "restrict.youtube.com"
	}
	// covers country domains like www.google.com.au
	d := strings.TrimPrefix(domain, "www.")
	if tld, ok := strings.CutPrefix(d, "google."); ok && tld != "" && strings.Count(tld, ".") <= 1 && len(tld) <= 6 {
		return "forcesafesearch.google.com"
	}
	return ""
}
