package main

import "testing"

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
