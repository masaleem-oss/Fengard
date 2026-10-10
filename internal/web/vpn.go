package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/masaleem-oss/Fengard/internal/config"
	"github.com/masaleem-oss/Fengard/internal/vpn"
)

// vpn

type vpnPeerView struct {
	config.VPNPeer
	Identity  string         `json:"identity"`
	Status    vpn.PeerStatus `json:"status"`
	Connected bool           `json:"connected"`
}

func (s *Server) vpnInfo(w http.ResponseWriter, r *http.Request) {
	v := s.Config.Get().VPN
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	reach := s.VPN.Reachability(ctx)
	status := s.VPN.Status()
	peers := make([]vpnPeerView, 0, len(v.Peers))
	for _, p := range v.Peers {
		p.PrivateKey, p.PresharedKey = "", "" // keys only leave via the profile download
		st := status[p.PublicKey]
		peers = append(peers, vpnPeerView{VPNPeer: p, Identity: p.Identity(), Status: st, Connected: st.Connected()})
	}
	writeJSON(w, map[string]any{
		"enabled": v.Enabled, "port": v.Port, "subnet": v.Subnet, "endpoint": v.Endpoint,
		"publicKey": v.PublicKey, "supported": vpn.Supported(), "running": s.VPN.Running(),
		"reach": reach, "effectiveEndpoint": vpn.Endpoint(v, reach.PublicIP),
		"peers": peers, "tailscale": vpn.TailscaleStatus(ctx),
		// with an exit node tailscale resolves dns itself and skips fengard unless the tailnet dns points here
		// lookups from tailscale addrs mean it does
		"tailscaleDns": s.tunnelLookupsSince(netip.MustParsePrefix(vpn.TailscaleCGNAT), 15*time.Minute),
	})
}

func (s *Server) tunnelLookupsSince(p netip.Prefix, within time.Duration) bool {
	cutoff := time.Now().Add(-within)
	for _, e := range s.Log.Recent(3000) {
		if e.Time.Before(cutoff) {
			continue
		}
		if ip, err := netip.ParseAddr(e.Client); err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) vpnSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled  bool   `json:"enabled"`
		Port     int    `json:"port"`
		Endpoint string `json:"endpoint"`
	}
	if !decode(w, r, &body) {
		return
	}
	summary := "Turned the VPN off"
	if body.Enabled {
		summary = "Turned the VPN on"
	}
	if s.update(w, r, summary, func(c *config.Config) error {
		v := &c.VPN
		if v.PrivateKey == "" {
			priv, pub, err := vpn.NewKey()
			if err != nil {
				return err
			}
			v.PrivateKey, v.PublicKey = priv, pub
		}
		v.Enabled, v.Port, v.Endpoint = body.Enabled, body.Port, body.Endpoint
		return nil
	}) {
		s.audit(r, summary, "")
		s.vpnInfo(w, r)
	}
}

func (s *Server) vpnAddPeer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Device string `json:"device"`
		Group  string `json:"group"`
	}
	if !decode(w, r, &body) {
		return
	}
	id := make([]byte, 4)
	rand.Read(id)
	priv, pub, err := vpn.NewKey()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	psk, _ := vpn.NewPresharedKey()
	peer := config.VPNPeer{
		ID: hex.EncodeToString(id), Name: strings.TrimSpace(body.Name), PublicKey: pub, PrivateKey: priv,
		PresharedKey: psk, Device: body.Device, Group: body.Group, Created: time.Now(), Enabled: true,
	}
	if s.update(w, r, "Added VPN device "+peer.Name, func(c *config.Config) error {
		if c.VPN.PrivateKey == "" {
			sp, spub, err := vpn.NewKey()
			if err != nil {
				return err
			}
			c.VPN.PrivateKey, c.VPN.PublicKey = sp, spub
		}
		ip, err := vpn.NextIP(c.VPN)
		if err != nil {
			return err
		}
		peer.IP = ip
		c.VPN.Peers = append(c.VPN.Peers, peer)
		// standalone peer shows up as its own device like any phone on the wifi
		if peer.Device == "" && c.Device(peer.Identity()) == nil {
			group := peer.Group
			if group == "" {
				group = c.Settings.DefaultGroup
			}
			c.Devices = append(c.Devices, config.Device{MAC: peer.Identity(), Name: peer.Name, Group: group, Approved: true, FirstSeen: time.Now()})
		}
		return nil
	}) {
		s.audit(r, "Added VPN device "+peer.Name, "")
		s.vpnPeerConfig(w, withPeer(r, peer.ID))
	}
}

func (s *Server) vpnUpdatePeer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name    *string `json:"name"`
		Device  *string `json:"device"`
		Group   *string `json:"group"`
		Enabled *bool   `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	var name string
	if s.update(w, r, "Changed VPN device", func(c *config.Config) error {
		for i := range c.VPN.Peers {
			p := &c.VPN.Peers[i]
			if p.ID != id {
				continue
			}
			if body.Name != nil {
				p.Name = *body.Name
			}
			if body.Device != nil {
				p.Device = *body.Device
			}
			if body.Group != nil {
				p.Group = *body.Group
			}
			if body.Enabled != nil {
				p.Enabled = *body.Enabled
			}
			name = p.Name
			return nil
		}
		return fmt.Errorf("no VPN device %q", id)
	}) {
		s.audit(r, "Changed VPN device "+name, "")
		s.vpnInfo(w, r)
	}
}

func (s *Server) vpnDeletePeer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var name string
	if s.update(w, r, "Removed VPN device", func(c *config.Config) error {
		for i, p := range c.VPN.Peers {
			if p.ID == id {
				name = p.Name
				c.VPN.Peers = append(c.VPN.Peers[:i], c.VPN.Peers[i+1:]...)
				for j, d := range c.Devices {
					if d.MAC == p.Identity() {
						c.Devices = append(c.Devices[:j], c.Devices[j+1:]...)
						break
					}
				}
				return nil
			}
		}
		return fmt.Errorf("no VPN device %q", id)
	}) {
		s.audit(r, "Removed VPN device "+name, "")
		writeJSON(w, map[string]bool{"ok": true})
	}
}

// json for the qr code or a .conf download
func (s *Server) vpnPeerConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v := s.Config.Get().VPN
	for _, p := range v.Peers {
		if p.ID != id {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		reach := s.VPN.Reachability(ctx)
		endpoint := vpn.Endpoint(v, reach.PublicIP)
		s.audit(r, "Exported VPN credentials", p.ID)
		conf := vpn.ClientConfig(v, p, endpoint)
		if r.URL.Query().Get("download") != "" {
			name := strings.Map(func(c rune) rune {
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
					return c
				}
				return '-'
			}, p.Name)
			w.Header().Set("Content-Type", "application/x-wireguard-profile")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Fengard-%s.conf"`, strings.Trim(name, "-")))
			w.Write([]byte(conf))
			return
		}
		writeJSON(w, map[string]any{"id": p.ID, "name": p.Name, "ip": p.IP, "endpoint": endpoint, "config": conf,
			"ready": endpoint != "", "reach": reach})
		return
	}
	httpError(w, http.StatusNotFound, "no such VPN device")
}

// sets the id path value so a handler can be reused after an add
func withPeer(r *http.Request, id string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.SetPathValue("id", id)
	return r2
}
