package h3

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"hash"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
)

// rfc 9001 section 5.2
var initialSalt = []byte{0x38, 0x76, 0x2c, 0xf7, 0xf5, 0x59, 0x34, 0xb3, 0x4d, 0x17, 0x9a, 0xe6, 0xa4, 0xc8, 0x0c, 0xad, 0xcc, 0xbb, 0x7f, 0x0a}

const tagLen = 16

// keys protects packets in one direction at one encryption level, only the connection's own goroutine uses them
type keys struct {
	suite  uint16
	secret []byte
	aead   cipher.AEAD
	iv     [12]byte
	hp     func(sample []byte) [5]byte
	n      [12]byte // nonce scratch so sealing doesn't allocate
}

func hashFor(suite uint16) (func() hash.Hash, int, error) {
	switch suite {
	case tls.TLS_AES_128_GCM_SHA256:
		return sha256.New, 16, nil
	case tls.TLS_AES_256_GCM_SHA384:
		return sha512.New384, 32, nil
	case tls.TLS_CHACHA20_POLY1305_SHA256:
		return sha256.New, 32, nil
	}
	return nil, 0, errors.New("h3: unsupported cipher suite")
}

// HKDF-Expand-Label from tls 1.3 with the "tls13 " prefix
func expandLabel(h func() hash.Hash, secret []byte, label string, n int) []byte {
	full := "tls13 " + label
	info := make([]byte, 0, 4+len(full))
	info = binary.BigEndian.AppendUint16(info, uint16(n))
	info = append(info, byte(len(full)))
	info = append(info, full...)
	info = append(info, 0) // empty context
	out, err := hkdf.Expand(h, secret, string(info), n)
	if err != nil {
		panic(err) // only when n is too long for the hash, which never happens here
	}
	return out
}

func newKeys(suite uint16, secret []byte) (*keys, error) {
	h, keyLen, err := hashFor(suite)
	if err != nil {
		return nil, err
	}
	k, err := packetKeys(suite, h, keyLen, secret)
	if err != nil {
		return nil, err
	}
	hpKey := expandLabel(h, secret, "quic hp", keyLen)
	if suite == tls.TLS_CHACHA20_POLY1305_SHA256 {
		k.hp = func(sample []byte) [5]byte {
			var mask [5]byte
			c, err := chacha20.NewUnauthenticatedCipher(hpKey, sample[4:16])
			if err == nil {
				c.SetCounter(binary.LittleEndian.Uint32(sample[:4]))
				c.XORKeyStream(mask[:], mask[:])
			}
			return mask
		}
	} else {
		block, err := aes.NewCipher(hpKey)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 16)
		k.hp = func(sample []byte) [5]byte {
			block.Encrypt(out, sample[:16])
			return [5]byte(out[:5])
		}
	}
	return k, nil
}

func packetKeys(suite uint16, h func() hash.Hash, keyLen int, secret []byte) (*keys, error) {
	key := expandLabel(h, secret, "quic key", keyLen)
	k := &keys{suite: suite, secret: secret}
	copy(k.iv[:], expandLabel(h, secret, "quic iv", 12))
	var err error
	if suite == tls.TLS_CHACHA20_POLY1305_SHA256 {
		k.aead, err = chacha20poly1305.New(key)
	} else {
		var block cipher.Block
		if block, err = aes.NewCipher(key); err == nil {
			k.aead, err = cipher.NewGCM(block)
		}
	}
	return k, err
}

// key update, rfc 9001 section 6, the header protection key stays the same
func (k *keys) next() *keys {
	h, keyLen, _ := hashFor(k.suite)
	secret := expandLabel(h, k.secret, "quic ku", h().Size())
	n, err := packetKeys(k.suite, h, keyLen, secret)
	if err != nil {
		panic(err) // same suite and sizes that already worked
	}
	n.hp = k.hp
	return n
}

func initialKeys(dcid []byte) (client, server *keys) {
	initial, _ := hkdf.Extract(sha256.New, dcid, initialSalt)
	c, _ := newKeys(tls.TLS_AES_128_GCM_SHA256, expandLabel(sha256.New, initial, "client in", 32))
	s, _ := newKeys(tls.TLS_AES_128_GCM_SHA256, expandLabel(sha256.New, initial, "server in", 32))
	return c, s
}

func (k *keys) nonce(pn uint64) []byte {
	k.n = k.iv
	for i := range 8 {
		k.n[11-i] ^= byte(pn >> (8 * i))
	}
	return k.n[:]
}

func (k *keys) seal(dst []byte, pn uint64, header, payload []byte) []byte {
	return k.aead.Seal(dst, k.nonce(pn), payload, header)
}

func (k *keys) open(dst []byte, pn uint64, header, ciphertext []byte) ([]byte, error) {
	return k.aead.Open(dst, k.nonce(pn), ciphertext, header)
}
