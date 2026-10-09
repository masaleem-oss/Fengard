package store

import (
	"encoding/binary"
	"encoding/json"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

type DB struct {
	b *bolt.DB
}

func Open(path string) (*DB, error) {
	b, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second, NoFreelistSync: true})
	if err != nil {
		return nil, err
	}
	return &DB{b: b}, nil
}

func (d *DB) Close() error { return d.b.Close() }

type Log struct {
	db     *DB
	bucket []byte
	mu     sync.Mutex
	last   uint64
}

func (d *DB) Log(name string) (*Log, error) {
	err := d.b.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		return err
	})
	return &Log{db: d, bucket: []byte(name)}, err
}

func (l *Log) key(t time.Time) []byte {
	l.mu.Lock()
	n := uint64(t.UnixNano())
	if n <= l.last {
		n = l.last + 1
	}
	l.last = n
	l.mu.Unlock()
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, n)
	return k
}

type Record struct {
	Time  time.Time
	Value any
}

func (l *Log) Append(recs ...Record) error {
	return l.db.b.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(l.bucket)
		b.FillPercent = 1 // keys always go up
		for _, r := range recs {
			v, err := json.Marshal(r.Value)
			if err != nil {
				return err
			}
			if err := b.Put(l.key(r.Time), v); err != nil {
				return err
			}
		}
		return nil
	})
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
			for _, k := range keys {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
			n = len(keys)
			return nil
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
