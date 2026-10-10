package querylog

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

func TestCloseDrainsSmallBatchAndHourlyTotals(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "close.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, _ := db.Log("queries")
	hours, _ := db.KV("hours")
	l := New(100, rows, hours)
	for range 17 {
		l.Add(Entry{Time: time.Now(), Domain: "example.com", Action: "allowed"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(ctx); err != nil {
		t.Fatal(err)
	}
	restored := New(100, rows, hours)
	defer restored.Close(context.Background())
	if len(restored.History(Filter{Limit: 100})) != 17 {
		t.Fatal("queued tail lost")
	}
	if restored.Stats().Total != 17 {
		t.Fatal("recent stats lost")
	}
	var h Hour
	if ok, err := hours.Get(hourKey(time.Now().Truncate(time.Hour)), &h); !ok || err != nil || h.Total != 17 {
		t.Fatalf("hourly totals: %+v %v", h, err)
	}
}

func TestAddAndCloseAreSafeAndAccountForEveryEntry(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, _ := db.Log("queries")
	l := New(100, rows, nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				l.Add(Entry{Time: time.Now(), Domain: "example.com", Action: "allowed"})
			}
		}()
	}
	if err := l.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if got := uint64(len(l.History(Filter{Limit: 1000}))) + l.Stats().Dropped; got != 800 {
		t.Fatalf("entries unaccounted: %d", got)
	}
}

func TestFlushIncludesQueuedQueries(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "flush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, _ := db.Log("queries")
	l := New(100, rows, nil)
	defer l.Close(context.Background())
	l.Add(Entry{Time: time.Now(), Domain: "example.com", Action: "allowed"})
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(l.History(Filter{Limit: 100})) != 1 {
		t.Fatal("flush omitted queued query")
	}
}
