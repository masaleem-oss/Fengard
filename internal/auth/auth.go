package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/masaleem-oss/Fengard/internal/store"
)

type Role string

const (
	Admin  Role = "admin"  // full control
	Viewer Role = "viewer" // read only
)

type User struct {
	Username   string    `json:"username"`
	Hash       []byte    `json:"hash,omitempty"`
	Role       Role      `json:"role"`
	Created    time.Time `json:"created"`
	TOTPSecret []byte    `json:"totp,omitempty"`
	Recovery   [][]byte  `json:"recovery,omitempty"` // sha256 of unused recovery codes
	TOTPSince  time.Time `json:"totpSince,omitzero"`
	TwoFactor  bool      `json:"twoFactor"` // derived for listings
}

type Session struct {
	Token    string
	Username string
	Role     Role
	Created  time.Time
	Expires  time.Time
}

const (
	idleTimeout = time.Hour      // signed out after an hour idle
	sessionTTL  = 12 * time.Hour // and after 12h no matter what
	maxSessions = 256
	bcryptCost  = 10 // about 50ms on a pc and under a second on router cpus
)

var (
	ErrBadLogin  = errors.New("wrong username or password")
	ErrSetupDone = errors.New("setup has already been completed")
	usernameRe   = regexp.MustCompile(`^[a-zA-Z0-9._-]{2,32}$`)
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equalizer"), bcryptCost)
)

type Auth struct {
	users  *store.KV
	keys   *store.KV
	Issuer string // shown in authenticator apps

	mu       sync.Mutex
	sessions map[string]*Session
	pendings map[string]pending    // sign ins waiting on a second factor
	enroll   map[string]enrollment // 2fa setups in progress
	lastStep map[string]int64      // replay protection per user
}

func New(users, keys *store.KV) *Auth {
	return &Auth{
		users: users, keys: keys,
		sessions: map[string]*Session{}, pendings: map[string]pending{},
		enroll: map[string]enrollment{}, lastStep: map[string]int64{},
	}
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (a *Auth) NeedsSetup() bool { return a.users.Len() == 0 }

// only works once
func (a *Auth) Setup(username, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.NeedsSetup() {
		return ErrSetupDone
	}
	return a.create(username, password, Admin)
}

func (a *Auth) CreateUser(username, password string, role Role) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var existing User
	if ok, _ := a.users.Get(username, &existing); ok {
		return errors.New("that username is taken")
	}
	return a.create(username, password, role)
}

func (a *Auth) create(username, password string, role Role) error {
	if !usernameRe.MatchString(username) {
		return errors.New("username must be 2-32 letters, digits, dots, dashes or underscores")
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	if role != Admin && role != Viewer {
		return errors.New("role must be admin or viewer")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	return a.users.Put(username, User{Username: username, Hash: hash, Role: role, Created: time.Now()})
}

func checkPassword(p string) error {
	if len(p) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(p) > 72 {
		return errors.New("password must be at most 72 characters")
	}
	return nil
}

func (a *Auth) SetPassword(username, password string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	var u User
	if ok, err := a.users.Get(username, &u); !ok || err != nil {
		return errors.New("no such user")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return err
	}
	u.Hash = hash
	if err := a.users.Put(username, u); err != nil {
		return err
	}
	a.revokeUser(username)
	return nil
}

func (a *Auth) DeleteUser(username string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var u User
	if ok, _ := a.users.Get(username, &u); !ok {
		return errors.New("no such user")
	}
	if u.Role == Admin && a.adminCount() <= 1 {
		return errors.New("can't delete the last admin")
	}
	if err := a.users.Delete(username); err != nil {
		return err
	}
	for t, s := range a.sessions {
		if s.Username == username {
			delete(a.sessions, t)
		}
	}
	return nil
}

func (a *Auth) adminCount() int {
	n := 0
	a.users.Each(func(_ string, v []byte) error {
		var u User
		if json.Unmarshal(v, &u) == nil && u.Role == Admin {
			n++
		}
		return nil
	})
	return n
}

func (a *Auth) Users() []User {
	out := []User{}
	a.users.Each(func(_ string, v []byte) error {
		var u User
		if json.Unmarshal(v, &u) == nil {
			u.TwoFactor = len(u.TOTPSecret) > 0
			u.Hash, u.TOTPSecret, u.Recovery = nil, nil, nil
			out = append(out, u)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// returns the user with secrets
func (a *Auth) Authenticate(username, password string) (*User, error) {
	var u User
	ok, err := a.users.Get(username, &u)
	if err != nil {
		return nil, err
	}
	if !ok {
		// burn the same time as a real check so usernames cant be probed
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrBadLogin
	}
	if bcrypt.CompareHashAndPassword(u.Hash, []byte(password)) != nil {
		return nil, ErrBadLogin
	}
	return &u, nil
}

// 2fa accounts get ErrTwoFactor then go through Pending and CompleteLogin
func (a *Auth) Login(username, password string) (*Session, error) {
	u, err := a.Authenticate(username, password)
	if err != nil {
		return nil, err
	}
	if len(u.TOTPSecret) > 0 {
		return nil, ErrTwoFactor
	}
	return a.StartSession(u)
}

func (a *Auth) StartSession(u *User) (*Session, error) {
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	now := time.Now()
	s := &Session{
		Token:    base64url(tok),
		Username: u.Username, Role: u.Role,
		Created: now, Expires: now.Add(idleTimeout),
	}
	a.mu.Lock()
	a.prune()
	a.sessions[s.Token] = s
	a.mu.Unlock()
	return s, nil
}

// drops expired sessions then the oldest if still over the cap
func (a *Auth) prune() {
	now := time.Now()
	var oldest *Session
	for t, s := range a.sessions {
		if now.After(s.Expires) {
			delete(a.sessions, t)
		} else if oldest == nil || s.Expires.Before(oldest.Expires) {
			oldest = s
		}
	}
	if len(a.sessions) >= maxSessions && oldest != nil {
		delete(a.sessions, oldest.Token)
	}
}

func (a *Auth) Session(token string) *Session {
	if token == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for t, s := range a.sessions {
		if subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
			now := time.Now()
			if now.After(s.Expires) {
				delete(a.sessions, t)
				return nil
			}
			// activity extends it up to the hard limit
			s.Expires = now.Add(idleTimeout)
			if limit := s.Created.Add(sessionTTL); s.Expires.After(limit) {
				s.Expires = limit
			}
			return s
		}
	}
	return nil
}

func (a *Auth) Logout(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

func (a *Auth) revokeUser(username string) {
	a.mu.Lock()
	for t, s := range a.sessions {
		if s.Username == username {
			delete(a.sessions, t)
		}
	}
	a.mu.Unlock()
}
