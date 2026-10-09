package vpn

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/masaleem-oss/Fengard/internal/config"
)

// fg so it doesnt clash with the router own wireguard
const Iface = "fgwg0"

func NewKey() (priv, pub string, err error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", "", err
	}
	// clamp like wg does
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
	p, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(p), nil
}

func NewPresharedKey() (string, error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

func ValidKey(s string) bool {
	b, err := base64.StdEncoding.DecodeString(s)
	return err == nil && len(b) == 32
}

func Gateway(subnet string) (net.IP, *net.IPNet, error) {
	_, n, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, nil, err
	}
	ip := make(net.IP, len(n.IP))
	copy(ip, n.IP.To4())
	ip[len(ip)-1]++
	return ip, n, nil
}

func NextIP(v config.VPN) (string, error) {
	gw, n, err := Gateway(v.Subnet)
	if err != nil {
		return "", err
	}
	used := map[string]bool{gw.String(): true}
	for _, p := range v.Peers {
		used[p.IP] = true
	}
	ones, bits := n.Mask.Size()
	size := 1 << (bits - ones)
	base := n.IP.To4()
	for i := 2; i < size-1; i++ {
		ip := make(net.IP, 4)
		copy(ip, base)
		ip[3] += byte(i % 256)
		ip[2] += byte(i / 256)
		if !used[ip.String()] {
			return ip.String(), nil
		}
	}
	return "", errors.New("the VPN network is full")
}

// everything incl dns goes through the tunnel so filtering works away from home
func ClientConfig(v config.VPN, p config.VPNPeer, endpoint string) string {
	gw, _, _ := Gateway(v.Subnet)
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s/32\nDNS = %s\n\n", p.PrivateKey, p.IP, gw)
	fmt.Fprintf(&b, "[Peer]\nPublicKey = %s\n", v.PublicKey)
	if p.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", p.PresharedKey)
	}
	fmt.Fprintf(&b, "AllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = %s\nPersistentKeepalive = 25\n", endpoint)
	return b.String()
}

func ServerConfig(v config.VPN) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nListenPort = %d\n", v.PrivateKey, v.Port)
	for _, p := range v.Peers {
		if !p.Enabled {
			continue
		}
		fmt.Fprintf(&b, "\n[Peer]\n# %s\nPublicKey = %s\n", p.Name, p.PublicKey)
		if p.PresharedKey != "" {
			fmt.Fprintf(&b, "PresharedKey = %s\n", p.PresharedKey)
		}
		fmt.Fprintf(&b, "AllowedIPs = %s/32\n", p.IP)
	}
	return b.String()
}

func Endpoint(v config.VPN, detected string) string {
	host := v.Endpoint
	if host == "" {
		host = detected
	}
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, fmt.Sprint(v.Port))
}

type PeerStatus struct {
	PublicKey     string    `json:"-"`
	Endpoint      string    `json:"endpoint,omitempty"`
	LastHandshake time.Time `json:"lastHandshake,omitzero"`
	RxBytes       int64     `json:"rxBytes"`
	TxBytes       int64     `json:"txBytes"`
}

// wireguard rekeys at least every 3 min while traffic flows
func (s PeerStatus) Connected() bool {
	return !s.LastHandshake.IsZero() && time.Since(s.LastHandshake) < 3*time.Minute
}
