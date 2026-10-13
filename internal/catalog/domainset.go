package catalog

import (
	"bytes"
	"slices"
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

// one sorted blob instead of a map. domains are kept back to front (moc.elpmaxe.sda) so neighbours share
// their endings, and each one only stores the part that differs from the one before it: how many bytes
// it shares, how many follow, then those bytes. every blockSize'th domain is stored whole with its offset
// in base, so a lookup binary searches those then reads one block. masks live in a palette and each
// domain keeps a 2 byte index into it. 534k domains took 50mb as a map
type DomainSet struct {
	blob    []byte
	base    []uint32 // offset of every blockSize'th domain, which is stored whole
	n       int
	idx     []uint16 // into palette
	palette []Mask
	wide    []Mask // only if more than 65536 different masks ever turn up
}

const blockSize = 16

// loading is where routers run out of memory, so domains go into fixed blocks that never get
// copied as they grow, each block of entries is sorted on its own and then they're merged once
// into a set sized exactly. 534k domains peaked at 55mb the old way
type Builder struct {
	arena  [][]byte  // domain bytes back to front, a block is never moved once made
	chunks [][]entry // sorted one by one in Build
}

const (
	arenaShift = 18 // a block holds 256kb so an offset fits in the low 18 bits
	arenaMax   = 1 << arenaShift
	chunkMax   = 1 << 15
)

// 12 bytes, a string and mask were 24 plus the string's own allocation
type entry struct {
	off uint32 // block index above arenaShift, offset below
	m   Mask
	n   uint8
}

func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) Add(domain string, m Mask) { add(b, domain, m) }

// AddBytes copies d, so it can come straight from a reused read buffer
func (b *Builder) AddBytes(d []byte, m Mask) { add(b, d, m) }

func add[T ~string | ~[]byte](b *Builder, domain T, m Mask) {
	if len(domain) == 0 || len(domain) > 255 {
		return
	}
	// blocks start small and double so the app set and tests don't pay for 256kb
	last := len(b.arena) - 1
	if last < 0 || len(b.arena[last])+len(domain) > cap(b.arena[last]) {
		b.arena = append(b.arena, make([]byte, 0, grow(len(b.arena), 4096, arenaMax)))
		last++
	}
	off := uint32(last)<<arenaShift | uint32(len(b.arena[last]))
	// back to front so sorting puts a site and its subdomains next to each other
	b.arena[last] = append(b.arena[last], domain...)
	slices.Reverse(b.arena[last][len(b.arena[last])-len(domain):])
	c := len(b.chunks) - 1
	if c < 0 || len(b.chunks[c]) == cap(b.chunks[c]) {
		b.chunks = append(b.chunks, make([]entry, 0, grow(len(b.chunks), 64, chunkMax)))
		c++
	}
	b.chunks[c] = append(b.chunks[c], entry{off, m, uint8(len(domain))})
}

// grow doubles from small up to most, without shifting so far that the size wraps to 0 on 32 bit
func grow(n, small, most int) int {
	for ; n > 0 && small < most; n-- {
		small *= 2
	}
	return min(small, most)
}

func (b *Builder) bytes(e entry) []byte {
	o := e.off & (arenaMax - 1)
	return b.arena[e.off>>arenaShift][o : o+uint32(e.n)]
}

// merge hands over every domain once in order with the masks of all its copies or'd together
func (b *Builder) merge(emit func(d []byte, m Mask)) {
	pos := make([]int, len(b.chunks))
	var heap []int // chunk indexes, smallest current domain first
	less := func(i, j int) bool {
		return bytes.Compare(b.bytes(b.chunks[heap[i]][pos[heap[i]]]), b.bytes(b.chunks[heap[j]][pos[heap[j]]])) < 0
	}
	down := func(i int) {
		for {
			l, s := 2*i+1, i
			if l < len(heap) && less(l, s) {
				s = l
			}
			if r := l + 1; r < len(heap) && less(r, s) {
				s = r
			}
			if s == i {
				return
			}
			heap[i], heap[s] = heap[s], heap[i]
			i = s
		}
	}
	for i, ch := range b.chunks {
		if len(ch) > 0 {
			heap = append(heap, i)
		}
	}
	for i := len(heap)/2 - 1; i >= 0; i-- {
		down(i)
	}
	var prev []byte
	var acc Mask
	have := false
	for len(heap) > 0 {
		c := heap[0]
		e := b.chunks[c][pos[c]]
		d := b.bytes(e)
		if have && bytes.Equal(d, prev) {
			acc |= e.m // same domain from several lists
		} else {
			if have {
				emit(prev, acc)
			}
			prev, acc, have = d, e.m, true
		}
		if pos[c]++; pos[c] == len(b.chunks[c]) {
			heap[0] = heap[len(heap)-1]
			heap = heap[:len(heap)-1]
		}
		down(0)
	}
	if have {
		emit(prev, acc)
	}
}

func (b *Builder) Build() *DomainSet {
	for _, ch := range b.chunks {
		slices.SortFunc(ch, func(x, y entry) int { return bytes.Compare(b.bytes(x), b.bytes(y)) })
	}
	// count first so the set is allocated once at its final size
	n, size := 0, 0
	var prev []byte
	pal := map[Mask]uint16{}
	b.merge(func(d []byte, m Mask) {
		if n%blockSize == 0 {
			size += 1 + len(d)
		} else {
			size += 2 + len(d) - shared(prev, d)
		}
		prev = d
		n++
		if _, ok := pal[m]; !ok && len(pal) <= 1<<16 {
			pal[m] = uint16(len(pal))
		}
	})
	s := &DomainSet{
		blob: make([]byte, 0, size),
		base: make([]uint32, 0, (n+blockSize-1)/blockSize),
	}
	if len(pal) > 1<<16 {
		s.wide = make([]Mask, 0, n)
	} else {
		s.idx = make([]uint16, 0, n)
		s.palette = make([]Mask, len(pal))
		for m, i := range pal {
			s.palette[i] = m
		}
	}
	prev = nil
	b.merge(func(d []byte, m Mask) {
		if s.n%blockSize == 0 {
			s.base = append(s.base, uint32(len(s.blob)))
			s.blob = append(s.blob, byte(len(d)))
			s.blob = append(s.blob, d...)
		} else {
			k := shared(prev, d)
			s.blob = append(s.blob, byte(k), byte(len(d)-k))
			s.blob = append(s.blob, d[k:]...)
		}
		prev = d
		s.n++
		if s.wide != nil {
			s.wide = append(s.wide, m)
		} else {
			s.idx = append(s.idx, pal[m])
		}
	})
	b.arena, b.chunks = nil, nil
	return s
}

// how many bytes two domains start with in common
func shared(a, b []byte) int {
	k := 0
	for k < len(a) && k < len(b) && a[k] == b[k] {
		k++
	}
	return k
}

func (s *DomainSet) Len() int {
	if s == nil {
		return 0
	}
	return s.n
}

func (s *DomainSet) mask(i int) Mask {
	if s.wide != nil {
		return s.wide[i]
	}
	return s.palette[s.idx[i]]
}

// first domain of block b as it's stored, back to front
func (s *DomainSet) first(b int) []byte {
	o := s.base[b]
	return s.blob[o+1 : o+1+uint32(s.blob[o])]
}

// walk hands fn each domain of block b in order, back to front, until fn says stop. cur lives on the
// stack and each one is built from the one before, so a lookup allocates nothing
func (s *DomainSet) walk(b int, fn func(i int, d []byte) bool) {
	var cur [255]byte
	o := s.base[b]
	n := int(s.blob[o])
	copy(cur[:], s.blob[o+1:o+1+uint32(n)])
	o += 1 + uint32(n)
	for i := b * blockSize; i < min(s.n, (b+1)*blockSize); i++ {
		if i > b*blockSize {
			k, rest := int(s.blob[o]), int(s.blob[o+1])
			copy(cur[k:], s.blob[o+2:o+2+uint32(rest)])
			n = k + rest
			o += 2 + uint32(rest)
		}
		if !fn(i, cur[:n]) {
			return
		}
	}
}

// at is the i'th domain the right way round, for tests
func (s *DomainSet) at(i int) string {
	var out string
	s.walk(i/blockSize, func(j int, d []byte) bool {
		if j < i {
			return true
		}
		r := make([]byte, len(d))
		for k := range d {
			r[len(d)-1-k] = d[k]
		}
		out = string(r)
		return false
	})
	return out
}

// binary search the blocks by their first domain, then read the one block it has to be in
func (s *DomainSet) Get(domain string) Mask {
	if s == nil || s.n == 0 || len(domain) == 0 || len(domain) > 255 {
		return 0
	}
	var buf [255]byte
	key := buf[:len(domain)]
	for i := range len(domain) {
		key[len(domain)-1-i] = domain[i]
	}
	b := sort.Search(len(s.base), func(b int) bool { return bytes.Compare(s.first(b), key) > 0 }) - 1
	if b < 0 {
		return 0
	}
	// walk without a closure, which would move cur and key off the stack. match is how much of key the
	// domain before matched. a domain sharing less than that with the one before has gone past key, one
	// sharing more is still before it, so only a domain sharing exactly match needs comparing
	var cur [255]byte
	o := s.base[b]
	n := int(s.blob[o])
	copy(cur[:], s.blob[o+1:o+1+uint32(n)])
	o += 1 + uint32(n)
	match := 0
	for i := b * blockSize; i < min(s.n, (b+1)*blockSize); i++ {
		if i > b*blockSize {
			k, rest := int(s.blob[o]), int(s.blob[o+1])
			copy(cur[k:], s.blob[o+2:o+2+uint32(rest)])
			n = k + rest
			o += 2 + uint32(rest)
			if k < match {
				return 0
			}
			if k > match {
				continue
			}
		}
		l := match
		for l < n && l < len(key) && cur[l] == key[l] {
			l++
		}
		switch {
		case l == n && l == len(key):
			return s.mask(i)
		case l == len(key), l < n && cur[l] > key[l]:
			return 0 // past it
		}
		match = l
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
