package main

import (
	"slices"
	"testing"
)

func TestVPNDNSPorts(t *testing.T) {
	env := []byte(`DNSMASQ='cfg01411c wgclient1 ovpnclient1 wgclient2'
ORIG_PORT_cfg01411c='unset'
ORIG_PORT_wgclient1='2153'
ORIG_PORT_ovpnclient1='4153'
ORIG_PORT_wgclient2='2253'
ORIG_PORT_lan2='53'
`)
	now := map[string]string{"cfg01411c": "0", "wgclient1": "0", "ovpnclient1": "4153", "wgclient2": "0", "lan2": "0"}
	got := vpnDNSPorts(env, func(s string) string { return now[s] })
	want := map[string]string{"wgclient1": "2153", "wgclient2": "2253"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for s, p := range want {
		if got[s] != p {
			t.Errorf("%s = %q, want %q", s, got[s], p)
		}
	}
}

func TestVPNServerNets(t *testing.T) {
	// trimmed from a gl.inet 4.x router with both servers set up
	show := []byte(`firewall.cfg01e63d=defaults
firewall.cfg02dc81=zone
firewall.cfg02dc81.name='lan'
firewall.cfg02dc81.network='lan'
firewall.cfg03dc81=zone
firewall.cfg03dc81.name='wan'
firewall.cfg03dc81.network='wan' 'wan6' 'secondwan'
firewall.cfg03dc81.masq='1'
firewall.wgserver=zone
firewall.wgserver.name='wgserver'
firewall.wgserver.network='wgserver'
firewall.wgserver.masq='1'
firewall.ovpnserver=zone
firewall.ovpnserver.name='ovpnserver'
firewall.ovpnserver.network='ovpnserver'
firewall.ovpnserver.masq='1'
firewall.ovpnserver.enabled='0'
firewall.wgclient=zone
firewall.wgclient.name='wgclient'
firewall.wgclient.network='wgclient1' 'wgclient2'
firewall.wgclient.masq='1'
firewall.fake=rule
firewall.fake.name='wgserver'
firewall.fake.network='lan'
`)
	got := vpnServerNets(show)
	if want := []string{"ovpnserver", "wgserver"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
