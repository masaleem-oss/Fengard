package notify

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/config"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMalformedRequestsDoNotPanicOrLeakTokens(t *testing.T) {
	n := &Notifier{BoxName: "test", client: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("delivery failed") })}}
	for _, ch := range []config.Channel{{Type: "telegram", Token: "private%token", ChatID: "1"}, {Type: "webhook", URL: "http://%zz"}, {Type: "ntfy", URL: "http://%zz"}, {Type: "telegram", Token: "private-token", ChatID: "1"}} {
		err := n.Send(ch, alerts.Alert{Title: "test", Time: time.Now()})
		if err == nil {
			t.Fatal("malformed or failed request succeeded")
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("token leaked in error")
		}
	}
}

func TestWorkerContinuesAfterMalformedChannel(t *testing.T) {
	var mu sync.Mutex
	var requests int
	n := &Notifier{BoxName: "test", queue: make(chan alerts.Alert, 1), status: map[string]Status{}, client: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}}
	n.Configure("test", []config.Channel{{ID: "bad", Type: "telegram", Token: "%", ChatID: "1", Enabled: true, MinLevel: "info"}, {ID: "good", Type: "webhook", URL: "https://example.com", Enabled: true, MinLevel: "info"}})
	n.Notify(alerts.Alert{Title: "test", Severity: alerts.Info, Time: time.Now()})
	close(n.queue)
	n.worker()
	if requests != 1 || n.Status()["bad"].Failed != 1 || n.Status()["good"].Sent != 1 {
		t.Fatal("worker did not continue after channel error")
	}
}

func TestConfigureAndSendUseSafeNameSnapshot(t *testing.T) {
	n := &Notifier{BoxName: "test", client: &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			n.Configure("changed", nil)
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			if err := n.Send(config.Channel{Type: "webhook", URL: "https://example.com"}, alerts.Alert{Time: time.Now()}); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
}
