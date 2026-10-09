package vpn

import (
	"strings"
	"testing"

	"github.com/masaleem-oss/Fengard/internal/config"
)

func TestKeysAndConfigs(t *testing.T) {
	priv, pub, err := NewKey()
	if err != nil || !ValidKey(priv) || !ValidKey(pub) || priv == pub {
		t.Fatalf("bad keypair: %v %q %q", err, priv, pub)
	}
	psk, _ := NewPresharedKey()
	v := config.VPN{Enabled: true, Port: 51820, Subnet: "10.66.0.0/24", PrivateKey: priv, PublicKey: pub}
	ip, err := NextIP(v)
	if err != nil || ip != "10.66.0.2" {
		t.Fatalf("first address: %q %v", ip, err)
	}
	cpriv, cpub, _ := NewKey()
	peer := config.VPNPeer{ID: "phone", Name: "Phone", PublicKey: cpub, PrivateKey: cpriv, PresharedKey: psk, IP: ip, Enabled: true}
	v.Peers = append(v.Peers, peer)
	if ip, _ := NextIP(v); ip != "10.66.0.3" {
		t.Fatalf("second address: %q", ip)
	}

	cc := ClientConfig(v, peer, Endpoint(v, "203.0.113.9"))
	for _, want := range []string{"PrivateKey = " + cpriv, "Address = 10.66.0.2/32", "DNS = 10.66.0.1", "PublicKey = " + pub,
		"PresharedKey = " + psk, "AllowedIPs = 0.0.0.0/0, ::/0", "Endpoint = 203.0.113.9:51820", "PersistentKeepalive = 25"} {
		if !strings.Contains(cc, want) {
			t.Errorf("client config missing %q:\n%s", want, cc)
		}
	}
	sc := ServerConfig(v)
	for _, want := range []string{"PrivateKey = " + priv, "ListenPort = 51820", "PublicKey = " + cpub, "AllowedIPs = 10.66.0.2/32"} {
		if !strings.Contains(sc, want) {
			t.Errorf("server config missing %q:\n%s", want, sc)
		}
	}
	if strings.Contains(sc, cpriv) {
		t.Error("server config leaks the client's private key")
	}
	v.Endpoint = "home.example.net:4000"
	if got := Endpoint(v, "1.2.3.4"); got != "home.example.net:4000" {
		t.Errorf("endpoint override ignored: %q", got)
	}
	v.Endpoint = "home.example.net"
	if got := Endpoint(v, ""); got != "home.example.net:51820" {
		t.Errorf("endpoint port not appended: %q", got)
	}
	if peer.Identity() != "vpn:phone" {
		t.Error("standalone peer identity")
	}
	peer.Device = "aa:bb:cc:dd:ee:ff"
	if peer.Identity() != "aa:bb:cc:dd:ee:ff" {
		t.Error("linked peer identity")
	}
}

func TestSubnetFull(t *testing.T) {
	v := config.VPN{Subnet: "10.66.0.0/29"}
	for i := 0; i < 5; i++ {
		ip, err := NextIP(v)
		if err != nil {
			t.Fatalf("peer %d: %v", i, err)
		}
		v.Peers = append(v.Peers, config.VPNPeer{IP: ip})
	}
	if _, err := NextIP(v); err == nil {
		t.Fatal("/29 should hold 5 peers, not 6")
	}
}
