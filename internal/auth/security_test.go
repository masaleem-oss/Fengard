package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func enableTestTOTP(t *testing.T, a *Auth) []string {
	t.Helper()
	secret, _, err := a.TOTPSetup("admin")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := b32.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := a.TOTPEnable("admin", totpCode(raw, time.Now().Unix()/totpStep))
	if err != nil {
		t.Fatal(err)
	}
	return codes
}

func TestRecoveryCodeConcurrentUse(t *testing.T) {
	for _, samePending := range []bool{true, false} {
		t.Run(map[bool]string{true: "same pending", false: "different pendings"}[samePending], func(t *testing.T) {
			a := newAuth(t)
			codes := enableTestTOTP(t, a)
			first, err := a.Pending("admin")
			if err != nil {
				t.Fatal(err)
			}
			tokens := make([]string, 16)
			for i := range tokens {
				tokens[i] = first
				if !samePending {
					tokens[i], err = a.Pending("admin")
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			start := make(chan struct{})
			var successes atomic.Int32
			var wg sync.WaitGroup
			for _, token := range tokens {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					if _, err := a.CompleteLogin(token, codes[0]); err == nil {
						successes.Add(1)
					}
				}()
			}
			close(start)
			wg.Wait()
			if successes.Load() != 1 {
				t.Fatalf("recovery code accepted %d times", successes.Load())
			}
			if a.RecoveryLeft("admin") != recoveryN-1 {
				t.Fatal("wrong recovery count")
			}
		})
	}
}

func TestTwoFactorPasswordRotationInvalidatesPending(t *testing.T) {
	a := newAuth(t)
	codes := enableTestTOTP(t, a)
	_, token, err := a.BeginLogin("admin", "correct horse battery")
	if err != nil || token == "" {
		t.Fatalf("pending: %v", err)
	}
	if err := a.ChangePassword("admin", "wrong password", "new correct password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err := a.ChangePassword("admin", "correct horse battery", "new correct password"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CompleteLogin(token, codes[0]); err == nil {
		t.Fatal("stale pending accepted")
	}
	if _, _, err := a.BeginLogin("admin", "correct horse battery"); err == nil {
		t.Fatal("old password accepted")
	}
	sess, token, err := a.BeginLogin("admin", "new correct password")
	if err != nil || sess != nil || token == "" {
		t.Fatalf("two-factor lost: %v", err)
	}
	if _, err := a.CompleteLogin(token, codes[0]); err != nil {
		t.Fatal(err)
	}
}

func TestAccountSecurityUpdatesStayMerged(t *testing.T) {
	a := newAuth(t)
	enableTestTOTP(t, a)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- a.SetPassword("admin", "new correct password") }()
	go func() { <-start; results <- a.AdminResetTOTP("admin") }()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Login("admin", "new correct password"); err != nil {
		t.Fatalf("security updates overwritten: %v", err)
	}
	if _, err := a.Login("admin", "correct horse battery"); err == nil {
		t.Fatal("old password remains")
	}
}

func TestRevokedKeyCannotReturnThroughUsageWrite(t *testing.T) {
	a := newAuth(t)
	for _, role := range []Role{Viewer, Admin} {
		plain, key, err := a.CreateKey("revocation", role)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; a.KeySession(plain) }()
		}
		close(start)
		if err := a.DeleteKey(key.ID); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if a.KeySession(plain) != nil {
			t.Fatal("revoked key authenticated")
		}
		if len(a.Keys()) != 0 {
			t.Fatal("revoked key recreated")
		}
	}
}

func TestSessionReturnedAsSnapshot(t *testing.T) {
	a := newAuth(t)
	sess, err := a.Login("admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	sess.Role = Viewer
	snapshot := a.Session(sess.Token)
	if snapshot.Role != Admin {
		t.Fatal("caller changed stored session")
	}
	snapshot.Expires = time.Time{}
	if a.Session(sess.Token) == nil {
		t.Fatal("caller expired stored session")
	}
}

func TestTwoFactorCanBeReenrolledAfterDisable(t *testing.T) {
	a := newAuth(t)
	enableTestTOTP(t, a)
	if err := a.TOTPDisable("admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	enableTestTOTP(t, a)
}

func TestConcurrentKeyCreationHonorsCap(t *testing.T) {
	a := newAuth(t)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range maxKeys + 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := a.CreateKey("capacity", Viewer); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != maxKeys || len(a.Keys()) != maxKeys {
		t.Fatalf("created %d keys", successes.Load())
	}
}
