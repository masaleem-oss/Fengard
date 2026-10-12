// Package h3 is fengard-h3, a small DNS-over-HTTP/3 client built for Fengard.
//
// It speaks just enough QUIC v1 (RFC 9000, 9001, 9002), HTTP/3 (RFC 9114) and QPACK (RFC 9204,
// static table only) to send DNS queries to a few public resolvers over one long lived connection
// each. The TLS 1.3 handshake and its keys come from crypto/tls.
package h3

import "errors"

const maxVarint = 1<<62 - 1

var errShort = errors.New("h3: truncated")

// quic variable length integers, the top two bits give the length
func readVarint(b []byte) (uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, errShort
	}
	n := 1 << (b[0] >> 6)
	if len(b) < n {
		return 0, 0, errShort
	}
	v := uint64(b[0] & 0x3f)
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, n, nil
}

func varintLen(v uint64) int {
	switch {
	case v < 1<<6:
		return 1
	case v < 1<<14:
		return 2
	case v < 1<<30:
		return 4
	default:
		return 8
	}
}

func appendVarint(b []byte, v uint64) []byte {
	switch {
	case v < 1<<6:
		return append(b, byte(v))
	case v < 1<<14:
		return append(b, byte(v>>8)|0x40, byte(v))
	case v < 1<<30:
		return append(b, byte(v>>24)|0x80, byte(v>>16), byte(v>>8), byte(v))
	default:
		return append(b, byte(v>>56)|0xc0, byte(v>>48), byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
}

// reader walks a buffer and remembers the first error so parsers can check once at the end
type reader struct {
	b   []byte
	err error
}

func (r *reader) varint() uint64 {
	if r.err != nil {
		return 0
	}
	v, n, err := readVarint(r.b)
	if err != nil {
		r.err = err
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) bytes(n uint64) []byte {
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.b)) {
		r.err = errShort
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) byte() byte {
	if b := r.bytes(1); b != nil {
		return b[0]
	}
	return 0
}
