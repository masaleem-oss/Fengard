package internet

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestOutageIsRecordedOnceItsBack(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer up.Close()
	// a captive portal redirects instead of answering, which isnt the internet
	portal := httptest.NewServer(http.RedirectHandler("http://login.example/", http.StatusFound))
	defer portal.Close()
	var got []Outage
	m := &Monitor{Targets: []string{"http://127.0.0.1:1/", portal.URL}, OnOutage: func(o Outage) { got = append(got, o) }}
	m.online, m.since = true, time.Now()
	ctx := context.Background()
	m.testing = true
	for range 5 {
		m.probe(ctx)
	}
	m.testing = false
	if !m.Status().Online {
		t.Fatal("a speed test filling the line counted as an outage")
	}
	m.probe(ctx)
	m.probe(ctx)
	if !m.Status().Online {
		t.Fatal("two misses counted as an outage")
	}
	m.probe(ctx)
	if m.Status().Online {
		t.Fatal("three misses didnt count as an outage")
	}
	m.since = time.Now().Add(-3 * time.Minute)
	m.Targets = []string{up.URL}
	m.probe(ctx)
	if !m.Status().Online || len(got) != 1 || got[0].End.Sub(got[0].Start) < 3*time.Minute {
		t.Fatalf("outage not reported right: %+v", got)
	}
}

func TestSpeedTestMeasuresBothWays(t *testing.T) {
	downFor, upFor, warmup = 1500*time.Millisecond, 1500*time.Millisecond, 300*time.Millisecond
	chunk := make([]byte, 64<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/__down":
			n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
			for n > 0 {
				k := min(n, len(chunk))
				if _, err := w.Write(chunk[:k]); err != nil {
					return
				}
				n -= k
			}
		case "/__up":
			io.Copy(io.Discard, r.Body)
		}
	}))
	defer srv.Close()
	m := &Monitor{Server: srv.URL}
	r := m.Test(context.Background())
	if r.Error != "" || r.DownMbps <= 0 || r.UpMbps <= 0 {
		t.Fatalf("bad result %+v", r)
	}
	if m.Status().Latest == nil {
		t.Fatal("latest result not kept")
	}
}

// FENGARD_LIVE=1 go test -run Live ./internal/internet -v
func TestLiveCloudflare(t *testing.T) {
	if os.Getenv("FENGARD_LIVE") == "" {
		t.Skip("needs the internet")
	}
	r := (&Monitor{}).Test(context.Background())
	t.Logf("down %.0f Mbps up %.0f Mbps ping %.1f ms %s", r.DownMbps, r.UpMbps, r.PingMs, r.Error)
}

func TestSlowMeansWellUnderTheUsualSpeed(t *testing.T) {
	now := time.Now()
	h := []Result{{Time: now, DownMbps: 20}, {Time: now.Add(-24 * time.Hour), DownMbps: 90}, {Time: now.Add(-48 * time.Hour), DownMbps: 100}, {Time: now.Add(-72 * time.Hour), DownMbps: 95}}
	if typ, slow := typical(h, &h[0]); typ != 95 || !slow {
		t.Fatalf("typical %v slow %v", typ, slow)
	}
	h[0].DownMbps = 70
	if _, slow := typical(h, &h[0]); slow {
		t.Fatal("a bit under usual counted as slow")
	}
	if _, slow := typical(h[:2], &h[0]); slow {
		t.Fatal("called slow without enough history")
	}
}

func TestRefusedTestSaysSoInsteadOfZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("bytes") == "0" {
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	m := &Monitor{Server: srv.URL}
	r := m.Test(context.Background())
	if r.Error == "" || m.Status().Latest != nil {
		t.Fatalf("a refused test looked like a result: %+v", r)
	}
}
