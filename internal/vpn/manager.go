package vpn

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/masaleem-oss/Fengard/internal/config"
)

type Manager struct {
	DataDir string
	WAN     []string // for the double nat check

	mu      sync.Mutex
	applied string
	up      bool
	lastErr string

	pubMu     sync.Mutex
	publicIP  string
	publicAt  time.Time
	publicErr string
	detecting bool
}

func Supported() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("wg")
	return err == nil
}

func (m *Manager) Apply(v config.VPN) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !Supported() {
		return nil
	}
	if !v.Enabled || v.PrivateKey == "" {
		if m.up {
			run("ip", "link", "del", Iface)
			m.up, m.applied = false, ""
		}
		return nil
	}
	gw, n, err := Gateway(v.Subnet)
	if err != nil {
		return err
	}
	conf := ServerConfig(v)
	ones, _ := n.Mask.Size()
	addr := fmt.Sprintf("%s/%d", gw, ones)
	sig := conf + addr
	if m.up && sig == m.applied {
		return nil
	}
	exec.Command("modprobe", "wireguard").Run()
	if _, err := run("ip", "link", "show", Iface); err != nil {
		if out, err := run("ip", "link", "add", Iface, "type", "wireguard"); err != nil {
			m.lastErr = out
			return fmt.Errorf("create %s: %s", Iface, strings.TrimSpace(out))
		}
	}
	path := filepath.Join(m.DataDir, "wg-server.conf")
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		return err
	}
	if out, err := run("wg", "syncconf", Iface, path); err != nil {
		// older wg has no syncconf so setconf is fine
		if out2, err2 := run("wg", "setconf", Iface, path); err2 != nil {
			m.lastErr = out + out2
			return fmt.Errorf("wg: %s", strings.TrimSpace(out2))
		}
	}
	run("ip", "addr", "flush", "dev", Iface)
	if out, err := run("ip", "addr", "add", addr, "dev", Iface); err != nil && !strings.Contains(out, "exists") {
		return fmt.Errorf("address: %s", strings.TrimSpace(out))
	}
	run("ip", "link", "set", "mtu", "1420", "up", "dev", Iface)
	m.up, m.applied, m.lastErr = true, sig, ""
	return nil
}

func (m *Manager) Down() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.up {
		run("ip", "link", "del", Iface)
		m.up = false
	}
}

func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.up
}

func (m *Manager) Status() map[string]PeerStatus {
	out := map[string]PeerStatus{}
	if !m.Running() {
		return out
	}
	dump, err := run("wg", "show", Iface, "dump")
	if err != nil {
		return out
	}
	for i, line := range strings.Split(dump, "\n") {
		f := strings.Split(line, "\t")
		if i == 0 || len(f) < 8 { // first line is the iface
			continue
		}
		st := PeerStatus{PublicKey: f[0]}
		if f[2] != "(none)" {
			st.Endpoint = f[2]
		}
		if ts, _ := strconv.ParseInt(f[4], 10, 64); ts > 0 {
			st.LastHandshake = time.Unix(ts, 0)
		}
		st.RxBytes, _ = strconv.ParseInt(f[5], 10, 64)
		st.TxBytes, _ = strconv.ParseInt(f[6], 10, 64)
		out[f[0]] = st
	}
	return out
}

type Reachability struct {
	WANIP     string `json:"wanIp"`
	PublicIP  string `json:"publicIp"`
	DoubleNAT bool   `json:"doubleNat"` // wan ip is private so another router is in front
	Detecting bool   `json:"detecting"`
	Error     string `json:"error,omitempty"`
}

// public lookup can take seconds so it runs in the background
func (m *Manager) Reachability(ctx context.Context) Reachability {
	r := Reachability{WANIP: wanAddress(m.WAN)}
	if ip := net.ParseIP(r.WANIP); ip != nil && ip.IsPrivate() {
		r.DoubleNAT = true
	}
	m.pubMu.Lock()
	defer m.pubMu.Unlock()
	if !m.detecting && time.Since(m.publicAt) > 10*time.Minute {
		m.detecting = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ip, err := publicIP(ctx)
			m.pubMu.Lock()
			m.publicIP, m.publicAt, m.publicErr, m.detecting = ip, time.Now(), "", false
			if err != nil {
				m.publicErr = err.Error()
			}
			m.pubMu.Unlock()
		}()
	}
	r.PublicIP, r.Error, r.Detecting = m.publicIP, m.publicErr, m.detecting
	return r
}

func (m *Manager) Detect() { m.Reachability(context.Background()) }

func wanAddress(ifaces []string) string {
	for _, name := range ifaces {
		ifc, err := net.InterfaceByName(name)
		if err != nil {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				return n.IP.String()
			}
		}
	}
	return ""
}

// opendns answers this name with your own ip so no http service needed
func publicIP(ctx context.Context) (string, error) {
	msg := new(dns.Msg)
	msg.SetQuestion("myip.opendns.com.", dns.TypeA)
	c := &dns.Client{Timeout: 4 * time.Second}
	for _, server := range []string{"208.67.222.222:53", "208.67.220.220:53"} {
		resp, _, err := c.ExchangeContext(ctx, msg, server)
		if err != nil {
			continue
		}
		for _, rr := range resp.Answer {
			if a, ok := rr.(*dns.A); ok {
				return a.A.String(), nil
			}
		}
	}
	return "", errors.New("could not detect the public address (is the gateway online?)")
}

func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		log.Printf("vpn: %s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), err
}
