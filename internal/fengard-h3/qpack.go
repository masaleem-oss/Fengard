package h3

import (
	"errors"
	"strconv"

	"golang.org/x/net/http2/hpack"
)

// qpack with the static table only, we tell the server our dynamic table holds nothing so it can't use one

// rfc 9204 appendix a
var staticTable = [...][2]string{
	{":authority", ""}, {":path", "/"}, {"age", "0"}, {"content-disposition", ""}, {"content-length", "0"},
	{"cookie", ""}, {"date", ""}, {"etag", ""}, {"if-modified-since", ""}, {"if-none-match", ""},
	{"last-modified", ""}, {"link", ""}, {"location", ""}, {"referer", ""}, {"set-cookie", ""},
	{":method", "CONNECT"}, {":method", "DELETE"}, {":method", "GET"}, {":method", "HEAD"}, {":method", "OPTIONS"},
	{":method", "POST"}, {":method", "PUT"}, {":scheme", "http"}, {":scheme", "https"}, {":status", "103"},
	{":status", "200"}, {":status", "304"}, {":status", "404"}, {":status", "503"}, {"accept", "*/*"},
	{"accept", "application/dns-message"}, {"accept-encoding", "gzip, deflate, br"}, {"accept-ranges", "bytes"},
	{"access-control-allow-headers", "cache-control"}, {"access-control-allow-headers", "content-type"},
	{"access-control-allow-origin", "*"}, {"cache-control", "max-age=0"}, {"cache-control", "max-age=2592000"},
	{"cache-control", "max-age=604800"}, {"cache-control", "no-cache"}, {"cache-control", "no-store"},
	{"cache-control", "public, max-age=31536000"}, {"content-encoding", "br"}, {"content-encoding", "gzip"},
	{"content-type", "application/dns-message"}, {"content-type", "application/javascript"},
	{"content-type", "application/json"}, {"content-type", "application/x-www-form-urlencoded"},
	{"content-type", "image/gif"}, {"content-type", "image/jpeg"}, {"content-type", "image/png"},
	{"content-type", "text/css"}, {"content-type", "text/html; charset=utf-8"}, {"content-type", "text/plain"},
	{"content-type", "text/plain;charset=utf-8"}, {"range", "bytes=0-"}, {"strict-transport-security", "max-age=31536000"},
	{"strict-transport-security", "max-age=31536000; includesubdomains"},
	{"strict-transport-security", "max-age=31536000; includesubdomains; preload"}, {"vary", "accept-encoding"},
	{"vary", "origin"}, {"x-content-type-options", "nosniff"}, {"x-xss-protection", "1; mode=block"},
	{":status", "100"}, {":status", "204"}, {":status", "206"}, {":status", "302"}, {":status", "400"},
	{":status", "403"}, {":status", "421"}, {":status", "425"}, {":status", "500"}, {"accept-language", ""},
	{"access-control-allow-credentials", "FALSE"}, {"access-control-allow-credentials", "TRUE"},
	{"access-control-allow-headers", "*"}, {"access-control-allow-methods", "get"},
	{"access-control-allow-methods", "get, post, options"}, {"access-control-allow-methods", "options"},
	{"access-control-expose-headers", "content-length"}, {"access-control-request-headers", "content-type"},
	{"access-control-request-method", "get"}, {"access-control-request-method", "post"}, {"alt-svc", "clear"},
	{"authorization", ""}, {"content-security-policy", "script-src 'none'; object-src 'none'; base-uri 'none'"},
	{"early-data", "1"}, {"expect-ct", ""}, {"forwarded", ""}, {"if-range", ""}, {"origin", ""}, {"purpose", "prefetch"},
	{"server", ""}, {"timing-allow-origin", "*"}, {"upgrade-insecure-requests", "1"}, {"user-agent", ""},
	{"x-forwarded-for", ""}, {"x-frame-options", "deny"}, {"x-frame-options", "sameorigin"},
}

// indexes we use when writing a request
const (
	qAuthority     = 0
	qPath          = 1
	qContentLength = 4
	qMethodPOST    = 20
	qSchemeHTTPS   = 23
	qAcceptDNS     = 30
	qTypeDNS       = 44
)

const maxFieldSection = 16 << 10

var errQPACK = errors.New("h3: bad qpack field section")

// rfc 7541 section 5.1 prefix integers, flags holds the bits above the prefix
func appendPrefixInt(b []byte, flags byte, bits uint, v uint64) []byte {
	max := uint64(1)<<bits - 1
	if v < max {
		return append(b, flags|byte(v))
	}
	b = append(b, flags|byte(max))
	v -= max
	for v >= 128 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readPrefixInt(r *reader, bits uint) uint64 {
	first := r.byte()
	if r.err != nil {
		return 0
	}
	max := uint64(1)<<bits - 1
	v := uint64(first) & max
	if v < max {
		return v
	}
	for shift := uint(0); ; shift += 7 {
		if shift > 56 {
			r.err = errQPACK
			return 0
		}
		c := r.byte()
		if r.err != nil {
			return 0
		}
		v += uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v
		}
	}
}

// literal strings, huffman flag is the bit above the length prefix
func appendString(b []byte, flags byte, bits uint, s string) []byte {
	return append(appendPrefixInt(b, flags, bits, uint64(len(s))), s...)
}

func readString(r *reader, bits uint) string {
	if r.err != nil || len(r.b) == 0 {
		r.err = errQPACK
		return ""
	}
	huff := r.b[0]&(1<<bits) != 0
	n := readPrefixInt(r, bits)
	if n > maxFieldSection {
		r.err = errQPACK
		return ""
	}
	raw := r.bytes(n)
	if r.err != nil {
		return ""
	}
	if !huff {
		return string(raw)
	}
	s, err := hpack.HuffmanDecodeToString(raw)
	if err != nil {
		r.err = errQPACK
	}
	return s
}

// encodeRequest is the whole request stream for a dns-over-https POST, rfc 8484
func encodeRequest(authority, path string, query []byte) []byte {
	hdr := make([]byte, 0, 64+len(authority)+len(path))
	hdr = append(hdr, 0, 0) // required insert count and base, no dynamic table
	hdr = appendPrefixInt(hdr, 0xc0, 6, qMethodPOST)
	hdr = appendPrefixInt(hdr, 0xc0, 6, qSchemeHTTPS)
	hdr = appendString(appendPrefixInt(hdr, 0x50, 4, qAuthority), 0, 7, authority)
	hdr = appendString(appendPrefixInt(hdr, 0x50, 4, qPath), 0, 7, path)
	hdr = appendPrefixInt(hdr, 0xc0, 6, qAcceptDNS)
	hdr = appendPrefixInt(hdr, 0xc0, 6, qTypeDNS)
	hdr = appendString(appendPrefixInt(hdr, 0x50, 4, qContentLength), 0, 7, strconv.Itoa(len(query)))
	b := make([]byte, 0, len(hdr)+len(query)+16)
	b = appendFrame(b, frameHeaders, hdr)
	return appendFrame(b, frameData, query)
}

// decodeHeaders reads a response field section and returns its status
func decodeHeaders(b []byte) (int, error) {
	r := reader{b: b}
	if readPrefixInt(&r, 8) != 0 {
		return 0, errQPACK // the server can't use a dynamic table we never gave it
	}
	readPrefixInt(&r, 7)
	status, size := 0, 0
	for len(r.b) > 0 && r.err == nil {
		var name, value string
		c := r.b[0]
		switch {
		case c&0x80 != 0: // indexed
			if c&0x40 == 0 {
				return 0, errQPACK
			}
			i := readPrefixInt(&r, 6)
			if i >= uint64(len(staticTable)) {
				return 0, errQPACK
			}
			name, value = staticTable[i][0], staticTable[i][1]
		case c&0x40 != 0: // literal with a name reference
			if c&0x10 == 0 {
				return 0, errQPACK
			}
			i := readPrefixInt(&r, 4)
			if i >= uint64(len(staticTable)) {
				return 0, errQPACK
			}
			name = staticTable[i][0]
			value = readString(&r, 7)
		case c&0x20 != 0: // literal name
			name = readString(&r, 3)
			value = readString(&r, 7)
		default: // post base forms need a dynamic table
			return 0, errQPACK
		}
		if r.err != nil {
			return 0, errQPACK
		}
		if size += len(name) + len(value) + 32; size > maxFieldSection {
			return 0, errQPACK
		}
		if name == ":status" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 100 || n > 999 || status != 0 {
				return 0, errQPACK
			}
			status = n
		}
	}
	if r.err != nil || status == 0 {
		return 0, errQPACK
	}
	return status, nil
}

// http/3 frames, rfc 9114 section 7.2
const (
	frameData     = 0x00
	frameHeaders  = 0x01
	frameSettings = 0x04
	framePush     = 0x05
	frameGoaway   = 0x07
)

func appendFrame(b []byte, typ uint64, payload []byte) []byte {
	b = appendVarint(b, typ)
	b = appendVarint(b, uint64(len(payload)))
	return append(b, payload...)
}

// nextFrame splits one frame off b, errShort when it isn't all there yet
func nextFrame(b []byte) (typ uint64, payload []byte, n int, err error) {
	r := reader{b: b}
	typ = r.varint()
	l := r.varint()
	if r.err != nil {
		return 0, nil, 0, r.err
	}
	if l > uint64(len(r.b)) {
		return 0, nil, 0, errShort
	}
	payload = r.b[:l]
	return typ, payload, len(b) - len(r.b) + int(l), nil
}

// h2 frame types that must never show up in http/3
func reservedFrame(typ uint64) bool { return typ == 0x02 || typ == 0x06 || typ == 0x08 || typ == 0x09 }

// readResponse turns a whole request stream from the server into the response body
func readResponse(b []byte) ([]byte, int, error) {
	var body []byte
	status := 0
	for len(b) > 0 {
		typ, payload, n, err := nextFrame(b)
		if err != nil {
			return nil, 0, errors.New("h3: response cut short")
		}
		b = b[n:]
		switch {
		case typ == frameHeaders:
			if status >= 200 {
				continue // trailers
			}
			if status, err = decodeHeaders(payload); err != nil {
				return nil, 0, err
			}
			if status < 200 {
				status = 0 // informational, the real one follows
			}
		case typ == frameData:
			if status == 0 {
				return nil, 0, errors.New("h3: response data before headers")
			}
			if len(body)+len(payload) > 65535 {
				return nil, 0, errors.New("h3: response too large for dns")
			}
			body = append(body, payload...)
		case reservedFrame(typ) || typ == frameSettings || typ == frameGoaway || typ == framePush || typ == 0x03 || typ == 0x0d:
			return nil, 0, errors.New("h3: unexpected frame on a request stream")
		}
	}
	if status == 0 {
		return nil, 0, errors.New("h3: response had no headers")
	}
	return body, status, nil
}
