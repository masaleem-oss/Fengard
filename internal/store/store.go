package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

type DB struct {
	b            *bolt.DB
	logsMu       sync.Mutex
	logs         map[string]*Log
	budgetMu     sync.Mutex
	maxFile      int64
	reserve      int64
	spaceChecked time.Time
	spaceErr     error
	closing      atomic.Bool
	bg           sync.WaitGroup
}

func Open(path string) (*DB, error) {
	b, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second, NoFreelistSync: true})
	if err != nil {
		return nil, err
	}
	b.AllocSize = 256 << 10
	return &DB{b: b, logs: map[string]*Log{}, maxFile: 16 << 20, reserve: 4 << 20}, nil
}

func (d *DB) Close() error {
	d.closing.Store(true)
	d.bg.Wait()
	return d.b.Close()
}

type Log struct {
	db         *DB
	bucket     []byte
	mu         sync.Mutex
	last       uint64
	maxRecords int
	maxBytes   int64
	evicted    atomic.Uint64
	settled    chan struct{}
}

type LogLimits struct {
	MaxRecords int
	MaxBytes   int64
}

var ErrLogBudget = errors.New("detailed logging suspended to preserve storage for critical state")

var usageBucket = []byte("_logusage")

func (d *DB) SetStorageBudget(maxFile, reserve int64) {
	d.budgetMu.Lock()
	defer d.budgetMu.Unlock()
	d.maxFile, d.reserve = maxFile, reserve
	d.spaceChecked = time.Time{}
}

func (d *DB) logAdmission() error {
	d.budgetMu.Lock()
	defer d.budgetMu.Unlock()
	info, err := os.Stat(d.b.Path())
	if err != nil {
		return err
	}
	// the file never shrinks so count free pages as room
	st := d.b.Stats()
	used := info.Size() - int64(st.FreePageN+st.PendingPageN)*int64(d.b.Info().PageSize)
	if d.maxFile > 0 && used >= d.maxFile-(1<<20) {
		return ErrLogBudget
	}
	if d.reserve <= 0 {
		return nil
	}
	if time.Since(d.spaceChecked) >= time.Second {
		free, err := diskFree(filepath.Dir(d.b.Path()))
		d.spaceChecked = time.Now()
		d.spaceErr = err
		if err == nil && free < uint64(d.reserve+(1<<20)) {
			d.spaceErr = ErrLogBudget
		}
	}
	return d.spaceErr
}

func (d *DB) Log(name string, limits ...LogLimits) (*Log, error) {
	d.logsMu.Lock()
	defer d.logsMu.Unlock()
	if existing := d.logs[name]; existing != nil {
		if len(limits) > 0 {
			if err := existing.SetLimits(limits[0].MaxRecords, limits[0].MaxBytes); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	l := &Log{db: d, bucket: []byte(name), maxRecords: 2000, maxBytes: 512 << 10}
	if name == "queries" {
		l.maxRecords, l.maxBytes = 10000, 4<<20
	}
	if name == "audit" {
		l.maxRecords, l.maxBytes = 10000, 2<<20 // a year of admin changes fits easily
	}
	if len(limits) > 0 {
		l.maxRecords, l.maxBytes = limits[0].MaxRecords, limits[0].MaxBytes
		if l.maxRecords < 1 || l.maxBytes < 1024 {
			return nil, errors.New("log limits must allow at least one record and 1024 bytes")
		}
	}
	err := d.b.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(l.bucket)
		if err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(usageBucket); err != nil {
			return err
		}
		if key, _ := b.Cursor().Last(); len(key) == 8 {
			l.last = binary.BigEndian.Uint64(key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// recount and trim in the background so a big old log cant hold up dns at boot
	l.settled = make(chan struct{})
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		defer close(l.settled)
		if err := l.recount(); err != nil {
			log.Printf("%s log recount: %v", name, err)
			return
		}
		if err := l.trim(l.limits()); err != nil {
			log.Printf("%s log trim: %v", name, err)
		}
	}()
	d.logs[name] = l
	return l, nil
}

// Settled closes once the startup recount and trim are done
func (l *Log) Settled() <-chan struct{} { return l.settled }

func (l *Log) limits() (int, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxRecords, l.maxBytes
}

// older versions write without updating the usage counts
func (l *Log) recount() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.db.b.Update(func(tx *bolt.Tx) error {
		var count, size uint64
		if err := tx.Bucket(l.bucket).ForEach(func(k, v []byte) error { count++; size += uint64(len(k) + len(v)); return nil }); err != nil {
			return err
		}
		return putUsage(tx.Bucket(usageBucket), l.bucket, count, size)
	})
}

// small transactions and the lock let go between them so writes keep flowing
func (l *Log) trim(maxRecords int, maxBytes int64) error {
	for !l.db.closing.Load() {
		removed := 0
		l.mu.Lock()
		err := l.db.b.Update(func(tx *bolt.Tx) error {
			meta := tx.Bucket(usageBucket)
			count, size := readUsage(meta, l.bucket)
			var err error
			removed, err = trimLog(tx.Bucket(l.bucket), &count, &size, maxRecords, maxBytes, 2000)
			if err != nil {
				return err
			}
			return putUsage(meta, l.bucket, count, size)
		})
		l.mu.Unlock()
		if err != nil {
			return err
		}
		l.evicted.Add(uint64(removed))
		if removed < 2000 {
			return nil
		}
	}
	return nil
}

func readUsage(meta *bolt.Bucket, key []byte) (uint64, uint64) {
	data := meta.Get(key)
	if len(data) != 16 {
		return 0, 0
	}
	return binary.BigEndian.Uint64(data[:8]), binary.BigEndian.Uint64(data[8:])
}

func putUsage(meta *bolt.Bucket, key []byte, count, size uint64) error {
	var data [16]byte
	binary.BigEndian.PutUint64(data[:8], count)
	binary.BigEndian.PutUint64(data[8:], size)
	return meta.Put(key, data[:])
}

func trimLog(b *bolt.Bucket, count, size *uint64, maxRecords int, maxBytes int64, chunk int) (int, error) {
	var keys [][]byte
	cur := b.Cursor()
	for k, v := cur.First(); k != nil && (*count > uint64(maxRecords) || *size > uint64(maxBytes)) && len(keys) < chunk; k, v = cur.Next() {
		keys = append(keys, append([]byte(nil), k...))
		*count--
		*size -= uint64(len(k) + len(v))
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

func (l *Log) SetLimits(maxRecords int, maxBytes int64) error {
	if maxRecords < 1 || maxBytes < 1024 {
		return errors.New("log limits must allow at least one record and 1024 bytes")
	}
	<-l.settled
	l.mu.Lock()
	l.maxRecords, l.maxBytes = maxRecords, maxBytes
	l.mu.Unlock()
	return l.trim(maxRecords, maxBytes)
}

func (l *Log) Evicted() uint64 { return l.evicted.Load() }

func (l *Log) key(t time.Time) []byte {
	n := uint64(t.UnixNano())
	if n <= l.last {
		n = l.last + 1
	}
	l.last = n
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, n)
	return k
}

type Record struct {
	Time  time.Time
	Value any
}

func (l *Log) Append(recs ...Record) error {
	if len(recs) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.db.logAdmission(); err != nil {
		return err
	}
	values := make([][]byte, len(recs))
	for i, r := range recs {
		data, err := json.Marshal(r.Value)
		if err != nil {
			return err
		}
		if int64(len(data)+8) > l.maxBytes {
			return fmt.Errorf("record exceeds detailed log byte limit: %w", ErrLogBudget)
		}
		values[i] = data
	}
	removed := 0
	err := l.db.b.Update(func(tx *bolt.Tx) error {
		b, meta := tx.Bucket(l.bucket), tx.Bucket(usageBucket)
		count, size := readUsage(meta, l.bucket)
		b.FillPercent = 1
		for i, r := range recs {
			count++
			size += uint64(len(values[i]) + 8)
			n, err := trimLog(b, &count, &size, l.maxRecords, l.maxBytes, l.maxRecords)
			if err != nil {
				return err
			}
			removed += n
			if err := b.Put(l.key(r.Time), values[i]); err != nil {
				return err
			}
		}
		return putUsage(meta, l.bucket, count, size)
	})
	if err == nil {
		l.evicted.Add(uint64(removed))
	}
	return err
}

// stops after maxScan records so a search cant pin the cpu
func (l *Log) Scan(before time.Time, maxScan int, fn func(t time.Time, v []byte) bool) error {
	return l.db.b.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(l.bucket).Cursor()
		var k, v []byte
		if before.IsZero() {
			k, v = c.Last()
		} else {
			seek := make([]byte, 8)
			binary.BigEndian.PutUint64(seek, uint64(before.UnixNano()))
			k, v = c.Seek(seek)
			if k == nil {
				k, v = c.Last()
			} else {
				k, v = c.Prev()
			}
		}
		for n := 0; k != nil && n < maxScan; k, v = c.Prev() {
			n++
			if !fn(time.Unix(0, int64(binary.BigEndian.Uint64(k))), v) {
				break
			}
		}
		return nil
	})
}

// deletes in chunks to keep transactions short
func (l *Log) Prune(cutoff time.Time) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	end := make([]byte, 8)
	binary.BigEndian.PutUint64(end, uint64(cutoff.UnixNano()))
	total := 0
	for {
		n := 0
		err := l.db.b.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket(l.bucket)
			// collect first since deleting while iterating a bbolt cursor skips keys
			var keys [][]byte
			c := b.Cursor()
			for k, _ := c.First(); k != nil && string(k) < string(end) && len(keys) < 5000; k, _ = c.Next() {
				keys = append(keys, append([]byte(nil), k...))
			}
			meta := tx.Bucket(usageBucket)
			count, size := readUsage(meta, l.bucket)
			for _, k := range keys {
				count--
				size -= uint64(len(k) + len(b.Get(k)))
				if err := b.Delete(k); err != nil {
					return err
				}
			}
			n = len(keys)
			return putUsage(meta, l.bucket, count, size)
		})
		total += n
		if err != nil || n < 5000 {
			return total, err
		}
	}
}

type KV struct {
	db     *DB
	bucket []byte
}

func (d *DB) KV(name string) (*KV, error) {
	err := d.b.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		return err
	})
	return &KV{db: d, bucket: []byte(name)}, err
}

func (kv *KV) Put(key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return kv.db.b.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(kv.bucket).Put([]byte(key), data)
	})
}

func (kv *KV) Get(key string, v any) (ok bool, err error) {
	err = kv.db.b.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(kv.bucket).Get([]byte(key))
		if data == nil {
			return nil
		}
		ok = true
		return json.Unmarshal(data, v)
	})
	return ok, err
}

func (kv *KV) Delete(key string) error {
	return kv.db.b.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(kv.bucket).Delete([]byte(key))
	})
}

func (kv *KV) Len() (n int) {
	kv.db.b.View(func(tx *bolt.Tx) error {
		n = tx.Bucket(kv.bucket).Stats().KeyN
		return nil
	})
	return n
}

func (kv *KV) Each(fn func(key string, v []byte) error) error {
	return kv.db.b.View(func(tx *bolt.Tx) error {
		return tx.Bucket(kv.bucket).ForEach(func(k, v []byte) error { return fn(string(k), v) })
	})
}

func (kv *KV) ReplacePrefix(prefix string, values map[string]any) error {
	encoded := make(map[string][]byte, len(values))
	for key, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded[key] = data
	}
	return kv.db.b.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(kv.bucket)
		var keys [][]byte
		cur := b.Cursor()
		for key, _ := cur.First(); key != nil; key, _ = cur.Next() {
			if strings.HasPrefix(string(key), prefix) {
				keys = append(keys, append([]byte(nil), key...))
			}
		}
		for _, key := range keys {
			if err := b.Delete(key); err != nil {
				return err
			}
		}
		for key, data := range encoded {
			if err := b.Put([]byte(key), data); err != nil {
				return err
			}
		}
		return nil
	})
}
