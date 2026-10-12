package h3

import "slices"

const (
	connWindow     = 1 << 20  // what the server may send before we give more credit
	streamWindow   = 80 << 10 // per request stream, a dns answer is at most 64 KB
	uniWindow      = 16 << 10 // control and qpack streams, topped up as they're read
	peerUniStreams = 16
	maxBuffered    = 64 // out of order pieces per stream
)

type streamKind uint8

const (
	kindRequest streamKind = iota
	kindControl            // ours
	kindPeerUni            // the server's, type not known yet
	kindPeerControl
	kindDiscard // qpack streams and anything unknown, read and thrown away
)

type segment struct {
	off  uint64
	data []byte
}

type stream struct {
	id   uint64
	kind streamKind
	req  *request

	// sending
	out       []byte
	fin       bool   // out is everything
	sent      uint64 // first byte never sent
	finSent   bool
	lost      []segRng // to send again
	lostFin   bool
	acked     []segRng
	finAcked  bool
	reset     bool // we reset it, nothing more goes out
	peerLimit uint64

	// receiving
	in        []byte    // contiguous data not yet consumed
	inOff     uint64    // stream offset of the end of in
	pending   []segment // arrived early
	highest   uint64
	finalSize int64 // -1 until known
	limit     uint64
	peerReset bool
	typeRead  bool // first byte of a server uni stream
}

type segRng struct{ off, n uint64 }

func newStream(id uint64, kind streamKind, limit uint64) *stream {
	return &stream{id: id, kind: kind, limit: limit, finalSize: -1}
}

// receive takes stream data, it returns how many new bytes count against connection flow control
func (s *stream) receive(off uint64, data []byte, fin bool) (uint64, error) {
	end := off + uint64(len(data))
	if end > s.limit {
		return 0, transportErr(errFlowControl, "stream data over the limit")
	}
	if s.finalSize >= 0 && end > uint64(s.finalSize) {
		return 0, transportErr(errFinalSize, "data after the final size")
	}
	if fin {
		if s.finalSize >= 0 && uint64(s.finalSize) != end || end < s.highest {
			return 0, transportErr(errFinalSize, "final size changed")
		}
		s.finalSize = int64(end)
	}
	var grew uint64
	if end > s.highest {
		grew = end - s.highest
		s.highest = end
	}
	if s.peerReset || end <= s.inOff {
		return grew, nil
	}
	if off <= s.inOff {
		s.in = append(s.in, data[s.inOff-off:]...)
		s.inOff = end
		s.drainPending()
		return grew, nil
	}
	if len(s.pending) >= maxBuffered {
		return grew, nil // dropped, the server sends it again
	}
	i, _ := slices.BinarySearchFunc(s.pending, off, func(g segment, o uint64) int { return cmpU(g.off, o) })
	s.pending = slices.Insert(s.pending, i, segment{off, append([]byte(nil), data...)})
	return grew, nil
}

func (s *stream) drainPending() {
	for len(s.pending) > 0 {
		g := s.pending[0]
		if g.off > s.inOff {
			return
		}
		if end := g.off + uint64(len(g.data)); end > s.inOff {
			s.in = append(s.in, g.data[s.inOff-g.off:]...)
			s.inOff = end
		}
		s.pending = s.pending[1:]
	}
}

func (s *stream) complete() bool { return s.finalSize >= 0 && s.inOff == uint64(s.finalSize) }

// nextSend gives the next piece to send, lost data first then new data within the server's limits
func (s *stream) nextSend(room int, peerLimit, connRoom uint64) (off uint64, data []byte, fin, isNew, ok bool) {
	if s.reset || room <= 0 {
		return
	}
	total := uint64(len(s.out))
	if len(s.lost) > 0 {
		l := &s.lost[0]
		n := min(l.n, uint64(room))
		off, data = l.off, s.out[l.off:l.off+n]
		l.off += n
		l.n -= n
		if l.n == 0 {
			s.lost = s.lost[1:]
		}
		fin = s.fin && off+n == total && !s.finAcked
		if fin {
			s.lostFin = false
		}
		return off, data, fin, false, true
	}
	if s.lostFin {
		s.lostFin = false
		return total, nil, true, false, true
	}
	limit := max(min(peerLimit, s.sent+connRoom), s.sent)
	n := min(total-s.sent, uint64(room), limit-s.sent)
	finNow := s.fin && !s.finSent && s.sent+n == total
	if n == 0 && !finNow {
		return
	}
	off, data = s.sent, s.out[s.sent:s.sent+n]
	s.sent += n
	if finNow {
		s.finSent = true
	}
	return off, data, finNow, true, true
}

func (s *stream) onLost(off, n uint64, fin bool) {
	if s.reset {
		return
	}
	if fin && !s.finAcked {
		s.lostFin = true
	}
	if n == 0 {
		return
	}
	for _, a := range s.acked {
		if off >= a.off && off+n <= a.off+a.n {
			return
		}
	}
	s.lost = append(s.lost, segRng{off, n})
}

func (s *stream) onAcked(off, n uint64, fin bool) {
	if fin {
		s.finAcked = true
	}
	if n == 0 {
		return
	}
	// kept sorted and merged, request streams usually have one piece
	s.acked = append(s.acked, segRng{off, n})
	slices.SortFunc(s.acked, func(a, b segRng) int { return cmpU(a.off, b.off) })
	merged := s.acked[:1]
	for _, a := range s.acked[1:] {
		last := &merged[len(merged)-1]
		if a.off <= last.off+last.n {
			last.n = max(last.off+last.n, a.off+a.n) - last.off
			continue
		}
		merged = append(merged, a)
	}
	s.acked = merged
}

// sendDone is true once the server has everything we wrote
func (s *stream) sendDone() bool {
	return s.reset || s.fin && s.finAcked && (len(s.out) == 0 || len(s.acked) == 1 && s.acked[0].off == 0 && s.acked[0].n == uint64(len(s.out)))
}

func (s *stream) wantsSend() bool {
	return !s.reset && (len(s.lost) > 0 || s.lostFin || s.sent < uint64(len(s.out)) || s.fin && !s.finSent)
}

func cmpU(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
