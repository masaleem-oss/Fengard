package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

func newAuth(t *testing.T) *Auth {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	users, _ := db.KV("users")
	keys, _ := db.KV("apikeys")
	a := New(users, keys)
	if err := a.Setup("admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTwoFactorRoundTrip(t *testing.T) {
	a := newAuth(t)
	secret, uri, err := a.TOTPSetup("admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("bad otpauth uri %q", uri)
	}
	raw, _ := b32.DecodeString(secret)
	if _, err := a.TOTPEnable("admin", "000000"); err == nil {
		t.Fatal("wrong code accepted during enrollment")
	}
	codes, err := a.TOTPEnable("admin", totpCode(raw, time.Now().Unix()/totpStep))
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryN {
		t.Fatalf("got %d recovery codes", len(codes))
	}

	// password alone isnt enough anymore
	if _, err := a.Login("admin", "correct horse battery"); !errors.Is(err, ErrTwoFactor) {
		t.Fatalf("Login = %v, want ErrTwoFactor", err)
	}
	tok, err := a.Pending("admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CompleteLogin(tok, "123456"); err == nil {
		t.Fatal("wrong TOTP accepted")
	}
	code := totpCode(raw, time.Now().Unix()/totpStep+1) // enrollment used the current step so use the next
	sess, err := a.CompleteLogin(tok, code)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Username != "admin" || a.Session(sess.Token) == nil {
		t.Fatal("no session after 2FA")
	}
	// same code cant be replayed
	tok2, _ := a.Pending("admin")
	if _, err := a.CompleteLogin(tok2, code); err == nil {
		t.Fatal("replayed TOTP accepted")
	}
	// recovery codes only work once
	if _, err := a.CompleteLogin(tok2, codes[0]); err != nil {
		t.Fatalf("recovery code rejected: %v", err)
	}
	tok3, _ := a.Pending("admin")
	if _, err := a.CompleteLogin(tok3, codes[0]); err == nil {
		t.Fatal("recovery code reused")
	}
	if a.RecoveryLeft("admin") != recoveryN-1 {
		t.Errorf("recovery left = %d", a.RecoveryLeft("admin"))
	}
	if err := a.TOTPDisable("admin", "wrong"); err == nil {
		t.Fatal("disable without password")
	}
	if err := a.TOTPDisable("admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Login("admin", "correct horse battery"); err != nil {
		t.Fatalf("login after disabling 2FA: %v", err)
	}
}

func TestAPIKeys(t *testing.T) {
	a := newAuth(t)
	plain, k, err := a.CreateKey("home assistant", Viewer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "fg_") || k.Prefix != plain[:8] {
		t.Fatalf("key %q prefix %q", plain, k.Prefix)
	}
	s := a.KeySession(plain)
	if s == nil || s.Role != Viewer || s.Username != "key:home assistant" {
		t.Fatalf("KeySession = %+v", s)
	}
	if a.KeySession("fg_nope") != nil || a.KeySession(plain[:len(plain)-1]+"x") != nil {
		t.Fatal("bad key accepted")
	}
	if list := a.Keys(); len(list) != 1 || list[0].Hash != nil || list[0].LastUsed.IsZero() {
		t.Fatalf("Keys = %+v", list)
	}
	if err := a.DeleteKey(k.ID); err != nil {
		t.Fatal(err)
	}
	if a.KeySession(plain) != nil {
		t.Fatal("deleted key still works")
	}
}
