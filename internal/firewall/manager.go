package firewall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Backend interface {
	Name() string
	Render(p Params) (string, error)
	Apply(rendered string, p Params) error
	Check(rendered string) error
	Remove() error
}

type Manager struct {
	Enabled bool // false just renders for dev
	Netns   string
	Backend Backend // nil means nftables
	Inputs  func() (Params, error)

	mu      sync.Mutex
	lastSum [32]byte
	status  Status
}

type Status struct {
	Enabled bool      `json:"enabled"`
	Backend string    `json:"backend"`
	Applied time.Time `json:"applied,omitzero"`
	Error   string    `json:"error,omitempty"`
	Ruleset string    `json:"ruleset"`
}

func (m *Manager) backend() Backend {
	if m.Backend == nil {
		m.Backend = &NFTables{Netns: m.Netns}
	}
	return m.Backend
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.Enabled = m.Enabled
	s.Backend = m.backend().Name()
	return s
}

func (m *Manager) Sync() error {
	p, err := m.Inputs()
	if err == nil {
		var rs string
		if rs, err = m.backend().Render(p); err == nil {
			if h, ok := m.backend().(interface{ Healthy() bool }); ok && m.Enabled && !h.Healthy() {
				m.mu.Lock()
				m.lastSum = [32]byte{}
				m.mu.Unlock()
			}
			return m.apply(rs, p)
		}
	}
	m.mu.Lock()
	m.status.Error = err.Error()
	m.mu.Unlock()
	return err
}

func (m *Manager) apply(rs string, p Params) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sum := sha256.Sum256([]byte(rs))
	if sum == m.lastSum && m.status.Error == "" {
		return nil
	}
	m.status.Ruleset = rs
	if !m.Enabled {
		m.lastSum, m.status.Error = sum, ""
		return nil
	}
	if err := m.backend().Apply(rs, p); err != nil {
		m.status.Error = err.Error()
		return err
	}
	m.lastSum, m.status.Error, m.status.Applied = sum, "", time.Now()
	return nil
}

func (m *Manager) Check(rs string) error { return m.backend().Check(rs) }

func (m *Manager) Remove() error {
	if !m.Enabled {
		return nil
	}
	m.mu.Lock()
	m.lastSum = [32]byte{}
	m.mu.Unlock()
	return m.backend().Remove()
}

// periodic so schedules and pauses kick in on time
func (m *Manager) Run(ctx context.Context, every time.Duration, kick <-chan struct{}) {
	doSync := func() {
		if err := m.Sync(); err != nil {
			log.Printf("firewall: %v", err)
		}
	}
	doSync()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			doSync()
		case <-kick:
			doSync()
		}
	}
}

func Detect(netns string) Backend {
	if runtime.GOOS != "linux" {
		return &NFTables{Netns: netns}
	}
	if _, err := run(netns, "nft", nil, "list", "tables"); err == nil {
		return &NFTables{Netns: netns}
	}
	if _, err := run(netns, "iptables", nil, "-S"); err == nil {
		log.Printf("firewall: nft not usable, using iptables")
		return NewIPTables(netns)
	}
	return &NFTables{Netns: netns}
}

func run(netns, name string, stdin []byte, args ...string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("%s is only available on Linux", name)
	}
	if netns != "" {
		args = append([]string{"netns", "exec", netns, name}, args...)
		name = "ip"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

type NFTables struct{ Netns string }

func (n *NFTables) Name() string                    { return "nftables" }
func (n *NFTables) Render(p Params) (string, error) { return Render(p) }
func (n *NFTables) Apply(rs string, _ Params) error {
	_, err := run(n.Netns, "nft", []byte(rs), "-f", "-")
	return err
}
func (n *NFTables) Check(rs string) error {
	_, err := run(n.Netns, "nft", []byte(rs), "-c", "-f", "-")
	return err
}
func (n *NFTables) Healthy() bool {
	_, err := run(n.Netns, "nft", nil, "list", "table", "inet", Table)
	return err == nil
}

func (n *NFTables) Remove() error {
	_, err := run(n.Netns, "nft", []byte(fmt.Sprintf("table inet %s {}\ndelete table inet %s\n", Table, Table)), "-f", "-")
	return err
}

// only call this on the router
func Harden() {
	if runtime.GOOS != "linux" {
		return
	}
	settings := map[string]string{
		"net/ipv4/tcp_syncookies":                         "1",
		"net/ipv4/tcp_max_syn_backlog":                    "4096",
		"net/ipv4/icmp_echo_ignore_broadcasts":            "1", // no smurf amplification
		"net/ipv4/icmp_ignore_bogus_error_responses":      "1",
		"net/ipv4/conf/all/accept_redirects":              "0",
		"net/ipv4/conf/all/send_redirects":                "0",
		"net/ipv4/conf/all/accept_source_route":           "0",
		"net/ipv6/conf/all/accept_redirects":              "0",
		"net/ipv6/conf/all/accept_source_route":           "0",
		"net/netfilter/nf_conntrack_tcp_timeout_syn_recv": "30",
	}
	for k, v := range settings {
		if err := os.WriteFile("/proc/sys/"+k, []byte(v), 0o644); err != nil {
			log.Printf("harden: %s: %v", k, err)
		}
	}
}
