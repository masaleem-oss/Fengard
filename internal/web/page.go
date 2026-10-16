package web

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// page fills in our few html pages. html/template looks up methods by name with reflect, and that stops
// the linker throwing away unused methods anywhere in the program, 1.8 mb on arm64. this only knows what
// the pages use: {{.A.B}} {{$v := .X}} {{if}} {{else if}} {{else}} {{range}} {{end}} and not, eq, or, call.
// every value gets html escaped unless it's rawHTML
type page struct{ nodes []pnode }

// rawHTML is printed as it is, only for the admins own html
type rawHTML string

type pnode struct {
	text  string
	print *pexpr
	set   string   // variable name for {{$v := ...}}
	conds []*pexpr // if and else if, a nil one is the else
	parts [][]pnode
	rng   *pexpr
	body  []pnode
}

type pexpr struct {
	fn   string // not eq or and call, empty for a plain value
	args []*pexpr
	path []string // ".A.B" is ["", "A", "B"], "$v.A" is ["$v", "A"]
	lit  any
}

func parsePage(src string) (*page, error) {
	p := &pageParser{src: src}
	nodes, end, err := p.list()
	if err != nil {
		return nil, err
	}
	if end != "" {
		return nil, fmt.Errorf("page: unexpected {{%s}}", end)
	}
	return &page{nodes}, nil
}

func mustPage(src string) *page {
	p, err := parsePage(src)
	if err != nil {
		panic(err)
	}
	return p
}

type pageParser struct {
	src string
	pos int
}

// list reads nodes until {{end}} {{else}} or the end, and says which one stopped it
func (p *pageParser) list() ([]pnode, string, error) {
	var out []pnode
	for {
		i := strings.Index(p.src[p.pos:], "{{")
		if i < 0 {
			if p.pos < len(p.src) {
				out = append(out, pnode{text: p.src[p.pos:]})
			}
			p.pos = len(p.src)
			return out, "", nil
		}
		if i > 0 {
			out = append(out, pnode{text: p.src[p.pos : p.pos+i]})
		}
		j := strings.Index(p.src[p.pos+i:], "}}")
		if j < 0 {
			return nil, "", errors.New("page: {{ without }}")
		}
		act := strings.TrimSpace(p.src[p.pos+i+2 : p.pos+i+j])
		p.pos += i + j + 2
		switch {
		case act == "end" || act == "else" || strings.HasPrefix(act, "else if "):
			return out, act, nil
		case strings.HasPrefix(act, "if "):
			n, err := p.ifNode(act[3:])
			if err != nil {
				return nil, "", err
			}
			out = append(out, n)
		case strings.HasPrefix(act, "range "):
			e, err := parseExpr(act[6:])
			if err != nil {
				return nil, "", err
			}
			body, end, err := p.list()
			if err != nil {
				return nil, "", err
			}
			if end != "end" {
				return nil, "", errors.New("page: range without end")
			}
			out = append(out, pnode{rng: e, body: body})
		case strings.HasPrefix(act, "$") && strings.Contains(act, ":="):
			name, rest, _ := strings.Cut(act, ":=")
			e, err := parseExpr(rest)
			if err != nil {
				return nil, "", err
			}
			out = append(out, pnode{set: strings.TrimSpace(name), print: e})
		default:
			e, err := parseExpr(act)
			if err != nil {
				return nil, "", err
			}
			out = append(out, pnode{print: e})
		}
	}
}

func (p *pageParser) ifNode(cond string) (pnode, error) {
	var n pnode
	for {
		e, err := parseExpr(cond)
		if err != nil {
			return n, err
		}
		body, end, err := p.list()
		if err != nil {
			return n, err
		}
		n.conds, n.parts = append(n.conds, e), append(n.parts, body)
		switch {
		case end == "end":
			return n, nil
		case strings.HasPrefix(end, "else if "):
			cond = end[8:]
		case end == "else":
			body, end, err := p.list()
			if err != nil {
				return n, err
			}
			if end != "end" {
				return n, errors.New("page: else without end")
			}
			n.conds, n.parts = append(n.conds, nil), append(n.parts, body)
			return n, nil
		default:
			return n, errors.New("page: if without end")
		}
	}
}

func parseExpr(s string) (*pexpr, error) {
	toks, err := tokens(s)
	if err != nil {
		return nil, err
	}
	e, rest, err := pipeline(toks)
	if err == nil && len(rest) > 0 {
		err = fmt.Errorf("page: left over %q in %q", rest[0], s)
	}
	return e, err
}

func tokens(s string) ([]string, error) {
	var out []string
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(' || c == ')':
			out = append(out, s[i:i+1])
			i++
		case c == '"':
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				return nil, errors.New("page: unclosed string")
			}
			out = append(out, s[i:i+j+2])
			i += j + 2
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\n()", rune(s[j])) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out, nil
}

// a function name and its arguments, or a single value
func pipeline(toks []string) (*pexpr, []string, error) {
	if len(toks) == 0 {
		return nil, nil, errors.New("page: empty action")
	}
	switch toks[0] {
	case "not", "eq", "or", "and", "call":
		e := &pexpr{fn: toks[0]}
		toks = toks[1:]
		for len(toks) > 0 && toks[0] != ")" {
			a, rest, err := term(toks)
			if err != nil {
				return nil, nil, err
			}
			e.args, toks = append(e.args, a), rest
		}
		return e, toks, nil
	}
	return term(toks)
}

func term(toks []string) (*pexpr, []string, error) {
	t := toks[0]
	switch {
	case t == "(":
		e, rest, err := pipeline(toks[1:])
		if err != nil {
			return nil, nil, err
		}
		if len(rest) == 0 || rest[0] != ")" {
			return nil, nil, errors.New("page: missing )")
		}
		return e, rest[1:], nil
	case strings.HasPrefix(t, `"`):
		return &pexpr{lit: t[1 : len(t)-1]}, toks[1:], nil
	case t == ".":
		return &pexpr{path: []string{""}}, toks[1:], nil
	case strings.HasPrefix(t, "."):
		return &pexpr{path: append([]string{""}, strings.Split(t[1:], ".")...)}, toks[1:], nil
	case strings.HasPrefix(t, "$"):
		return &pexpr{path: strings.Split(t, ".")}, toks[1:], nil
	}
	if n, err := strconv.Atoi(t); err == nil {
		return &pexpr{lit: n}, toks[1:], nil
	}
	return nil, nil, fmt.Errorf("page: don't know %q", t)
}

type pageScope struct {
	vars map[string]reflect.Value
}

func (pg *page) Execute(w io.Writer, data any) error {
	var b strings.Builder
	root := reflect.ValueOf(data)
	sc := &pageScope{vars: map[string]reflect.Value{"$": root}}
	if err := sc.run(&b, pg.nodes, root); err != nil {
		return err
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (sc *pageScope) run(b *strings.Builder, nodes []pnode, dot reflect.Value) error {
	for _, n := range nodes {
		switch {
		case n.set != "":
			v, err := sc.eval(n.print, dot)
			if err != nil {
				return err
			}
			sc.vars[n.set] = v
		case n.print != nil:
			v, err := sc.eval(n.print, dot)
			if err != nil {
				return err
			}
			writeValue(b, v)
		case n.conds != nil:
			for i, c := range n.conds {
				ok := c == nil
				if !ok {
					v, err := sc.eval(c, dot)
					if err != nil {
						return err
					}
					ok = truth(v)
				}
				if ok {
					if err := sc.run(b, n.parts[i], dot); err != nil {
						return err
					}
					break
				}
			}
		case n.rng != nil:
			v, err := sc.eval(n.rng, dot)
			if err != nil {
				return err
			}
			v = indirect(v)
			if v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) {
				for i := range v.Len() {
					if err := sc.run(b, n.body, v.Index(i)); err != nil {
						return err
					}
				}
			}
		default:
			b.WriteString(n.text)
		}
	}
	return nil
}

func (sc *pageScope) eval(e *pexpr, dot reflect.Value) (reflect.Value, error) {
	if e.lit != nil {
		return reflect.ValueOf(e.lit), nil
	}
	if e.path != nil {
		v := dot
		if e.path[0] != "" {
			var ok bool
			if v, ok = sc.vars[e.path[0]]; !ok {
				return v, fmt.Errorf("page: no variable %s", e.path[0])
			}
		}
		for _, f := range e.path[1:] {
			v = field(v, f)
		}
		return v, nil
	}
	args := make([]reflect.Value, len(e.args))
	for i, a := range e.args {
		v, err := sc.eval(a, dot)
		if err != nil {
			return v, err
		}
		args[i] = v
	}
	switch e.fn {
	case "not":
		return reflect.ValueOf(len(args) == 1 && !truth(args[0])), nil
	case "eq":
		return reflect.ValueOf(len(args) == 2 && equal(args[0], args[1])), nil
	case "or", "and":
		// like templates these hand back the deciding value
		for _, a := range args {
			if truth(a) == (e.fn == "or") {
				return a, nil
			}
		}
		return args[len(args)-1], nil
	case "call":
		if len(args) == 0 {
			return reflect.Value{}, errors.New("page: call needs a func")
		}
		fn := indirect(args[0])
		if fn.Kind() != reflect.Func || fn.IsNil() {
			return reflect.Value{}, errors.New("page: call of a non func")
		}
		out := fn.Call(args[1:])
		if len(out) == 0 {
			return reflect.Value{}, nil
		}
		return out[0], nil
	}
	return reflect.Value{}, fmt.Errorf("page: unknown %s", e.fn)
}

func indirect(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func field(v reflect.Value, name string) reflect.Value {
	v = indirect(v)
	switch {
	case !v.IsValid():
		return v
	case v.Kind() == reflect.Struct:
		return v.FieldByName(name)
	case v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String:
		return v.MapIndex(reflect.ValueOf(name).Convert(v.Type().Key()))
	}
	return reflect.Value{}
}

func truth(v reflect.Value) bool {
	v = indirect(v)
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return v.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return v.Float() != 0
	case reflect.Func:
		return !v.IsNil()
	}
	return true
}

func equal(a, b reflect.Value) bool {
	a, b = indirect(a), indirect(b)
	if !a.IsValid() || !b.IsValid() {
		return false
	}
	switch {
	case a.CanInt() && b.CanInt():
		return a.Int() == b.Int()
	case a.Kind() == reflect.String && b.Kind() == reflect.String:
		return a.String() == b.String()
	case a.Kind() == reflect.Bool && b.Kind() == reflect.Bool:
		return a.Bool() == b.Bool()
	}
	return false
}

var pageEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;", "\x00", "�")

func writeValue(b *strings.Builder, v reflect.Value) {
	v = indirect(v)
	if !v.IsValid() {
		return
	}
	if v.Type() == reflect.TypeFor[rawHTML]() {
		b.WriteString(v.String())
		return
	}
	var s string
	switch {
	case v.Kind() == reflect.String:
		s = v.String()
	case v.CanInt():
		s = strconv.FormatInt(v.Int(), 10)
	case v.Kind() == reflect.Bool:
		s = strconv.FormatBool(v.Bool())
	default:
		s = fmt.Sprint(v.Interface())
	}
	pageEscaper.WriteString(b, s)
}
