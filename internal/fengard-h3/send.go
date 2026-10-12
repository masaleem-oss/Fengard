package h3

import (
	"strconv"
	"time"
)

// part is one packet's worth of frames waiting for its header
type part struct {
	sp        space
	payload   []byte
	frames    []sentFrame
	eliciting bool
}

func itoa(n int) string { return strconv.Itoa(n) }

// flush sends everything that's ready, one datagram at a time
func (c *Conn) flush(now time.Time) {
	var parts [numSpaces]part
	for range 64 {
		if c.closing {
			return
		}
		n := 0
		room := maxDatagram
		for sp := spInitial; sp < numSpaces; sp++ {
			if c.send[sp] == nil || c.sp[sp].dropped {
				continue
			}
			over := c.overhead(sp)
			if room-over < 40 {
				break
			}
			p := c.gather(sp, room-over, now)
			if len(p.payload) == 0 {
				continue
			}
			room -= over + len(p.payload)
			parts[n] = p
			n++
		}
		if n == 0 {
			return
		}
		c.writeDatagram(now, parts[:n])
	}
}

// overhead is the header and tag around a packet's payload
func (c *Conn) overhead(sp space) int {
	if sp == spApp {
		return 1 + len(c.dcid) + pnLen + tagLen
	}
	n := 1 + 4 + 1 + len(c.dcid) + 1 + len(c.scid) + 2 + pnLen + tagLen
	if sp == spInitial {
		n += varintLen(uint64(len(c.token))) + len(c.token)
	}
	return n
}

func (c *Conn) hasData(sp space) bool {
	s := c.sp[sp]
	if len(s.cryptoLost) > 0 || s.cryptoSent < uint64(len(s.cryptoOut)) || c.probe[sp] > 0 {
		return true
	}
	if sp != spApp || !c.handshakeDone {
		return false
	}
	if c.ping || c.sendMaxData || c.sendMaxUni || len(c.retireQ) > 0 || len(c.newCIDQ) > 0 || len(c.pathResp) > 0 || len(c.ctrl) > 0 {
		return true
	}
	for _, st := range c.sendQ {
		if st.wantsSend() {
			return true
		}
	}
	return false
}

// gather fills one packet for a space with at most room bytes of frames
func (c *Conn) gather(sp space, room int, now time.Time) part {
	s := c.sp[sp]
	p := part{sp: sp}
	b := c.payload[sp][:0]
	canSend := c.cc.canSend() || c.probe[sp] > 0
	if !s.ackAt.IsZero() && (!now.Before(s.ackAt) || canSend && c.hasData(sp)) && len(s.recvd.r) > 0 {
		var delay uint64
		if sp == spApp {
			delay = uint64(now.Sub(s.largestRecvAt).Microseconds()) >> 3
		}
		if a := s.recvd.appendAck(b, delay); len(a) <= room {
			b = a
			s.ackAt, s.unacked = time.Time{}, 0
		}
	}
	if canSend {
		b = c.gatherCrypto(sp, b, room, &p)
		if sp == spApp && c.handshakeDone {
			b = c.gatherControl(b, room, &p)
			b = c.gatherStreams(b, room, &p)
		}
		if c.probe[sp] > 0 {
			if !p.eliciting && len(b) < room {
				b = append(b, 0x01) // ping
				p.eliciting = true
			}
			if p.eliciting {
				c.probe[sp]--
			}
		}
	}
	c.payload[sp] = b
	p.payload = b
	return p
}

func (c *Conn) gatherCrypto(sp space, b []byte, room int, p *part) []byte {
	s := c.sp[sp]
	for {
		var off, n uint64
		switch {
		case len(s.cryptoLost) > 0:
			off, n = s.cryptoLost[0].off, s.cryptoLost[0].n
		case s.cryptoSent < uint64(len(s.cryptoOut)):
			off, n = s.cryptoSent, uint64(len(s.cryptoOut))-s.cryptoSent
		default:
			return b
		}
		hdr := 1 + varintLen(off) + 2
		if room-len(b) <= hdr {
			return b
		}
		n = min(n, uint64(room-len(b)-hdr))
		b = append(b, 0x06)
		b = appendVarint(b, off)
		b = appendVarint(b, n)
		b = append(b, s.cryptoOut[off:off+n]...)
		p.frames = append(p.frames, sentFrame{kind: sfCrypto, off: off, n: n})
		p.eliciting = true
		if len(s.cryptoLost) > 0 {
			s.cryptoLost[0].off += n
			if s.cryptoLost[0].n -= n; s.cryptoLost[0].n == 0 {
				s.cryptoLost = s.cryptoLost[1:]
			}
		} else {
			s.cryptoSent += n
		}
	}
}

func (c *Conn) gatherControl(b []byte, room int, p *part) []byte {
	const most = 1 + 8 + 8 + 8 // the largest control frame we write
	add := func(f []byte, sf sentFrame, record bool) {
		b = append(b, f...)
		if record {
			p.frames = append(p.frames, sf)
		}
		p.eliciting = true
	}
	var tmp [most]byte
	if c.ping {
		add([]byte{0x01}, sentFrame{}, false)
		c.ping = false
	}
	if c.sendMaxData && room-len(b) >= most {
		add(appendVarint(append(tmp[:0], 0x10), c.maxData), sentFrame{kind: sfMaxData}, true)
		c.sendMaxData = false
	}
	if c.sendMaxUni && room-len(b) >= most {
		add(appendVarint(append(tmp[:0], 0x13), c.maxPeerUni), sentFrame{kind: sfMaxStreamsUni}, true)
		c.sendMaxUni = false
	}
	for len(c.newCIDQ) > 0 && room-len(b) >= 40 {
		seq := c.newCIDQ[0]
		c.newCIDQ = c.newCIDQ[1:]
		for _, o := range c.ourCIDs {
			if o.seq == seq {
				f := appendVarint(appendVarint([]byte{0x18}, seq), 0)
				f = append(append(append(f, byte(len(o.id))), o.id...), o.token...)
				add(f, sentFrame{kind: sfNewCID, id: seq}, true)
			}
		}
	}
	for len(c.retireQ) > 0 && room-len(b) >= most {
		seq := c.retireQ[0]
		c.retireQ = c.retireQ[1:]
		add(appendVarint(append(tmp[:0], 0x19), seq), sentFrame{kind: sfRetireCID, id: seq}, true)
	}
	for len(c.pathResp) > 0 && room-len(b) >= 9 {
		add(append(append(tmp[:0], 0x1b), c.pathResp[0][:]...), sentFrame{}, false)
		c.pathResp = c.pathResp[1:]
	}
	for len(c.ctrl) > 0 && room-len(b) >= most {
		f := c.ctrl[0]
		c.ctrl = c.ctrl[1:]
		s := c.streams[f.id]
		switch f.kind {
		case sfMaxStreamData:
			if s == nil || s.complete() {
				continue
			}
			add(appendVarint(appendVarint(append(tmp[:0], 0x11), f.id), s.limit), sentFrame{kind: sfMaxStreamData, id: f.id}, true)
		case sfStopSending:
			if s == nil {
				continue
			}
			add(appendVarint(appendVarint(append(tmp[:0], 0x05), f.id), f.code), sentFrame{kind: sfStopSending, id: f.id}, true)
		case sfResetStream:
			add(appendVarint(appendVarint(appendVarint(append(tmp[:0], 0x04), f.id), f.code), f.final), sentFrame{kind: sfResetStream, id: f.id, off: f.final}, true)
		}
	}
	return b
}

func (c *Conn) gatherStreams(b []byte, room int, p *part) []byte {
	keep := c.sendQ[:0]
	for _, s := range c.sendQ {
		for s.wantsSend() {
			hdr := 1 + varintLen(s.id) + varintLen(s.sent) + 2
			free := room - len(b) - hdr
			if free < 0 {
				break
			}
			var connRoom uint64
			if c.peerMaxData > c.sentData {
				connRoom = c.peerMaxData - c.sentData
			}
			off, data, fin, isNew, ok := s.nextSend(free, s.peerLimit, connRoom)
			if !ok {
				break
			}
			typ := byte(0x0e)
			if fin {
				typ |= 0x01
			}
			b = append(b, typ)
			b = appendVarint(b, s.id)
			b = appendVarint(b, off)
			b = appendVarint(b, uint64(len(data)))
			b = append(b, data...)
			if isNew {
				c.sentData += uint64(len(data))
			}
			p.frames = append(p.frames, sentFrame{kind: sfStream, id: s.id, off: off, n: uint64(len(data)), fin: fin})
			p.eliciting = true
		}
		if s.wantsSend() {
			keep = append(keep, s)
		}
	}
	clear(c.sendQ[len(keep):])
	c.sendQ = keep
	return b
}

// writeDatagram seals the parts into one datagram and sends it
func (c *Conn) writeDatagram(now time.Time, parts []part) {
	if c.udp == nil {
		return
	}
	total, initial := 0, false
	for _, p := range parts {
		total += c.overhead(p.sp) + len(p.payload)
		initial = initial || p.sp == spInitial
	}
	padded := -1
	// every datagram carrying an initial packet has to be at least 1200 bytes, rfc 9000 section 14.1
	if initial && total < maxDatagram {
		last := &parts[len(parts)-1]
		last.payload = append(last.payload, make([]byte, maxDatagram-total)...)
		padded = len(parts) - 1
	}
	d := c.dgram[:0]
	sentHandshake, eliciting := false, false
	for i, p := range parts {
		s := c.sp[p.sp]
		pn := s.next
		s.next++
		start := len(d)
		var pnOff int
		switch p.sp {
		case spApp:
			d, pnOff = appendShort(d, c.dcid, c.keyPhase, pn)
			c.sentApp++
		case spInitial:
			d, pnOff = appendLong(d, typeInitial, c.dcid, c.scid, c.token, len(p.payload), pn)
		default:
			d, pnOff = appendLong(d, typeHandshake, c.dcid, c.scid, nil, len(p.payload), pn)
			sentHandshake = true
		}
		d = protect(d, start, pnOff, p.payload, c.send[p.sp], pn, p.sp != spApp)
		inFlight := p.eliciting || i == padded
		if !inFlight {
			continue
		}
		sp := &sentPacket{pn: pn, at: now, size: len(d) - start, ackEliciting: p.eliciting, inFlight: true, frames: p.frames}
		s.sent = append(s.sent, sp)
		c.cc.inFlight += sp.size
		if p.eliciting {
			s.lastEliciting = now
			eliciting = true
		}
	}
	c.dgram = d
	c.setStatus(func(st *Status) { st.Sent++ })
	if _, err := c.udp.Write(d); err != nil && !c.handshakeDone && !c.closing {
		c.fail(ErrUnreachable)
		return
	}
	// a client drops its initial keys once it sends a handshake packet, rfc 9001 section 4.9.1
	if sentHandshake {
		c.drop(spInitial)
	}
	if eliciting && !c.elicitingSinceRecv {
		c.lastActive, c.elicitingSinceRecv = now, true
		c.lastActivity.Store(now.UnixNano())
	}
	if c.sentApp > aeadSendLimit && !c.goaway {
		// time for fresh keys, a new connection is simpler than a key update for this little traffic
		c.goaway, c.goawayID = true, c.openedBidi*4
		c.goingAway.Store(true)
		c.closeIfIdle()
	}
}
