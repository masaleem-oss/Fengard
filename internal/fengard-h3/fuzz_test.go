package h3

import (
	"crypto/tls"
	"testing"
	"time"
)

// everything here reads bytes straight off the internet, none of it may panic or hang

func FuzzHeader(f *testing.F) {
	f.Add([]byte{0xc3, 0, 0, 0, 1, 8, 1, 2, 3, 4, 5, 6, 7, 8, 0, 0, 0x40, 0x20})
	f.Add([]byte{0x43, 1, 2, 3, 4, 5, 6, 7, 8, 9, 9, 9, 9})
	f.Add([]byte{0xf0, 0, 0, 0, 1, 0, 8, 1, 2, 3, 4, 5, 6, 7, 8})
	c, _ := initialKeys([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Fuzz(func(t *testing.T, d []byte) {
		for len(d) > 0 {
			h, err := parseHeader(d, 8)
			if err != nil {
				return
			}
			if h.length <= 0 || h.length > len(d) || h.pnOffset > h.length {
				t.Fatalf("bad bounds %+v for %d bytes", h, len(d))
			}
			pkt := append([]byte(nil), d[:h.length]...)
			if h.pnOffset < len(pkt) {
				if n, err := unmask(pkt, h.pnOffset, c, h.long); err == nil {
					hdrEnd := h.pnOffset + n
					c.open(nil, decodePN(-1, truncatedPN(pkt, h.pnOffset, n), n), pkt[:hdrEnd], pkt[hdrEnd:])
				}
			}
			d = d[h.length:]
		}
	})
}

func FuzzParams(f *testing.F) {
	f.Add(appendOurParams(nil, []byte{1, 2, 3, 4}))
	f.Add([]byte{0x00, 0x04, 1, 2, 3, 4, 0x0f, 0x00, 0x02, 0x10, 0x04, 0x01, 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		parseParams(b)
	})
}

func FuzzAck(f *testing.F) {
	var r received
	for _, pn := range []uint64{1, 2, 3, 7, 8, 20, 50, 51} {
		r.add(pn)
	}
	f.Add(r.appendAck(nil, 5)[1:], false)
	f.Add([]byte{0x05, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00}, true)
	f.Fuzz(func(t *testing.T, b []byte, ecn bool) {
		rd := reader{b: b}
		ranges, _, err := parseAck(&rd, ecn, nil)
		if err != nil {
			return
		}
		for i, g := range ranges {
			if g.lo > g.hi || i > 0 && g.hi >= ranges[i-1].lo {
				t.Fatalf("ranges out of order: %v", ranges)
			}
		}
	})
}

func FuzzQPACK(f *testing.F) {
	req := encodeRequest("dns.example", "/dns-query", []byte{1, 2, 3})
	f.Add(req)
	f.Add([]byte{0x01, 0x03, 0x00, 0x00, 0xd9, 0x00, 0x02, 0xab, 0xcd})
	f.Add([]byte{0x01, 0x0a, 0x00, 0x00, 0x5f, 0x1d, 0x05, 'h', 'e', 'l', 'l', 'o', 0xd9})
	f.Fuzz(func(t *testing.T, b []byte) {
		readResponse(b)
		decodeHeaders(b)
	})
}

// fuzzConn is a connection mid conversation with no network under it
func fuzzConn(tb testing.TB) *Conn {
	c := newConn("fuzz:443", "fuzz", 443, 0, &tls.Config{InsecureSkipVerify: true}, nil)
	if err := c.startTLS(); err != nil {
		tb.Fatal(err)
	}
	c.handshakeDone, c.confirmed = true, true
	c.applyParams(params{maxData: 1 << 20, maxStreamBidi: 1 << 16, maxStreamUni: 1 << 16, maxStreamsBidi: 100, maxStreamsUni: 100, ackDelayExp: 3, maxAckDelay: 25 * time.Millisecond, cidLimit: 2})
	c.send[spApp], c.recv[spApp] = initialKeys([]byte{9, 9, 9, 9, 9, 9, 9, 9})
	c.recvNext = c.recv[spApp].next()
	for i := range 3 {
		r := &request{data: encodeRequest("fuzz", "/q", []byte{byte(i)}), done: make(chan result, 1)}
		c.queue(r)
	}
	c.sp[spApp].next = 50
	return c
}

func FuzzFrames(f *testing.F) {
	var r received
	r.add(3)
	f.Add(append(r.appendAck(nil, 0), 0x01))
	resp := appendFrame(nil, frameHeaders, []byte{0x00, 0x00, 0xd9})
	resp = appendFrame(resp, frameData, []byte{1, 2, 3})
	stream := appendVarint(appendVarint(appendVarint([]byte{0x0f}, 0), 0), uint64(len(resp)))
	f.Add(append(stream, resp...))
	ctrl := appendFrame([]byte{0x00}, frameSettings, []byte{0x06, 0x40, 0x64})
	f.Add(append(appendVarint(appendVarint(appendVarint([]byte{0x0f}, 3), 0), uint64(len(ctrl))), ctrl...))
	f.Add([]byte{0x18, 0x01, 0x00, 0x08, 1, 2, 3, 4, 5, 6, 7, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0x1a, 1, 2, 3, 4, 5, 6, 7, 8, 0x10, 0x44, 0x00, 0x1e})
	f.Add([]byte{0x04, 0x00, 0x00, 0x10, 0x05, 0x04, 0x00, 0x11, 0x00, 0x50, 0x00})
	f.Fuzz(func(t *testing.T, b []byte) {
		c := fuzzConn(t)
		now := time.Now()
		if _, err := c.frames(spApp, b, now); err == nil && !c.closing {
			c.flush(now) // whatever the frames queued has to go out without trouble
		}
		c.tls.Close()
	})
}

func FuzzStreamReassembly(f *testing.F) {
	f.Add([]byte{0, 5, 1, 3, 4, 0, 2, 9})
	f.Fuzz(func(t *testing.T, plan []byte) {
		data := []byte("the quick brown fox jumps over the lazy dog, then does it again")
		s := newStream(0, kindRequest, 1<<16)
		// plan bytes pick pieces to deliver, the stream has to end up exact whatever the order
		for i := 0; i+1 < len(plan); i += 2 {
			off := int(plan[i]) % len(data)
			n := int(plan[i+1]) % (len(data) - off + 1)
			if _, err := s.receive(uint64(off), data[off:off+n], off+n == len(data)); err != nil {
				return
			}
		}
		s.receive(0, data, true)
		if !s.complete() || string(s.in) != string(data) {
			t.Fatalf("reassembled %q", s.in)
		}
	})
}

// a client still waiting for the server's first packet, with connection ids the fuzzer can match
func handshakeConn(tb testing.TB) *Conn {
	c := newConn("fuzz:443", "fuzz", 443, 0, &tls.Config{InsecureSkipVerify: true}, nil)
	if err := c.startTLS(); err != nil {
		tb.Fatal(err)
	}
	c.scid = []byte{1, 2, 3, 4, 5, 6, 7, 8}
	c.ourCIDs[0].id = c.scid
	c.origDCID = []byte{9, 9, 9, 9, 9, 9, 9, 9}
	return c
}

func feed(c *Conn, b []byte) {
	d := bufPool.Get().(*[]byte)
	n := copy(*d, b)
	c.datagram(datagram{d, n}, time.Now())
}

func FuzzDatagram(f *testing.F) {
	ids := []byte{8, 1, 2, 3, 4, 5, 6, 7, 8}
	retry := append(append([]byte{0xf0, 0, 0, 0, 1}, ids...), 1, 0xaa, 1, 2, 3)
	f.Add(retry)
	vn := append(append(append([]byte{0x80, 0, 0, 0, 0}, ids...), 8, 9, 9, 9, 9, 9, 9, 9, 9), 0, 0, 0, 2)
	f.Add(vn)
	initial := append(append([]byte{0xc3, 0, 0, 0, 1}, ids...), 4, 1, 1, 1, 1, 0, 0x40, 0x30)
	f.Add(append(initial, make([]byte, 48)...))
	f.Add(append([]byte{0x43, 1, 2, 3, 4, 5, 6, 7, 8}, make([]byte, 40)...))
	f.Fuzz(func(t *testing.T, b []byte) {
		c := handshakeConn(t)
		feed(c, b)
		if !c.closing {
			c.flush(time.Now())
		}
		c.tls.Close()
	})
}

// a forged retry too short to hold its integrity tag used to crash the whole program
func TestShortRetry(t *testing.T) {
	for n := range 20 {
		c := handshakeConn(t)
		pkt := append(append([]byte{0xf0, 0, 0, 0, 1}, 8, 1, 2, 3, 4, 5, 6, 7, 8), 1, 0xaa)
		feed(c, append(pkt, make([]byte, n)...))
		if c.retried {
			t.Fatalf("accepted a %d byte retry body without a valid tag", n)
		}
		c.tls.Close()
	}
}

func TestAckedRangesMerge(t *testing.T) {
	s := newStream(0, kindRequest, 1000)
	s.out, s.fin = make([]byte, 300), true
	s.onAcked(0, 100, false)
	s.onAcked(200, 100, true)
	s.onAcked(100, 100, false)
	if !s.sendDone() || len(s.acked) != 1 {
		t.Fatalf("acked %v done %v", s.acked, s.sendDone())
	}
}

func TestHugeAckDelayAndIdle(t *testing.T) {
	b := appendVarint(nil, tpIdleTimeout)
	b = appendVarint(b, 8)
	b = appendVarint(b, maxVarint)
	b = appendVarint(b, tpOrigDCID)
	b = appendVarint(b, 0)
	b = appendVarint(b, tpInitialSCID)
	b = appendVarint(b, 0)
	p, err := parseParams(b)
	if err != nil || p.idle <= 0 || p.idle > time.Hour {
		t.Fatalf("idle %v %v", p.idle, err)
	}
	if d := time.Duration(min(uint64(maxVarint), 1<<24)<<20) * time.Microsecond; d <= 0 {
		t.Fatalf("ack delay wrapped to %v", d)
	}
}
