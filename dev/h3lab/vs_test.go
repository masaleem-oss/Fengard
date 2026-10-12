//go:build linux

package h3lab

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"

	h3 "github.com/masaleem-oss/Fengard/internal/fengard-h3"
)

// fengard-h3 against quic-go's own http/3 client, the way a dns forwarder like adguard's dnsproxy uses it.
// the server runs in its own process so cpu and memory are only the client's.
//   go test -run XXX -bench Vs .

// TestServe is the server process, it only runs when the benchmark starts it
func TestServe(t *testing.T) {
	out := os.Getenv("H3LAB_SERVE")
	if out == "" {
		t.Skip("started by the vs benchmark")
	}
	idle, _ := time.ParseDuration(os.Getenv("H3LAB_IDLE"))
	s := startServer(t, serverOpts{idle: idle})
	der := s.cert.Certificate[0]
	os.WriteFile(out+".pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(out, []byte(s.addr()), 0o644)
	time.Sleep(10 * time.Minute)
}

var (
	vsOnce sync.Once
	vsAddr string
	vsPool *x509.CertPool
)

func vsServer(b *testing.B) (string, *x509.CertPool) { return vsServerIdle(b, "") }

var quietOnce sync.Once
var quietAddr string

func vsServerIdle(b *testing.B, idle string) (string, *x509.CertPool) {
	once, addr := &vsOnce, &vsAddr
	if idle != "" {
		once, addr = &quietOnce, &quietAddr
	}
	once.Do(func() {
		f := b.TempDir() + "/addr"
		cmd := exec.Command(os.Args[0], "-test.run", "^TestServe$")
		cmd.Env = append(os.Environ(), "H3LAB_SERVE="+f, "H3LAB_IDLE="+idle)
		if err := cmd.Start(); err != nil {
			b.Fatal(err)
		}
		go func() { <-time.After(10 * time.Minute); cmd.Process.Kill() }()
		for range 200 {
			if a, err := os.ReadFile(f); err == nil && len(a) > 0 {
				*addr = string(a)
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		pemBytes, _ := os.ReadFile(f + ".pem")
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pemBytes)
		if vsPool == nil {
			vsPool = pool
		}
	})
	if *addr == "" {
		b.Fatal("server didn't start")
	}
	return *addr, vsPool
}

// a lookup after the house went quiet for longer than the server keeps an idle connection
func BenchmarkVsAfterQuiet(b *testing.B) {
	addr, pool := vsServerIdle(b, "1s")
	for _, name := range vsNames {
		b.Run(name, func(b *testing.B) {
			c := newVs(name, pool)
			defer c.close()
			u, msg := url(addr), query("quiet.test")
			var times []time.Duration
			failed := 0
			for b.Loop() {
				if _, err := c.ask(context.Background(), u, msg); err != nil {
					b.Fatal(err)
				}
				time.Sleep(1500 * time.Millisecond)
				start := time.Now()
				// a forwarder asks again when the first try fails, so failures count as the time to the answer
				if _, err := c.ask(context.Background(), u, msg); err != nil {
					failed++
					b.Logf("%s: %v", name, err)
					if _, err := c.ask(context.Background(), u, msg); err != nil {
						b.Fatal(err)
					}
				}
				times = append(times, time.Since(start))
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			b.ReportMetric(float64(times[len(times)/2].Microseconds()), "µs-median")
			b.ReportMetric(float64(failed), "failed-first-tries")
		})
	}
}

type client interface {
	ask(ctx context.Context, url string, msg []byte) ([]byte, error)
	close()
}

type ours struct{ c *h3.Client }

func (o ours) ask(ctx context.Context, url string, msg []byte) ([]byte, error) {
	return o.c.Exchange(ctx, url, msg)
}
func (o ours) close() { o.c.Close() }

type quicGo struct{ t *http3.Transport }

func (q quicGo) ask(ctx context.Context, url string, msg []byte) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(msg))
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := q.t.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
func (q quicGo) close() { q.t.Close() }

func newVs(name string, pool *x509.CertPool) client {
	if name == "fengard-h3" {
		return ours{&h3.Client{TLSConfig: &tls.Config{RootCAs: pool}}}
	}
	return quicGo{&http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

func cpuTime() time.Duration {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

var vsNames = []string{"fengard-h3", "quic-go"}

// one lookup at a time over an open connection
func BenchmarkVsSerial(b *testing.B) {
	addr, pool := vsServer(b)
	for _, name := range vsNames {
		b.Run(name, func(b *testing.B) {
			c := newVs(name, pool)
			defer c.close()
			u, msg := url(addr), query("bench.test")
			if _, err := c.ask(context.Background(), u, msg); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			cpu0 := cpuTime()
			b.ResetTimer()
			for b.Loop() {
				if _, err := c.ask(context.Background(), u, msg); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64((cpuTime()-cpu0).Microseconds())/float64(b.N), "cpu-µs/op")
		})
	}
}

// 64 lookups at once on one connection, like a busy house
func BenchmarkVsParallel(b *testing.B) {
	addr, pool := vsServer(b)
	for _, name := range vsNames {
		b.Run(name, func(b *testing.B) {
			c := newVs(name, pool)
			defer c.close()
			u, msg := url(addr), query("bench.test")
			c.ask(context.Background(), u, msg)
			b.ReportAllocs()
			b.SetParallelism(64 / runtime.GOMAXPROCS(0))
			cpu0 := cpuTime()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := c.ask(context.Background(), u, msg); err != nil {
						b.Error(err)
						return
					}
				}
			})
			b.ReportMetric(float64((cpuTime()-cpu0).Microseconds())/float64(b.N), "cpu-µs/op")
		})
	}
}

// a fresh connection each time: handshake plus first answer, and what an idle connection holds on to
func BenchmarkVsFirstLookup(b *testing.B) {
	addr, pool := vsServer(b)
	for _, name := range vsNames {
		b.Run(name, func(b *testing.B) {
			u, msg := url(addr), query("first.test")
			var times []time.Duration
			var held []float64
			b.ReportAllocs()
			cpu0 := cpuTime()
			for b.Loop() {
				runtime.GC()
				var m0, m1 runtime.MemStats
				runtime.ReadMemStats(&m0)
				c := newVs(name, pool)
				start := time.Now()
				if _, err := c.ask(context.Background(), u, msg); err != nil {
					b.Fatal(err)
				}
				times = append(times, time.Since(start))
				runtime.GC()
				runtime.ReadMemStats(&m1)
				held = append(held, float64(m1.HeapInuse)-float64(m0.HeapInuse))
				c.close()
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			sort.Float64s(held)
			b.ReportMetric(float64(times[len(times)/2].Microseconds()), "µs-median")
			b.ReportMetric(held[len(held)/2]/1024, "KB-held")
			b.ReportMetric(float64((cpuTime()-cpu0).Microseconds())/float64(b.N), "cpu-µs/op")
		})
	}
}

var _ = strings.TrimSpace
