package catalog

import (
	"bytes"
	"cmp"
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

// one sorted blob instead of a map, 534k domains is under 11mb here vs 50mb as a map which matters on routers.
// domains are found by a byte length each plus a full offset every blockSize, and the few different
// masks that occur live in a palette so each domain only stores a 2 byte index into it
type DomainSet struct {
	blob    []byte
	lens    []uint8
	base    []uint32 // offset of every blockSize'th domain
	idx     []uint16 // into palette
	palette []Mask
	wide    []Mask // only if more than 65536 different masks ever turn up
}

const blockSize = 16

// loading is where routers run out of memory, so domains go into fixed blocks that never get
// copied as they grow, each block of entries is sorted on its own and then they're merged once
// into a set sized exactly. 534k domains peaked at 55mb the old way
type Builder struct {
	arena  [][]byte  // domain bytes, a block is never moved once made
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

func (b *Builder) Add(domain string, m Mask) {
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
	b.arena[last] = append(b.arena[last], domain...)
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
	pal := map[Mask]uint16{}
	b.merge(func(d []byte, m Mask) {
		n++
		size += len(d)
		if _, ok := pal[m]; !ok && len(pal) <= 1<<16 {
			pal[m] = uint16(len(pal))
		}
	})
	s := &DomainSet{
		blob: make([]byte, 0, size),
		lens: make([]uint8, 0, n),
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
	b.merge(func(d []byte, m Mask) {
		if len(s.lens)%blockSize == 0 {
			s.base = append(s.base, uint32(len(s.blob)))
		}
		s.blob = append(s.blob, d...)
		s.lens = append(s.lens, uint8(len(d)))
		if s.wide != nil {
			s.wide = append(s.wide, m)
		} else {
			s.idx = append(s.idx, pal[m])
		}
	})
	b.arena, b.chunks = nil, nil
	return s
}

func (s *DomainSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.lens)
}

func (s *DomainSet) mask(i int) Mask {
	if s.wide != nil {
		return s.wide[i]
	}
	return s.palette[s.idx[i]]
}

// first domain of block b, no adding up needed
func (s *DomainSet) first(b int) []byte {
	o := s.base[b]
	return s.blob[o : o+uint32(s.lens[b*blockSize])]
}

// compare without turning the bytes into a string, which would copy them on every lookup
func compare(b []byte, s string) int {
	for i := range min(len(b), len(s)) {
		if b[i] != s[i] {
			if b[i] < s[i] {
				return -1
			}
			return 1
		}
	}
	return cmp.Compare(len(b), len(s))
}

func (s *DomainSet) at(i int) string {
	o := s.base[i/blockSize]
	for j := i &^ (blockSize - 1); j < i; j++ {
		o += uint32(s.lens[j])
	}
	return string(s.blob[o : o+uint32(s.lens[i])])
}

// binary search the blocks by their first domain, then walk the one block it has to be in
func (s *DomainSet) Get(domain string) Mask {
	if s == nil || len(s.lens) == 0 {
		return 0
	}
	b := sort.Search(len(s.base), func(b int) bool { return compare(s.first(b), domain) > 0 }) - 1
	if b < 0 {
		return 0
	}
	// adding up lengths is cheap, comparing strings isn't, so offsets first then a binary search
	lo, hi := b*blockSize, min(len(s.lens), (b+1)*blockSize)
	var offs [blockSize + 1]uint32
	offs[0] = s.base[b]
	for i := lo; i < hi; i++ {
		offs[i-lo+1] = offs[i-lo] + uint32(s.lens[i])
	}
	l, h := 0, hi-lo
	for l < h {
		m := int(uint(l+h) >> 1)
		switch c := compare(s.blob[offs[m]:offs[m+1]], domain); {
		case c == 0:
			return s.mask(lo + m)
		case c < 0:
			l = m + 1
		default:
			h = m
		}
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
