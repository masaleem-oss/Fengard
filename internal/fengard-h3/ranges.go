package h3

// pnRange is an inclusive run of packet numbers
type pnRange struct{ lo, hi uint64 }

// maxAckRanges caps what we remember, anything older than the oldest range counts as already seen
const maxAckRanges = 32

// received keeps the packet numbers we got, newest range first, for duplicate checks and ack frames
type received struct {
	r []pnRange
}

func (s *received) has(pn uint64) bool {
	if len(s.r) == 0 {
		return false
	}
	if pn < s.r[len(s.r)-1].lo && len(s.r) == maxAckRanges {
		return true // forgotten, treat as a duplicate
	}
	for _, g := range s.r {
		if pn > g.hi {
			return false
		}
		if pn >= g.lo {
			return true
		}
	}
	return false
}

func (s *received) add(pn uint64) {
	for i, g := range s.r {
		switch {
		case pn >= g.lo && pn <= g.hi:
			return
		case pn == g.hi+1:
			s.r[i].hi = pn
			return
		case pn == g.lo-1 && g.lo > 0:
			s.r[i].lo = pn
			// joins the next older range
			if i+1 < len(s.r) && s.r[i+1].hi+1 == pn {
				s.r[i].lo = s.r[i+1].lo
				s.r = append(s.r[:i+1], s.r[i+2:]...)
			}
			return
		case pn > g.hi:
			s.r = append(s.r, pnRange{})
			copy(s.r[i+1:], s.r[i:])
			s.r[i] = pnRange{pn, pn}
			s.trim()
			return
		}
	}
	s.r = append(s.r, pnRange{pn, pn})
	s.trim()
}

func (s *received) trim() {
	if len(s.r) > maxAckRanges {
		s.r = s.r[:maxAckRanges]
	}
}

func (s *received) largest() uint64 { return s.r[0].hi }

// appendAck writes an ack frame, delay is already scaled by the ack delay exponent
func (s *received) appendAck(b []byte, delay uint64) []byte {
	b = append(b, 0x02)
	b = appendVarint(b, s.r[0].hi)
	b = appendVarint(b, delay)
	b = appendVarint(b, uint64(len(s.r)-1))
	b = appendVarint(b, s.r[0].hi-s.r[0].lo)
	for i := 1; i < len(s.r); i++ {
		b = appendVarint(b, s.r[i-1].lo-s.r[i].hi-2)
		b = appendVarint(b, s.r[i].hi-s.r[i].lo)
	}
	return b
}

// parseAck reads an ack frame after its type into ranges, newest first
func parseAck(r *reader, ecn bool, out []pnRange) ([]pnRange, uint64, error) {
	largest := r.varint()
	delay := r.varint()
	count := r.varint()
	first := r.varint()
	if r.err != nil {
		return nil, 0, r.err
	}
	if first > largest {
		return nil, 0, transportErr(errFrameEncoding, "bad ack range")
	}
	smallest := largest - first
	out = append(out[:0], pnRange{smallest, largest})
	for range count {
		gap := r.varint()
		n := r.varint()
		if r.err != nil {
			return nil, 0, r.err
		}
		if gap+2 > smallest {
			return nil, 0, transportErr(errFrameEncoding, "bad ack gap")
		}
		hi := smallest - gap - 2
		if n > hi {
			return nil, 0, transportErr(errFrameEncoding, "bad ack range")
		}
		smallest = hi - n
		// only the newest few ranges matter, the rest are long since settled
		if len(out) < 64 {
			out = append(out, pnRange{smallest, hi})
		}
	}
	if ecn {
		r.varint()
		r.varint()
		r.varint()
	}
	return out, delay, r.err
}
