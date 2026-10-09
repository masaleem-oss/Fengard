package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// totp per rfc 6238 plus one time recovery codes for a lost phone

const (
	totpStep    = 30
	totpDigits  = 6
	pendingTTL  = 5 * time.Minute
	enrollTTL   = 10 * time.Minute
	recoveryN   = 8
	maxPendings = 64
)

var ErrTwoFactor = errors.New("two-factor code required")

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

type pending struct {
	user    string
	expires time.Time
}

type enrollment struct {
	secret  []byte
	expires time.Time
}

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, code%1000000)
}

// allows one step of clock drift either way and never the same step twice
func (a *Auth) verifyTOTP(username string, secret []byte, code string) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return false
	}
	now := time.Now().Unix() / totpStep
	for d := int64(-1); d <= 1; d++ {
		step := now + d
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			a.mu.Lock()
			used := a.lastStep[username] >= step
			if !used {
				a.lastStep[username] = step
			}
			a.mu.Unlock()
			return !used
		}
	}
	return false
}

// nothing is saved until TOTPEnable
func (a *Auth) TOTPSetup(username string) (secret, uri string, err error) {
	var u User
	if ok, err := a.users.Get(username, &u); !ok || err != nil {
		return "", "", errors.New("no such user")
	}
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	a.mu.Lock()
	a.enroll[username] = enrollment{secret: raw, expires: time.Now().Add(enrollTTL)}
	a.mu.Unlock()
	secret = b32.EncodeToString(raw)
	issuer := a.Issuer
	if issuer == "" {
		issuer = "Fengard"
	}
	uri = "otpauth://totp/" + url.PathEscape(issuer+":"+username) +
		"?secret=" + secret + "&issuer=" + url.QueryEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
	return secret, uri, nil
}

// recovery codes are only shown once
func (a *Auth) TOTPEnable(username, code string) ([]string, error) {
	a.mu.Lock()
	e, ok := a.enroll[username]
	a.mu.Unlock()
	if !ok || time.Now().After(e.expires) {
		return nil, errors.New("setup expired, start again")
	}
	if !a.verifyTOTP(username, e.secret, code) {
		return nil, errors.New("that code isn't right, check the time on your phone and try again")
	}
	var u User
	if ok, err := a.users.Get(username, &u); !ok || err != nil {
		return nil, errors.New("no such user")
	}
	codes := make([]string, recoveryN)
	u.Recovery = make([][]byte, recoveryN)
	for i := range codes {
		raw := make([]byte, 5)
		rand.Read(raw)
		c := strings.ToLower(b32.EncodeToString(raw))
		codes[i] = c[:4] + "-" + c[4:]
		h := sha256.Sum256([]byte(codes[i]))
		u.Recovery[i] = h[:]
	}
	u.TOTPSecret = e.secret
	u.TOTPSince = time.Now()
	if err := a.users.Put(username, u); err != nil {
		return nil, err
	}
	a.mu.Lock()
	delete(a.enroll, username)
	a.mu.Unlock()
	return codes, nil
}

func (a *Auth) TOTPDisable(username, password string) error {
	var u User
	if ok, err := a.users.Get(username, &u); !ok || err != nil {
		return errors.New("no such user")
	}
	if bcrypt.CompareHashAndPassword(u.Hash, []byte(password)) != nil {
		return errors.New("wrong password")
	}
	u.TOTPSecret, u.Recovery, u.TOTPSince = nil, nil, time.Time{}
	return a.users.Put(username, u)
}

// lost phone case
func (a *Auth) AdminResetTOTP(username string) error {
	var u User
	if ok, err := a.users.Get(username, &u); !ok || err != nil {
		return errors.New("no such user")
	}
	u.TOTPSecret, u.Recovery, u.TOTPSince = nil, nil, time.Time{}
	return a.users.Put(username, u)
}

// token is only good for CompleteLogin
func (a *Auth) Pending(username string) (string, error) {
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return "", err
	}
	t := base64url(tok)
	a.mu.Lock()
	now := time.Now()
	for k, p := range a.pendings {
		if now.After(p.expires) {
			delete(a.pendings, k)
		}
	}
	if len(a.pendings) >= maxPendings {
		a.mu.Unlock()
		return "", errors.New("too many sign-ins in progress, try again in a few minutes")
	}
	a.pendings[t] = pending{user: username, expires: now.Add(pendingTTL)}
	a.mu.Unlock()
	return t, nil
}

func (a *Auth) CompleteLogin(token, code string) (*Session, error) {
	a.mu.Lock()
	p, ok := a.pendings[token]
	a.mu.Unlock()
	if !ok || time.Now().After(p.expires) {
		return nil, errors.New("sign-in expired, start again")
	}
	var u User
	if ok, err := a.users.Get(p.user, &u); !ok || err != nil {
		return nil, ErrBadLogin
	}
	code = strings.ToLower(strings.TrimSpace(code))
	accepted := a.verifyTOTP(p.user, u.TOTPSecret, code)
	if !accepted {
		h := sha256.Sum256([]byte(code))
		for i, rc := range u.Recovery {
			if subtle.ConstantTimeCompare(rc, h[:]) == 1 {
				u.Recovery = append(u.Recovery[:i], u.Recovery[i+1:]...)
				if err := a.users.Put(p.user, u); err != nil {
					return nil, err
				}
				accepted = true
				break
			}
		}
	}
	if !accepted {
		return nil, errors.New("wrong code")
	}
	a.mu.Lock()
	delete(a.pendings, token)
	a.mu.Unlock()
	return a.StartSession(&u)
}

func (a *Auth) RecoveryLeft(username string) int {
	var u User
	a.users.Get(username, &u)
	return len(u.Recovery)
}
