package h3

import (
	"bytes"
	"time"
)

// transport parameter ids, rfc 9000 section 18.2
const (
	tpOrigDCID         = 0x00
	tpIdleTimeout      = 0x01
	tpStatelessReset   = 0x02
	tpMaxUDPPayload    = 0x03
	tpMaxData          = 0x04
	tpMaxStreamBidiLoc = 0x05
	tpMaxStreamBidiRem = 0x06
	tpMaxStreamUni     = 0x07
	tpMaxStreamsBidi   = 0x08
	tpMaxStreamsUni    = 0x09
	tpAckDelayExp      = 0x0a
	tpMaxAckDelay      = 0x0b
	tpActiveCIDLimit   = 0x0e
	tpInitialSCID      = 0x0f
	tpRetrySCID        = 0x10
)

// what the server told us about itself
type params struct {
	origDCID       []byte
	initialSCID    []byte
	retrySCID      []byte
	hasRetrySCID   bool
	resetToken     []byte
	idle           time.Duration
	maxData        uint64
	maxStreamBidi  uint64 // for streams we open, the server calls them bidi remote
	maxStreamUni   uint64
	maxStreamsBidi uint64
	maxStreamsUni  uint64
	ackDelayExp    uint64
	maxAckDelay    time.Duration
	cidLimit       uint64
}

func parseParams(b []byte) (params, error) {
	p := params{ackDelayExp: 3, maxAckDelay: 25 * time.Millisecond, cidLimit: 2}
	var seen []uint64
	hasOrig, hasSCID := false, false
	r := reader{b: b}
	for len(r.b) > 0 && r.err == nil {
		id := r.varint()
		val := r.bytes(r.varint())
		if r.err != nil {
			break
		}
		for _, s := range seen {
			if s == id {
				return p, transportErr(errTransportParameter, "duplicate transport parameter")
			}
		}
		seen = append(seen, id)
		v := reader{b: val}
		num := func() uint64 {
			n := v.varint()
			if len(v.b) != 0 {
				v.err = errShort
			}
			return n
		}
		switch id {
		case tpOrigDCID:
			p.origDCID, hasOrig = val, true
		case tpInitialSCID:
			p.initialSCID, hasSCID = val, true
		case tpRetrySCID:
			p.retrySCID, p.hasRetrySCID = val, true
		case tpStatelessReset:
			if len(val) != 16 {
				return p, transportErr(errTransportParameter, "bad stateless reset token")
			}
			p.resetToken = val
		case tpIdleTimeout:
			p.idle = time.Duration(min(num(), 3600_000)) * time.Millisecond
		case tpMaxUDPPayload:
			if num() < 1200 {
				v.err = errShort
			}
		case tpMaxData:
			p.maxData = num()
		case tpMaxStreamBidiRem:
			p.maxStreamBidi = num()
		case tpMaxStreamUni:
			p.maxStreamUni = num()
		case tpMaxStreamsBidi:
			if p.maxStreamsBidi = num(); p.maxStreamsBidi > 1<<60 {
				v.err = errShort
			}
		case tpMaxStreamsUni:
			if p.maxStreamsUni = num(); p.maxStreamsUni > 1<<60 {
				v.err = errShort
			}
		case tpAckDelayExp:
			if p.ackDelayExp = num(); p.ackDelayExp > 20 {
				v.err = errShort
			}
		case tpMaxAckDelay:
			d := num()
			if d >= 1<<14 {
				v.err = errShort
			}
			p.maxAckDelay = time.Duration(d) * time.Millisecond
		case tpActiveCIDLimit:
			if p.cidLimit = num(); p.cidLimit < 2 {
				v.err = errShort
			}
		}
		if v.err != nil {
			return p, transportErr(errTransportParameter, "bad transport parameter")
		}
	}
	if r.err != nil {
		return p, transportErr(errTransportParameter, "truncated transport parameters")
	}
	if !hasOrig || !hasSCID {
		return p, transportErr(errTransportParameter, "missing connection id transport parameters")
	}
	return p, nil
}

// checkCIDs makes sure nobody rewrote the handshake's connection ids, rfc 9000 section 7.3
func (p params) checkCIDs(origDCID, serverSCID, retrySCID []byte) error {
	ok := bytes.Equal(p.origDCID, origDCID) && bytes.Equal(p.initialSCID, serverSCID) &&
		p.hasRetrySCID == (retrySCID != nil) && bytes.Equal(p.retrySCID, retrySCID)
	if !ok {
		return transportErr(errTransportParameter, "connection ids don't match the handshake")
	}
	return nil
}

// what we tell the server, only our receive limits and our connection id
func appendOurParams(b []byte, scid []byte) []byte {
	num := func(id, v uint64) {
		b = appendVarint(b, id)
		b = appendVarint(b, uint64(varintLen(v)))
		b = appendVarint(b, v)
	}
	num(tpIdleTimeout, uint64(idleTimeout/time.Millisecond))
	num(tpMaxUDPPayload, 1472)
	num(tpMaxData, connWindow)
	num(tpMaxStreamBidiLoc, streamWindow)
	num(tpMaxStreamUni, uniWindow)
	num(tpMaxStreamsUni, peerUniStreams)
	b = appendVarint(b, tpInitialSCID)
	b = appendVarint(b, uint64(len(scid)))
	return append(b, scid...)
}
