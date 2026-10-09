// alerts queue and drop when full so callers never block
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/masaleem-oss/Fengard/internal/alerts"
	"github.com/masaleem-oss/Fengard/internal/config"
)

type Notifier struct {
	BoxName  string
	client   *http.Client
	queue    chan alerts.Alert
	channels atomic.Pointer[[]config.Channel]

	mu     sync.Mutex
	status map[string]Status // by channel id
}

type Status struct {
	LastSent  time.Time `json:"lastSent,omitzero"`
	LastError string    `json:"lastError,omitempty"`
	Sent      int       `json:"sent"`
	Failed    int       `json:"failed"`
}

var levels = map[string]int{"info": 0, "warning": 1, "critical": 2}

func New(boxName string) *Notifier {
	n := &Notifier{
		BoxName: boxName,
		client:  &http.Client{Timeout: 10 * time.Second},
		queue:   make(chan alerts.Alert, 256),
		status:  map[string]Status{},
	}
	n.channels.Store(&[]config.Channel{})
	go n.worker()
	return n
}

func (n *Notifier) Configure(boxName string, chans []config.Channel) {
	n.BoxName = boxName
	cp := append([]config.Channel{}, chans...)
	n.channels.Store(&cp)
}

func (n *Notifier) Notify(a alerts.Alert) {
	select {
	case n.queue <- a:
	default:
		log.Printf("notify: queue full, dropping %q", a.Title)
	}
}

func (n *Notifier) Status() map[string]Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make(map[string]Status, len(n.status))
	for k, v := range n.status {
		out[k] = v
	}
	return out
}

func (n *Notifier) worker() {
	for a := range n.queue {
		for _, ch := range *n.channels.Load() {
			if !ch.Enabled || levels[string(a.Severity)] < levels[ch.MinLevel] {
				continue
			}
			n.record(ch.ID, n.Send(ch, a))
		}
	}
}

func (n *Notifier) record(id string, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.status[id]
	if err != nil {
		s.Failed++
		s.LastError = err.Error()
		log.Printf("notify %s: %v", id, err)
	} else {
		s.Sent++
		s.LastSent = time.Now()
		s.LastError = ""
	}
	n.status[id] = s
}

// sync send used by the worker and the test button
func (n *Notifier) Send(ch config.Channel, a alerts.Alert) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	title := fmt.Sprintf("[%s] %s", n.BoxName, a.Title)
	var req *http.Request
	var err error
	switch ch.Type {
	case "webhook":
		body, _ := json.Marshal(map[string]any{
			"source": "fengard", "network": n.BoxName, "time": a.Time, "kind": a.Kind,
			"severity": a.Severity, "title": a.Title, "detail": a.Detail, "mac": a.MAC, "domain": a.Domain,
		})
		req, err = http.NewRequestWithContext(ctx, "POST", ch.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	case "discord":
		color := map[alerts.Severity]int{alerts.Info: 0x8b6cff, alerts.Warning: 0xf0b429, alerts.Critical: 0xf2555a}[a.Severity]
		body, _ := json.Marshal(map[string]any{
			"username": "Fengard",
			"embeds": []map[string]any{{"title": a.Title, "description": a.Detail, "color": color,
				"footer": map[string]string{"text": n.BoxName + " · " + string(a.Severity)}, "timestamp": a.Time.UTC().Format(time.RFC3339)}},
		})
		req, err = http.NewRequestWithContext(ctx, "POST", ch.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	case "slack":
		body, _ := json.Marshal(map[string]any{"text": fmt.Sprintf("*%s*\n%s", title, a.Detail)})
		req, err = http.NewRequestWithContext(ctx, "POST", ch.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	case "ntfy":
		req, err = http.NewRequestWithContext(ctx, "POST", ch.URL, strings.NewReader(a.Detail))
		req.Header.Set("Title", title)
		req.Header.Set("Priority", map[alerts.Severity]string{alerts.Info: "default", alerts.Warning: "high", alerts.Critical: "urgent"}[a.Severity])
		req.Header.Set("Tags", map[alerts.Severity]string{alerts.Info: "information_source", alerts.Warning: "warning", alerts.Critical: "rotating_light"}[a.Severity])
	case "telegram":
		form := url.Values{"chat_id": {ch.ChatID}, "text": {title + "\n" + a.Detail}}
		req, err = http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+ch.Token+"/sendMessage", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	default:
		return fmt.Errorf("unknown channel type %q", ch.Type)
	}
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Fengard/1.0")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, ch.Type)
	}
	return nil
}
