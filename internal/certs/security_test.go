package certs

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentImportsPersistOneCompleteIdentity(t *testing.T) {
	a, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bundle := a.Export()
			if i%2 == 0 {
				bundle = b.Export()
			}
			if err := target.Import(bundle); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reopened, err := LoadOrCreate(target.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reopened.Export(), target.Export()) {
		t.Fatal("disk and memory CA differ")
	}
}

func TestFailedCAImportRetainsPreviousIdentity(t *testing.T) {
	a, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(b.dir, caBundleFile)
	backup := target + ".original"
	if err := os.Rename(target, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	before := b.Export()
	if err := b.Import(a.Export()); err == nil {
		t.Fatal("failed import reported success")
	}
	if !bytes.Equal(b.Export(), before) {
		t.Fatal("failed import changed active CA")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, target); err != nil {
		t.Fatal(err)
	}
	reopened, err := LoadOrCreate(b.dir)
	if err != nil || !bytes.Equal(reopened.Export(), before) {
		t.Fatalf("previous CA not usable: %v", err)
	}
}

func TestLegacyCAFilesMigrateWithoutChangingTrust(t *testing.T) {
	original, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st := original.st.Load()
	if err := os.WriteFile(filepath.Join(dir, caCertFile), st.certPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, caKeyFile), st.keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := LoadOrCreate(dir)
	if err != nil || migrated.Fingerprint() != original.Fingerprint() {
		t.Fatalf("migration changed trust: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, caBundleFile)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, caKeyFile))
	if err != nil || !bytes.Equal(data, st.keyPEM) {
		t.Fatal("migration broke legacy key")
	}
}
