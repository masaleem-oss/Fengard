package h3

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"errors"
)

const (
	version1 = 0x00000001

	typeInitial   = 0
	typeZeroRTT   = 1
	typeHandshake = 2
	typeRetry     = 3

	// every packet number goes out as 4 bytes so there's always room for the header protection sample
	pnLen = 4
	// what we send, the smallest size every quic path has to carry
	maxDatagram = 1200
	cidLen      = 8
	maxCIDLen   = 20
)

type space int

const (
	spInitial space = iota
	spHandshake
	spApp
	numSpaces
)

func (s space) String() string { return [...]string{"initial", "handshake", "1-rtt"}[s] }

var errDrop = errors.New("h3: packet dropped")

type header struct {
	long     bool
	typ      byte
	version  uint32
	dcid     []byte
	scid     []byte
	token    []byte
	pnOffset int
	length   int // of this packet inside the datagram
}

// parseHeader reads the unprotected part of the first packet in d
func parseHeader(d []byte, shortCIDLen int) (header, error) {
	var h header
	if len(d) == 0 {
		return h, errDrop
	}
	first := d[0]
	if first&0x80 == 0 {
		// short header, only 1-rtt
		if first&0x40 == 0 || len(d) < 1+shortCIDLen {
			return h, errDrop
		}
		h.dcid = d[1 : 1+shortCIDLen]
		h.pnOffset = 1 + shortCIDLen
		h.length = len(d)
		return h, nil
	}
	h.long = true
	if len(d) < 7 {
		return h, errDrop
	}
	h.version = binary.BigEndian.Uint32(d[1:5])
	r := reader{b: d[5:]}
	h.dcid = r.bytes(uint64(r.byte()))
	h.scid = r.bytes(uint64(r.byte()))
	if r.err != nil || len(h.dcid) > maxCIDLen || len(h.scid) > maxCIDLen {
		return h, errDrop
	}
	if h.version == 0 {
		h.length = len(d) // version negotiation, the rest is a version list
		h.pnOffset = len(d) - len(r.b)
		return h, nil
	}
	if first&0x40 == 0 {
		return h, errDrop
	}
	h.typ = (first >> 4) & 3
	switch h.typ {
	case typeRetry:
		h.pnOffset = len(d) - len(r.b)
		h.length = len(d)
		return h, nil
	case typeInitial:
		h.token = r.bytes(r.varint())
	}
	n := r.varint()
	if r.err != nil {
		return h, errDrop
	}
	h.pnOffset = len(d) - len(r.b)
	if n > uint64(len(r.b)) {
		return h, errDrop
	}
	h.length = h.pnOffset + int(n)
	return h, nil
}

// rfc 9000 appendix a.3
func decodePN(largest int64, truncated uint64, pnBytes int) uint64 {
	expected := largest + 1
	win := int64(1) << (8 * pnBytes)
	hwin := win / 2
	candidate := (expected &^ (win - 1)) | int64(truncated)
	switch {
	case candidate <= expected-hwin && candidate < (1<<62)-win:
		candidate += win
	case candidate > expected+hwin && candidate >= win:
		candidate -= win
	}
	return uint64(candidate)
}

// unmask takes header protection off pkt in place and returns the packet number length
func unmask(pkt []byte, pnOffset int, k *keys, long bool) (int, error) {
	if len(pkt) < pnOffset+4+16 {
		return 0, errDrop
	}
	mask := k.hp(pkt[pnOffset+4 : pnOffset+20])
	if long {
		pkt[0] ^= mask[0] & 0x0f
	} else {
		pkt[0] ^= mask[0] & 0x1f
	}
	n := int(pkt[0]&3) + 1
	for i := range n {
		pkt[pnOffset+i] ^= mask[1+i]
	}
	return n, nil
}

func truncatedPN(pkt []byte, pnOffset, n int) uint64 {
	var v uint64
	for _, b := range pkt[pnOffset : pnOffset+n] {
		v = v<<8 | uint64(b)
	}
	return v
}

// appendLong writes a long header with room for the 4 byte packet number and returns where it starts
func appendLong(b []byte, typ byte, dcid, scid, token []byte, payloadLen int, pn uint64) ([]byte, int) {
	b = append(b, 0xc0|typ<<4|(pnLen-1))
	b = binary.BigEndian.AppendUint32(b, version1)
	b = append(b, byte(len(dcid)))
	b = append(b, dcid...)
	b = append(b, byte(len(scid)))
	b = append(b, scid...)
	if typ == typeInitial {
		b = appendVarint(b, uint64(len(token)))
		b = append(b, token...)
	}
	// always two bytes so the header size is known before the payload is
	n := pnLen + payloadLen + tagLen
	b = append(b, byte(n>>8)|0x40, byte(n))
	pnOff := len(b)
	return binary.BigEndian.AppendUint32(b, uint32(pn)), pnOff
}

func appendShort(b []byte, dcid []byte, keyPhase bool, pn uint64) ([]byte, int) {
	first := byte(0x40 | (pnLen - 1))
	if keyPhase {
		first |= 0x04
	}
	b = append(b, first)
	b = append(b, dcid...)
	pnOff := len(b)
	return binary.BigEndian.AppendUint32(b, uint32(pn)), pnOff
}

// protect seals the payload after the header at b[start:] and masks the header
func protect(b []byte, start, pnOff int, payload []byte, k *keys, pn uint64, long bool) []byte {
	hdr := b[start:]
	b = k.seal(b, pn, hdr, payload)
	pkt := b[start:]
	off := pnOff - start
	mask := k.hp(pkt[off+4 : off+20])
	if long {
		pkt[0] ^= mask[0] & 0x0f
	} else {
		pkt[0] ^= mask[0] & 0x1f
	}
	for i := range pnLen {
		pkt[off+i] ^= mask[1+i]
	}
	return b
}

// rfc 9001 section 5.8
var (
	retryKey   = []byte{0xbe, 0x0c, 0x69, 0x0b, 0x9f, 0x66, 0x57, 0x5a, 0x1d, 0x76, 0x6b, 0x54, 0xe3, 0x68, 0xc8, 0x4e}
	retryNonce = []byte{0x46, 0x15, 0x99, 0xd3, 0x5d, 0x63, 0x2b, 0xf2, 0x23, 0x98, 0x25, 0xbb}
)

func retryValid(pkt, odcid []byte) bool {
	if len(pkt) < tagLen {
		return false
	}
	pseudo := make([]byte, 0, 1+len(odcid)+len(pkt))
	pseudo = append(pseudo, byte(len(odcid)))
	pseudo = append(pseudo, odcid...)
	pseudo = append(pseudo, pkt[:len(pkt)-tagLen]...)
	block, _ := aes.NewCipher(retryKey)
	gcm, _ := cipher.NewGCM(block)
	tag := gcm.Seal(nil, retryNonce, nil, pseudo)
	return subtle.ConstantTimeCompare(tag, pkt[len(pkt)-tagLen:]) == 1
}
