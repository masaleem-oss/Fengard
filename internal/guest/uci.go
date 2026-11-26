// package guest sets up the guest wi-fi on the router and puts the router back when its turned off.
// every change goes through uci, and what it changed is written down first as uci batch lines
// so turning it off and the uninstaller undo exactly that
package guest

import (
	"slices"
	"sort"
	"strings"
)

// Section is one uci section with its options, lists keep every value
type Section struct {
	Name, Type string
	Opts       map[string][]string
}

func (s *Section) Get(opt string) string {
	if s == nil || len(s.Opts[opt]) == 0 {
		return ""
	}
	return s.Opts[opt][0]
}

func (s *Section) Has(opt string) bool {
	if s == nil {
		return false
	}
	_, ok := s.Opts[opt]
	return ok
}

// Package is one uci config file like wireless, sections in file order
type Package struct {
	Name     string
	Sections []*Section
}

func (p *Package) Section(name string) *Section {
	if p == nil {
		return nil
	}
	for _, s := range p.Sections {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (p *Package) OfType(typ string) []*Section {
	if p == nil {
		return nil
	}
	var out []*Section
	for _, s := range p.Sections {
		if s.Type == typ {
			out = append(out, s)
		}
	}
	return out
}

// ParseShow reads uci -X show output for one package. values come quoted like the shell,
// so 'it'\”s' is one value and 'a' 'b' is a list of two
func ParseShow(pkg string, out []byte) *Package {
	p := &Package{Name: pkg}
	for _, line := range strings.Split(string(out), "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		parts := strings.SplitN(key, ".", 3)
		if len(parts) < 2 || parts[0] != pkg || parts[1] == "" {
			continue
		}
		switch len(parts) {
		case 2:
			if p.Section(parts[1]) == nil {
				p.Sections = append(p.Sections, &Section{Name: parts[1], Type: strings.Trim(val, "'"), Opts: map[string][]string{}})
			}
		case 3:
			s := p.Section(parts[1])
			if s == nil {
				s = &Section{Name: parts[1], Opts: map[string][]string{}}
				p.Sections = append(p.Sections, s)
			}
			s.Opts[parts[2]] = words(val)
		}
	}
	return p
}

// shell style words, quotes join and a backslash escapes outside them
func words(s string) []string {
	var out []string
	var cur strings.Builder
	in, started := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case in && c == '\'':
			in = false
		case in:
			cur.WriteByte(c)
		case c == '\'':
			in, started = true, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			started = true
		case c == ' ' || c == '\t':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

// Op is one uci batch command, Key is like wireless.guest2g.ssid
type Op struct {
	Cmd   string `json:"cmd"` // set delete add_list del_list
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

func (o Op) Pkg() string { p, _, _ := strings.Cut(o.Key, "."); return p }

func (o Op) String() string {
	if o.Cmd == "delete" {
		return "delete " + o.Key
	}
	return o.Cmd + " " + o.Key + "=" + quote(o.Value)
}

func quote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

func set(key, val string) Op     { return Op{Cmd: "set", Key: key, Value: val} }
func del(key string) Op          { return Op{Cmd: "delete", Key: key} }
func addList(key, val string) Op { return Op{Cmd: "add_list", Key: key, Value: val} }
func delList(key, val string) Op { return Op{Cmd: "del_list", Key: key, Value: val} }

// Batch is the ops as uci batch input with a commit for every package they touch
func Batch(ops []Op) string {
	var b strings.Builder
	pkgs := map[string]bool{}
	for _, o := range ops {
		b.WriteString(o.String() + "\n")
		pkgs[o.Pkg()] = true
	}
	var names []string
	for p := range pkgs {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		b.WriteString("commit " + p + "\n")
	}
	return b.String()
}

// Packages the ops change
func Packages(ops []Op) []string {
	var out []string
	for _, o := range ops {
		if p := o.Pkg(); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Router is the router's uci config fengard plans from
type Router struct {
	Network, Wireless, Firewall, DHCP *Package
}

func (r Router) pkg(name string) *Package {
	switch name {
	case "network":
		return r.Network
	case "wireless":
		return r.Wireless
	case "firewall":
		return r.Firewall
	case "dhcp":
		return r.DHCP
	}
	return nil
}

// Applicable drops undo ops that would fail now, like deleting a section someone already removed.
// one failing line would stop uci batch and leave the rest of the router unrestored
func (r Router) Applicable(ops []Op) []Op {
	var out []Op
	for _, o := range ops {
		parts := strings.SplitN(o.Key, ".", 3)
		if len(parts) < 2 {
			continue
		}
		s := r.pkg(parts[0]).Section(parts[1])
		switch {
		case o.Cmd == "set" && len(parts) == 2:
		case s == nil:
			continue
		case o.Cmd == "delete" && len(parts) == 3 && !s.Has(parts[2]):
			continue
		case o.Cmd == "del_list" && (len(parts) < 3 || !slices.Contains(s.Opts[parts[2]], o.Value)):
			continue
		}
		out = append(out, o)
	}
	return out
}

// Missing is the ops that didn't take, read back after a batch. uci batch can stop early and
// still say it worked
func (r Router) Missing(ops []Op) []Op {
	var out []Op
	for _, o := range ops {
		parts := strings.SplitN(o.Key, ".", 3)
		if len(parts) < 2 {
			continue
		}
		s := r.pkg(parts[0]).Section(parts[1])
		ok := true
		switch {
		case o.Cmd == "set" && len(parts) == 2:
			ok = s != nil && s.Type == o.Value
		case o.Cmd == "delete" && len(parts) == 2:
			ok = s == nil
		case s == nil:
			ok = false
		case o.Cmd == "set":
			ok = s.Has(parts[2]) && strings.Join(s.Opts[parts[2]], " ") == o.Value
		case o.Cmd == "add_list":
			ok = slices.Contains(s.Opts[parts[2]], o.Value)
		case o.Cmd == "delete":
			ok = !s.Has(parts[2])
		case o.Cmd == "del_list":
			ok = !slices.Contains(s.Opts[parts[2]], o.Value)
		}
		if !ok {
			out = append(out, o)
		}
	}
	return out
}
