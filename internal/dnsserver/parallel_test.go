package dnsserver

import (
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/masaleem-oss/Fengard/internal/config"
)

func ask(t *testing.T, s *Server) (*dns.Msg, time.Duration) {
	t.Helper()
	q := new(dns.Msg)
	q.SetQuestion("example.org.", dns.TypeA)
	start := time.Now()
	r, err := s.exchange(q, *s.upstreams.Load())
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	return r, time.Since(start)
}

func withUpstreams(parallel bool, ups ...*fakeUpstream) func(*config.Config) {
	return func(c *config.Config) {
		c.Settings.Upstreams = nil
		for _, u := range ups {
			c.Settings.Upstreams = append(c.Settings.Upstreams, u.addr)
		}
		c.Settings.Parallel = parallel
	}
}

// a slow first server holds every lookup up in order, all at once the quick one answers
func TestParallelSlowServer(t *testing.T) {
	slow, quick := startUpstream(t), startUpstream(t)
	slow.delay.Store(int64(800 * time.Millisecond))

	s, _ := startServer(t, quick, withUpstreams(false, slow, quick))
	if _, took := ask(t, s); took < 700*time.Millisecond {
		t.Fatalf("in order should wait for the slow one, took %v", took)
	}
	s, _ = startServer(t, quick, withUpstreams(true, slow, quick))
	if r, took := ask(t, s); took > 300*time.Millisecond || len(r.Answer) != 1 {
		t.Fatalf("all at once took %v: %v", took, r)
	}
}

// an unreachable server costs nothing when the others are asked at the same time
func TestParallelDeadServer(t *testing.T) {
	dead, quick := startUpstream(t), startUpstream(t)
	dead.down.Store(true)
	s, _ := startServer(t, quick, withUpstreams(true, dead, quick))
	if r, took := ask(t, s); took > 300*time.Millisecond || len(r.Answer) != 1 {
		t.Fatalf("took %v: %v", took, r)
	}
}

// a quick SERVFAIL doesn't beat a real answer that comes a bit later, and is used if nothing better comes
func TestParallelServfailLoses(t *testing.T) {
	broken, good := startUpstream(t), startUpstream(t)
	broken.fail.Store(true)
	good.delay.Store(int64(100 * time.Millisecond))
	s, _ := startServer(t, good, withUpstreams(true, broken, good))
	if r, _ := ask(t, s); r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 {
		t.Fatalf("got %v", r)
	}
	good.down.Store(true)
	q := new(dns.Msg)
	q.SetQuestion("other.example.", dns.TypeA)
	r, err := s.exchange(q, *s.upstreams.Load())
	if err != nil || r.Rcode != dns.RcodeServerFailure {
		t.Fatalf("with only the broken one answering: %v %v", r, err)
	}
}
