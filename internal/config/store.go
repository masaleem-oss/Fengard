package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masaleem-oss/Fengard/internal/persist"
)

const keepVersions = 30

type Store struct {
	path    string
	histDir string

	cur     atomic.Pointer[Config]
	applyMu sync.Mutex
	mu      sync.Mutex
	subs    []func(*Config)
}

type Version struct {
	Version int       `json:"version"`
	Time    time.Time `json:"time"`
	Summary string    `json:"summary"`
	Actor   string    `json:"actor"`
}

func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "config.json"), histDir: filepath.Join(dir, "history")}
	if err := os.MkdirAll(s.histDir, 0o700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		c := Default()
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("default config invalid: %w", err)
		}
		c.Version, c.Updated = 1, time.Now()
		if err := s.persist(c, "Initial setup", "system"); err != nil {
			return nil, err
		}
		s.cur.Store(c)
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	s.cur.Store(c)
	return s, nil
}

// dont modify the result its shared
func (s *Store) Get() *Config { return s.cur.Load() }

func (s *Store) Subscribe(fn func(*Config)) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
	fn(s.Get())
}

func (s *Store) Update(actor, summary string, fn func(*Config) error) (*Config, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	next := s.Get().Clone()
	if err := fn(next); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if err := next.Validate(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	next.Version = s.Get().Version + 1
	next.Updated = time.Now()
	if err := s.persist(next, summary, actor); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.cur.Store(next)
	s.prune()
	subs := append([]func(*Config){}, s.subs...)
	s.mu.Unlock()

	for _, fn := range subs {
		fn(next)
	}
	return next, nil
}

func (s *Store) Reconcile() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.mu.Lock()
	subs := append([]func(*Config){}, s.subs...)
	c := s.Get()
	s.mu.Unlock()
	for _, fn := range subs {
		fn(c)
	}
}

func (s *Store) Replace(actor, summary string, c *Config) (*Config, error) {
	return s.Update(actor, summary, func(cur *Config) error {
		*cur = *c.Clone()
		return nil
	})
}

func (s *Store) persist(c *Config, summary, actor string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(Version{c.Version, c.Updated, summary, actor})
	name := filepath.Join(s.histDir, fmt.Sprintf("%08d", c.Version))
	if err := writeAtomic(name+".json", data); err != nil {
		return err
	}
	if err := writeAtomic(name+".meta", meta); err != nil {
		return err
	}
	if err := writeAtomic(s.path, data); err != nil {
		return err
	}
	return nil
}

func (s *Store) prune() {
	vs := s.versionNumbers()
	for len(vs) > keepVersions {
		base := filepath.Join(s.histDir, fmt.Sprintf("%08d", vs[0]))
		os.Remove(base + ".json")
		os.Remove(base + ".meta")
		vs = vs[1:]
	}
}

func (s *Store) versionNumbers() []int {
	entries, _ := os.ReadDir(s.histDir)
	var vs []int
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			if v, err := strconv.Atoi(n); err == nil {
				if c := s.cur.Load(); c == nil || v <= c.Version {
					vs = append(vs, v)
				}
			}
		}
	}
	sort.Ints(vs)
	return vs
}

func (s *Store) History() []Version {
	vs := s.versionNumbers()
	out := make([]Version, 0, len(vs))
	for i := len(vs) - 1; i >= 0; i-- {
		var v Version
		data, err := os.ReadFile(filepath.Join(s.histDir, fmt.Sprintf("%08d.meta", vs[i])))
		if err == nil && json.Unmarshal(data, &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) Rollback(actor string, version int) (*Config, error) {
	old, err := s.At(version)
	if err != nil {
		return nil, err
	}
	return s.Replace(actor, fmt.Sprintf("Rolled back to version %d", version), old)
}

// At reads an old version from history without applying it
func (s *Store) At(version int) (*Config, error) {
	if version < 1 || version > s.Get().Version {
		return nil, fmt.Errorf("version %d not found", version)
	}
	data, err := os.ReadFile(filepath.Join(s.histDir, fmt.Sprintf("%08d.json", version)))
	if err != nil {
		return nil, fmt.Errorf("version %d not found", version)
	}
	old := &Config{}
	if err := json.Unmarshal(data, old); err != nil {
		return nil, err
	}
	return old, nil
}

func writeAtomic(path string, data []byte) error {
	return persist.WriteFile(path, data, 0o600)
}
