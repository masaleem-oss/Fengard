package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// only the hash is stored and the plain key is shown once
type APIKey struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Role     Role      `json:"role"`
	Prefix   string    `json:"prefix"` // first 8 chars so you can spot it in the list
	Hash     []byte    `json:"hash,omitempty"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
}

const maxKeys = 50

func (a *Auth) CreateKey(name string, role Role) (string, APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return "", APIKey{}, errors.New("give the key a name (up to 60 characters)")
	}
	if role != Admin && role != Viewer {
		return "", APIKey{}, errors.New("role must be admin or viewer")
	}
	if a.keys.Len() >= maxKeys {
		return "", APIKey{}, errors.New("too many API keys; remove one first")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", APIKey{}, err
	}
	plain := "fg_" + base64url(raw)
	h := sha256.Sum256([]byte(plain))
	k := APIKey{ID: hex.EncodeToString(raw[:6]), Name: name, Role: role, Prefix: plain[:8], Hash: h[:], Created: time.Now()}
	if err := a.keys.Put(k.ID, k); err != nil {
		return "", APIKey{}, err
	}
	return plain, k, nil
}

func (a *Auth) Keys() []APIKey {
	out := []APIKey{}
	a.keys.Each(func(_ string, v []byte) error {
		var k APIKey
		if json.Unmarshal(v, &k) == nil {
			k.Hash = nil
			out = append(out, k)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (a *Auth) DeleteKey(id string) error {
	var k APIKey
	if ok, _ := a.keys.Get(id, &k); !ok {
		return errors.New("no such key")
	}
	return a.keys.Delete(id)
}

func (a *Auth) KeySession(token string) *Session {
	if !strings.HasPrefix(token, "fg_") {
		return nil
	}
	h := sha256.Sum256([]byte(token))
	var found *APIKey
	a.keys.Each(func(_ string, v []byte) error {
		var k APIKey
		if json.Unmarshal(v, &k) == nil && subtle.ConstantTimeCompare(k.Hash, h[:]) == 1 {
			found = &k
		}
		return nil
	})
	if found == nil {
		return nil
	}
	// only record use once a minute so reads dont turn into writes
	if time.Since(found.LastUsed) > time.Minute {
		found.LastUsed = time.Now()
		a.keys.Put(found.ID, *found)
	}
	return &Session{Token: "", Username: "key:" + found.Name, Role: found.Role, Created: time.Now(), Expires: time.Now().Add(time.Minute)}
}
