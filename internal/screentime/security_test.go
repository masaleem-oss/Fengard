package screentime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

func TestRemovedBonusStaysRemovedAfterRestart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "screen.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	kv, err := db.KV("screentime")
	if err != nil {
		t.Fatal(err)
	}
	tracker := New(kv, time.Local)
	tracker.AddBonus("kid", 30)
	if err := tracker.Flush(); err != nil {
		t.Fatal(err)
	}
	tracker.AddBonus("kid", -30)
	if err := tracker.Flush(); err != nil {
		t.Fatal(err)
	}
	if restored := New(kv, time.Local); restored.Bonus("kid") != 0 {
		t.Fatal("revoked bonus restored")
	}
}

func TestFailedSnapshotRetriesWithoutNewActivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := db.KV("screentime")
	if err != nil {
		t.Fatal(err)
	}
	tracker := New(kv, time.Local)
	tracker.AddBonus("kid", 30)
	db.Close()
	if err := tracker.Flush(); err == nil {
		t.Fatal("closed storage write succeeded")
	}
	if !tracker.changed {
		t.Fatal("failed snapshot lost dirty state")
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tracker.kv, err = db.KV("screentime")
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Flush(); err != nil {
		t.Fatal(err)
	}
	if New(tracker.kv, time.Local).Bonus("kid") != 30 {
		t.Fatal("retry lost bonus")
	}
}

func TestRunPersistsFinalSnapshotBeforeReturning(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "screen.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	kv, _ := db.KV("screentime")
	tracker := New(kv, time.Local)
	tracker.AddBonus("kid", 15)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tracker.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not join")
	}
	if New(kv, time.Local).Bonus("kid") != 15 {
		t.Fatal("final bonus not persisted")
	}
}
