package catalog

import (
	"sort"
	"strings"
)

// bits 0 to 15 are categories and 16 to 31 are custom lists
type Mask uint32

const (
	customShift    = 16
	MaxCustomLists = 16
	CategoryMask   = Mask(1)<<customShift - 1
)

func CustomBit(i int) Mask { return Mask(1) << (customShift + i) }

func (m Mask) CustomIndex() int {
	for i := range MaxCustomLists {
		if m&CustomBit(i) != 0 {
			return i
		}
	}
	return -1
}

// one sorted blob instead of a map 500k domains is 14mb vs 50mb which matters on routers
type DomainSet struct {
	blob  []byte
	offs  []uint32 // end is the next offset
	masks []Mask
}

// slice not map to keep peak memory down while loading
type Builder struct {
	entries []entry
	size    int
}

type entry struct {
	d string
	m Mask
}

func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) Add(domain string, m Mask) {
	b.entries = append(b.entries, entry{domain, m})
	b.size += len(domain)
}

func (b *Builder) Build() *DomainSet {
	e := b.entries
	sort.Slice(e, func(i, j int) bool { return e[i].d < e[j].d })
	s := &DomainSet{
		blob:  make([]byte, 0, b.size),
		offs:  make([]uint32, 0, len(e)+1),
		masks: make([]Mask, 0, len(e)),
	}
	for i := 0; i < len(e); i++ {
		// last domain runs to the end of blob
		if n := len(s.masks); n > 0 && string(s.blob[s.offs[n-1]:]) == e[i].d {
			s.masks[n-1] |= e[i].m // same domain from several lists
			continue
		}
		s.offs = append(s.offs, uint32(len(s.blob)))
		s.blob = append(s.blob, e[i].d...)
		s.masks = append(s.masks, e[i].m)
	}
	s.offs = append(s.offs, uint32(len(s.blob)))
	b.entries = nil
	return s
}

func (s *DomainSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.masks)
}

func (s *DomainSet) at(i int) string {
	return string(s.blob[s.offs[i]:s.offs[i+1]])
}

func (s *DomainSet) Get(domain string) Mask {
	if s == nil || len(s.masks) == 0 {
		return 0
	}
	i := sort.Search(len(s.masks), func(i int) bool {
		return string(s.blob[s.offs[i]:s.offs[i+1]]) >= domain
	})
	if i < len(s.masks) && s.at(i) == domain {
		return s.masks[i]
	}
	return 0
}

func (s *DomainSet) Match(domain string) Mask {
	var m Mask
	for d := domain; d != ""; d = Parent(d) {
		m |= s.Get(d)
	}
	return m
}

func Parent(d string) string {
	i := strings.IndexByte(d, '.')
	if i < 0 {
		return ""
	}
	return d[i+1:]
}
