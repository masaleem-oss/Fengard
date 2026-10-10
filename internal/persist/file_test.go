package persist

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicReplacementAndTemporaryCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, []byte("new")) {
		t.Fatalf("replacement failed: %q %v", data, err)
	}
	blocked := filepath.Join(dir, "directory")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(blocked, []byte("invalid"), 0600); err == nil {
		t.Fatal("failed rename reported success")
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".fengard-*"))
	if err != nil || len(temps) != 0 {
		t.Fatal("temporary files left behind")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("secret file permissions changed")
		}
	}
}
