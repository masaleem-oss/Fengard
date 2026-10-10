package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestInvalidListURLReturnsError(t *testing.T) {
	for _, url := range []string{"http://%zz", "http://", "file:///etc/passwd", "https://[broken"} {
		c := Default()
		c.Lists = []CustomList{{ID: "test", URL: url, Enabled: true}}
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted %q", url)
		}
	}
}

func TestFailedConfigStagingLeavesCurrentUsable(t *testing.T) {
	for _, failedPath := range []string{"history/00000002.json", "history/00000002.meta", "config.json"} {
		t.Run(failedPath, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, filepath.FromSlash(failedPath))
			backup := target + ".original"
			if failedPath == "config.json" {
				if err := os.Rename(target, backup); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Update("test", "failed", func(c *Config) error { c.Block = []string{"failed.example"}; return nil }); err == nil {
				t.Fatal("failed write reported success")
			}
			if s.Get().Version != 1 || len(s.Get().Block) != 0 {
				t.Fatal("memory changed after failure")
			}
			if len(s.History()) != 1 {
				t.Fatal("uncommitted generation advertised")
			}
			if _, err := s.Rollback("test", 2); err == nil {
				t.Fatal("uncommitted generation used for rollback")
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if failedPath == "config.json" {
				if err := os.Rename(backup, target); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if reopened.Get().Version != 1 || len(reopened.Get().Block) != 0 {
				t.Fatal("restart activated failed config")
			}
			if _, err := s.Update("test", "retry", func(c *Config) error { c.Block = []string{"retry.example"}; return nil }); err != nil {
				t.Fatal(err)
			}
			reopened, err = Open(dir)
			if err != nil || reopened.Get().Version != 2 || reopened.Get().Block[0] != "retry.example" {
				t.Fatalf("retry not committed: %v", err)
			}
		})
	}
}

func TestConfigCallbacksApplyInOrder(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var versions []int
	s.Subscribe(func(c *Config) {
		if c.Version == 2 {
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		versions = append(versions, c.Version)
		mu.Unlock()
	})
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update("test", fmt.Sprint(i), func(*Config) error { return nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(versions) != 17 {
		t.Fatalf("callbacks: %v", versions)
	}
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("out-of-order callbacks: %v", versions)
		}
	}
	s.Reconcile()
	if versions[len(versions)-1] != s.Get().Version {
		t.Fatal("reconcile applied stale config")
	}
}
