package h3

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"testing"
)

func unhex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rfc 9001 appendix a.1 to a.3
func TestInitialKeys(t *testing.T) {
	c, s := initialKeys(unhex(t, "8394c8f03e515708"))
	if got := hex.EncodeToString(c.secret); got != "c00cf151ca5be075ed0ebfb5c80323c42d6b7db67881289af4008f1f6c357aea" {
		t.Fatalf("client secret %s", got)
	}
	if got := hex.EncodeToString(s.secret); got != "3c199828fd139efd216c155ad844cc81fb82fa8d7446fa7d78be803acdda951b" {
		t.Fatalf("server secret %s", got)
	}
	if got := hex.EncodeToString(c.iv[:]); got != "fa044b2f42a3fd3b46fb255c" {
		t.Fatalf("client iv %s", got)
	}
	if got := hex.EncodeToString(s.iv[:]); got != "0ac1493ca1905853b0bba03e" {
		t.Fatalf("server iv %s", got)
	}
	if m := c.hp(unhex(t, "d1b1c98dd7689fb8ec11d242b123dc9b")); hex.EncodeToString(m[:]) != "437b9aec36" {
		t.Fatalf("client hp mask %x", m)
	}
	if m := s.hp(unhex(t, "2cd0991cd25b0aac406a5816b6394100")); hex.EncodeToString(m[:]) != "2ec0d8356a" {
		t.Fatalf("server hp mask %x", m)
	}
}

// rfc 9001 appendix a.5, a whole short header packet with chacha20
func TestChaChaPacket(t *testing.T) {
	k, err := newKeys(tls.TLS_CHACHA20_POLY1305_SHA256, unhex(t, "9ac312a7f877468ebe69422748ad00a15443f18203a07d6060f688f30f21632b"))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(k.iv[:]); got != "e0459b3474bdd0e44a41c144" {
		t.Fatalf("iv %s", got)
	}
	if got := hex.EncodeToString(k.next().secret); got != "1223504755036d556342ee9361d253421a826c9ecdf3c7148684b36b714881f9" {
		t.Fatalf("key update secret %s", got)
	}
	hdr := unhex(t, "4200bff4")
	ct := k.seal(nil, 654360564, hdr, []byte{0x01})
	if got := hex.EncodeToString(ct); got != "655e5cd55c41f69080575d7999c25a5bfb" {
		t.Fatalf("sealed %s", got)
	}
	m := k.hp(ct[1:17])
	if hex.EncodeToString(m[:]) != "aefefe7d03" {
		t.Fatalf("mask %x", m)
	}
	pt, err := k.open(nil, 654360564, hdr, ct)
	if err != nil || !bytes.Equal(pt, []byte{1}) {
		t.Fatalf("open %x %v", pt, err)
	}
}

func TestVarint(t *testing.T) {
	// rfc 9000 appendix a.1
	for _, c := range []struct {
		hex string
		v   uint64
	}{{"c2197c5eff14e88c", 151288809941952652}, {"9d7f3e7d", 494878333}, {"7bbd", 15293}, {"25", 37}} {
		b := unhex(t, c.hex)
		v, n, err := readVarint(b)
		if err != nil || v != c.v || n != len(b) {
			t.Fatalf("%s: %d %d %v", c.hex, v, n, err)
		}
		if got := appendVarint(nil, c.v); !bytes.Equal(got, b) {
			t.Fatalf("encode %d: %x", c.v, got)
		}
	}
	if v, _, _ := readVarint(unhex(t, "4025")); v != 37 {
		t.Fatal("two byte 37")
	}
	if _, _, err := readVarint(unhex(t, "c2197c")); err == nil {
		t.Fatal("truncated varint accepted")
	}
}
