// package internet watches the connection for outages and runs speed tests
package internet

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

type Result struct {
	Time     time.Time `json:"time"`
	DownMbps float64   `json:"downMbps"`
	Via      string    `json:"via,omitempty"` // which server it ran against
	UpMbps   float64   `json:"upMbps"`
	PingMs   float64   `json:"pingMs"`
	JitterMs float64   `json:"jitterMs"`
	Error    string    `json:"error,omitempty"`
}

type Outage struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitzero"`
}

type Status struct {
	Online  bool      `json:"online"`
	Since   time.Time `json:"since"`
	Checked time.Time `json:"checked,omitzero"`
	Testing bool      `json:"testing"`
	Live    *Progress `json:"live,omitempty"`
	Latest  *Result   `json:"latest,omitempty"`
	History []Result  `json:"history"`
	Outages []Outage  `json:"outages"`
	Typical float64   `json:"typicalMbps,omitempty"`
	Slow    bool      `json:"slow"`
}

type Monitor struct {
	Speeds   *store.Log
	Outages  *store.Log
	Daily    func() bool // false skips the nightly test
	Location func() *time.Location
	OnOutage func(Outage)
	OnResult func(Result)

	// the pages phones fetch to see if theres internet, nobody blocks these
	// unlike 1.1.1.1 and 8.8.8.8 which networks that block doh also block
	Targets []string
	Server  string

	mu      sync.Mutex
	online  bool
	since   time.Time
	checked time.Time
	fails   int
	testing bool
	latest  *Result
	live    Progress
}

// Progress is what a running test has measured so far
type Progress struct {
	Phase    string  `json:"phase"` // ping, download or upload
	PingMs   float64 `json:"pingMs"`
	JitterMs float64 `json:"jitterMs"`
	DownMbps float64 `json:"downMbps"`
	UpMbps   float64 `json:"upMbps"`
}

func (m *Monitor) report(fn func(*Progress)) {
	m.mu.Lock()
	fn(&m.live)
	m.mu.Unlock()
}

const (
	probeEvery = 15 * time.Second
	// a few misses in a row so a busy line or one dropped packet isnt an outage
	failsForOutage = 3
	// shorter blips arent worth an alert
	minOutage = time.Minute
)

func (m *Monitor) Run(ctx context.Context) {
	if len(m.Targets) == 0 {
		m.Targets = []string{"http://connectivitycheck.gstatic.com/generate_204", "http://captive.apple.com/hotspot-detect.html", "http://www.msftconnecttest.com/connecttest.txt"}
	}
	m.mu.Lock()
	m.online, m.since = true, time.Now()
	m.mu.Unlock()
	m.loadLatest()
	// the nightly test lands at a random minute so every fengard doesnt hit cloudflare at once
	minute := rand.IntN(55)
	t := time.NewTicker(probeEvery)
	defer t.Stop()
	for {
		m.probe(ctx)
		if m.dueDaily(minute) {
			m.Test(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Monitor) probe(ctx context.Context) {
	// a speed test fills the line so checks would time out and look like an outage
	m.mu.Lock()
	testing := m.testing
	m.mu.Unlock()
	if testing {
		return
	}
	ok := false
	for _, u := range m.Targets {
		if reachable(ctx, u) {
			ok = true
			break
		}
	}
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	m.mu.Lock()
	m.checked = now
	var ended *Outage
	switch {
	case ok:
		m.fails = 0
		if !m.online {
			ended = &Outage{Start: m.since, End: now}
			m.online, m.since = true, now
		}
	default:
		m.fails++
		if m.online && m.fails >= failsForOutage {
			// it went down at the first miss not the second
			m.online, m.since = false, now.Add(-probeEvery*time.Duration(m.fails-1))
		}
	}
	m.mu.Unlock()
	if ended != nil && ended.End.Sub(ended.Start) >= minOutage {
		if m.Outages != nil {
			m.Outages.Append(store.Record{Time: ended.Start, Value: ended})
		}
		if m.OnOutage != nil {
			m.OnOutage(*ended)
		}
	}
}

func (m *Monitor) dueDaily(minute int) bool {
	if m.Daily != nil && !m.Daily() {
		return false
	}
	loc := time.Local
	if m.Location != nil {
		loc = m.Location()
	}
	now := time.Now().In(loc)
	if now.Hour() != 4 || now.Minute() < minute {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.online || m.testing {
		return false
	}
	if m.latest != nil {
		last := m.latest.Time.In(loc)
		if last.YearDay() == now.YearDay() && last.Year() == now.Year() {
			return false
		}
	}
	return true
}

func (m *Monitor) Status() Status {
	m.mu.Lock()
	st := Status{Online: m.online, Since: m.since, Checked: m.checked, Testing: m.testing, Latest: m.latest}
	if m.testing {
		live := m.live
		st.Live = &live
	}
	m.mu.Unlock()
	st.History, st.Outages = []Result{}, []Outage{}
	if m.Speeds != nil {
		m.Speeds.Scan(time.Time{}, 30, func(_ time.Time, v []byte) bool {
			var r Result
			if json.Unmarshal(v, &r) == nil {
				st.History = append(st.History, r)
			}
			return true
		})
	}
	if m.Outages != nil {
		m.Outages.Scan(time.Time{}, 20, func(_ time.Time, v []byte) bool {
			var o Outage
			if json.Unmarshal(v, &o) == nil {
				st.Outages = append(st.Outages, o)
			}
			return true
		})
	}
	if !st.Online {
		st.Outages = append([]Outage{{Start: st.Since}}, st.Outages...)
	}
	st.Typical, st.Slow = typical(st.History, st.Latest)
	return st
}

// typical is the middle download speed of earlier tests, slow means the latest is under half of it
func typical(history []Result, latest *Result) (float64, bool) {
	var past []float64
	for _, r := range history {
		if r.Error == "" && (latest == nil || !r.Time.Equal(latest.Time)) {
			past = append(past, r.DownMbps)
		}
	}
	if len(past) < 3 {
		return 0, false
	}
	sort.Float64s(past)
	mid := past[len(past)/2]
	return mid, latest != nil && latest.DownMbps < mid/2
}

func (m *Monitor) loadLatest() {
	if m.Speeds == nil {
		return
	}
	m.Speeds.Scan(time.Time{}, 10, func(_ time.Time, v []byte) bool {
		var r Result
		if json.Unmarshal(v, &r) == nil && r.Error == "" {
			m.mu.Lock()
			m.latest = &r
			m.mu.Unlock()
			return false
		}
		return true
	})
}

var (
	ErrBusy    = errors.New("a speed test is already running")
	errRefused = errors.New("speed test server refused")
)

// Start runs a test in the background so the dashboard can poll for it
func (m *Monitor) Start(run func(func(context.Context)) bool) error {
	m.mu.Lock()
	busy := m.testing
	m.mu.Unlock()
	if busy {
		return ErrBusy
	}
	if !run(func(ctx context.Context) { m.Test(ctx) }) {
		return errors.New("fengard is shutting down")
	}
	return nil
}

func (m *Monitor) Test(ctx context.Context) Result {
	m.mu.Lock()
	if m.testing {
		m.mu.Unlock()
		return Result{Error: ErrBusy.Error()}
	}
	m.testing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.testing = false
		m.mu.Unlock()
	}()
	r := m.measure(ctx)
	if m.Speeds != nil {
		m.Speeds.Append(store.Record{Time: r.Time, Value: r})
	}
	if r.Error == "" {
		m.mu.Lock()
		m.latest = &r
		m.mu.Unlock()
	}
	if m.OnResult != nil {
		m.OnResult(r)
	}
	return r
}

// caps a test at about 1.3 GB even on a very fast line
const (
	downCap, upCap = 1 << 30, 300 << 20
	streams        = 6
)

var (
	downFor, upFor = 10 * time.Second, 8 * time.Second
	// skips tcp slow start so the number is the real speed
	warmup = 1500 * time.Millisecond
)

func (m *Monitor) measure(ctx context.Context) Result {
	r := Result{Time: time.Now()}
	// http 1.1 so each stream gets its own tcp connection
	tr := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConnsPerHost: streams, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
	}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr}

	m.report(func(p *Progress) { *p = Progress{Phase: "ping"} })
	srv, err := m.pick(ctx, c)
	if err != nil {
		r.Error = "couldn't find a speed test server"
		return r
	}
	r.Via = srv.name
	var pings []float64
	for i := 0; i < 11; i++ {
		// spread out a little so the dashboard can show them come in
		if i > 1 {
			time.Sleep(150 * time.Millisecond)
		}
		t0 := time.Now()
		resp, err := c.Get(srv.ping)
		if err != nil {
			r.Error = "couldn't reach the speed test server"
			return r
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		// the first one includes the tls handshake
		if i > 0 {
			pings = append(pings, float64(time.Since(t0).Microseconds())/1000)
			m.report(func(p *Progress) { p.PingMs, p.JitterMs = latency(pings) })
		}
	}
	r.PingMs, r.JitterMs = latency(pings)
	m.report(func(p *Progress) { p.Phase = "download" })

	// the server says no after lots of tests from one address in a day
	var refused atomic.Bool
	var got atomic.Int64
	r.DownMbps = run(ctx, downFor, downCap, &got, func(v float64) { m.report(func(p *Progress) { p.DownMbps = v }) }, func(ctx context.Context) error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.down(), nil)
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			refused.Store(true)
			return errRefused
		}
		_, err = io.Copy(counter{&got}, resp.Body)
		return err
	})
	if refused.Load() && r.DownMbps < 1 {
		r.DownMbps, r.Error = 0, "the speed test server turned the test away, try again in a little while"
		return r
	}
	m.report(func(p *Progress) { p.Phase, p.DownMbps = "upload", r.DownMbps })
	payload := make([]byte, 4<<20)
	var sent atomic.Int64
	r.UpMbps = run(ctx, upFor, upCap, &sent, func(v float64) { m.report(func(p *Progress) { p.UpMbps = v }) }, func(ctx context.Context) error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.up(), &countingReader{r: bytes.NewReader(payload), n: &sent})
		req.ContentLength = int64(len(payload))
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	})
	if r.DownMbps == 0 && r.UpMbps == 0 && ctx.Err() == nil {
		r.Error = "the speed test didn't get any data through"
	}
	return r
}

// runs streams for a while and returns the speed after warmup in mbps
func run(ctx context.Context, d time.Duration, limit int64, n *atomic.Int64, live func(float64), one func(context.Context) error) float64 {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	var wg sync.WaitGroup
	for range streams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil && n.Load() < limit {
				if one(ctx) != nil && ctx.Err() == nil {
					time.Sleep(200 * time.Millisecond)
				}
			}
		}()
	}
	start := time.Now()
	base, baseAt := int64(-1), time.Time{}
	// the live number is the speed over about the last half second
	lastN, lastAt := int64(0), start
	for ctx.Err() == nil && n.Load() < limit {
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
		now, cur := time.Now(), n.Load()
		if base < 0 && now.Sub(start) >= warmup {
			base, baseAt = cur, now
		}
		if secs := now.Sub(lastAt).Seconds(); live != nil && secs >= 0.5 {
			live(float64(cur-lastN) * 8 / secs / 1e6)
			lastN, lastAt = cur, now
		}
	}
	end, endAt := n.Load(), time.Now()
	cancel()
	wg.Wait()
	// a line fast enough to hit the cap during warmup gets measured from the start
	if base < 0 || end == base {
		base, baseAt = 0, start
	}
	secs := endAt.Sub(baseAt).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(end-base) * 8 / secs / 1e6
}

type counter struct{ n *atomic.Int64 }

func (c counter) Write(p []byte) (int, error) { c.n.Add(int64(len(p))); return len(p), nil }

type countingReader struct {
	r *bytes.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n.Add(int64(k))
	return k, err
}

var checkClient = &http.Client{
	Timeout: 8 * time.Second,
	// a captive portal answers with a redirect, which isnt the internet
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext},
}

func reachable(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := checkClient.Do(req)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent
}

// latency is the quickest round trip and jitter how much they wobble from one to the next
func latency(ms []float64) (float64, float64) {
	if len(ms) == 0 {
		return 0, 0
	}
	low, wobble := ms[0], 0.0
	for i, v := range ms {
		low = min(low, v)
		if i > 0 {
			wobble += math.Abs(v - ms[i-1])
		}
	}
	if len(ms) > 1 {
		wobble /= float64(len(ms) - 1)
	}
	return low, wobble
}
