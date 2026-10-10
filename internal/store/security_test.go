package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestLogBudgetsBoundRecordsBytesAndGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounded.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := db.Log("queries")
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	if err := l.SetLimits(10, 2048); err != nil {
		t.Fatal(err)
	}
	for batch := range 100 {
		var records []Record
		for i := range 100 {
			records = append(records, Record{Time: time.Now(), Value: map[string]any{"n": batch*100 + i, "value": strings.Repeat("x", 160)}})
		}
		if err := l.Append(records...); err != nil {
			t.Fatal(err)
		}
	}
	count, size := 0, 0
	if err := l.Scan(time.Time{}, 1000, func(_ time.Time, v []byte) bool { count++; size += 8 + len(v); return true }); err != nil {
		t.Fatal(err)
	}
	if count > 10 || size > 2048 || l.Evicted() < 9990 {
		t.Fatalf("records %d bytes %d evicted %d", count, size, l.Evicted())
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 2<<20 {
		t.Fatalf("bounded records grew file unexpectedly: %v %v", info, err)
	}
	db.b.View(func(tx *bolt.Tx) error {
		n, bytes := readUsage(tx.Bucket(usageBucket), l.bucket)
		if int(n) != count || int(bytes) != size {
			t.Fatalf("usage differs from actual records: %d %d", n, bytes)
		}
		return nil
	})
	if _, err := l.Prune(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Record{Time: time.Now(), Value: "after prune"}); err != nil {
		t.Fatal(err)
	}
}

func TestLogSuspensionPreservesCriticalWrites(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "reserve.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := db.Log("queries")
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	kv, err := db.KV("users")
	if err != nil {
		t.Fatal(err)
	}
	for _, limits := range [][2]int64{{0, 1 << 60}, {1 << 20, 0}} {
		db.SetStorageBudget(limits[0], limits[1])
		if err := l.Append(Record{Time: time.Now(), Value: "drop"}); !errors.Is(err, ErrLogBudget) {
			t.Fatalf("log admission: %v", err)
		}
		if err := kv.Put("critical", "retained"); err != nil {
			t.Fatal(err)
		}
	}
	db.SetStorageBudget(0, 0)
	if err := l.Append(Record{Time: time.Now(), Value: "recovered"}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidBatchKeepsPreviousRecords(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := db.Log("queries")
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	if err := l.SetLimits(1, 1024); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Record{Time: time.Now(), Value: "previous"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Record{Time: time.Now(), Value: "first"}, Record{Time: time.Now(), Value: make(chan int)}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	var value string
	l.Scan(time.Time{}, 10, func(_ time.Time, data []byte) bool { json.Unmarshal(data, &value); return false })
	if value != "previous" {
		t.Fatal("failed batch evicted previous data")
	}
}

func TestUsageAndKeyOrderingSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := db.Log("queries")
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	at := time.Now()
	if err := l.Append(Record{Time: at, Value: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err = db.Log("queries")
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	if err := l.Append(Record{Time: at, Value: "second"}); err != nil {
		t.Fatal(err)
	}
	var values []string
	l.Scan(time.Time{}, 10, func(_ time.Time, data []byte) bool {
		var v string
		json.Unmarshal(data, &v)
		values = append(values, v)
		return true
	})
	if len(values) != 2 || values[0] != "second" || values[1] != "first" {
		t.Fatalf("restart records: %v", values)
	}
}

func TestReopenRecountsLogsWrittenByOlderVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := db.Log("queries")
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	if err := l.Append(Record{Time: time.Now(), Value: "initial"}); err != nil {
		t.Fatal(err)
	}
	err = db.b.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(l.bucket).Put(l.key(time.Now()), []byte(`"legacy write"`))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err = db.Log("queries")
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	err = db.b.View(func(tx *bolt.Tx) error {
		count, size := readUsage(tx.Bucket(usageBucket), l.bucket)
		if count != 2 || size != uint64(16+len(`"initial"`)+len(`"legacy write"`)) {
			t.Fatalf("recount after legacy write: %d records, %d bytes", count, size)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetLimits(1, 1024); err != nil {
		t.Fatal(err)
	}
	count := 0
	if err := l.Scan(time.Time{}, 10, func(_ time.Time, _ []byte) bool { count++; return true }); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recount failed to enforce retention: %d records", count)
	}
}

func TestInitialCustomLimitsPreserveExistingHistory(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "custom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("queries"))
		if err != nil {
			return err
		}
		for i := range 10001 {
			var key [8]byte
			binary.BigEndian.PutUint64(key[:], uint64(i+1))
			if err := b.Put(key[:], []byte(`"legacy query"`)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := db.Log("queries", LogLimits{MaxRecords: 20000, MaxBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	<-l.Settled()
	count := 0
	if err := l.Scan(time.Time{}, 20000, func(_ time.Time, _ []byte) bool { count++; return true }); err != nil {
		t.Fatal(err)
	}
	if count != 10001 || l.Evicted() != 0 {
		t.Fatalf("initial custom limits lost history: %d records, %d evicted", count, l.Evicted())
	}
}

func TestBigOldLogOpensFastAndLoggingResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	value := []byte(strings.Repeat("x", 200))
	for batch := 0; batch < 30; batch++ {
		err := raw.Update(func(tx *bolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists([]byte("queries"))
			if err != nil {
				return err
			}
			for i := 0; i < 2000; i++ {
				k := make([]byte, 8)
				binary.BigEndian.PutUint64(k, uint64(batch*2000+i+1))
				if err := b.Put(k, value); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	info, _ := os.Stat(path)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetStorageBudget(info.Size(), 0)
	start := time.Now()
	l, err := db.Log("queries", LogLimits{MaxRecords: 1000, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("opening the old log took %v", time.Since(start))
	}
	<-l.Settled()
	if err := l.Append(Record{Time: time.Now(), Value: "after"}); err != nil {
		t.Fatalf("logging still suspended after the trim freed space: %v", err)
	}
}
