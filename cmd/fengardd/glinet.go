package main

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const installEnv = "/etc/fengard/install.env"

var aghSwitch = regexp.MustCompile(`(?m)^(adguardhome\.[^.=]+\.enabled)='?1'?$`)

// older installers stopped gl.inet's adguard home but left its switch in the gl.inet settings on.
// gl.inet can then point dns at adguard again, like when a vpn client comes up, and nothing answers.
// this turns the switch off on those routers once and notes it so a reinstall knows
func glinetAdGuardOff() {
	env, err := os.ReadFile(installEnv)
	if err != nil || !bytes.Contains(env, []byte("AGH_INIT='/")) || bytes.Contains(env, []byte("AGH_UCI=")) {
		return
	}
	out, err := exec.Command("uci", "-q", "show", "adguardhome").Output()
	if err != nil {
		return
	}
	var opts []string
	for _, m := range aghSwitch.FindAllStringSubmatch(string(out), -1) {
		opts = append(opts, m[1])
	}
	for _, o := range opts {
		if err := exec.Command("uci", "set", o+"=0").Run(); err != nil {
			log.Printf("adguard home: %s: %v", o, err)
			return
		}
	}
	if len(opts) > 0 {
		if err := exec.Command("uci", "commit", "adguardhome").Run(); err != nil {
			log.Printf("adguard home: %v", err)
			return
		}
		log.Printf("turned off GL.iNet's AdGuard Home switch too, it was still on after AdGuard Home was stopped")
	}
	f, err := os.OpenFile(installEnv, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return
	}
	f.WriteString("AGH_UCI='" + strings.Join(opts, " ") + "'\n")
	f.Close()
}

var origPort = regexp.MustCompile(`(?m)^ORIG_PORT_([A-Za-z0-9_]+)='([0-9]+)'$`)

// sections an older installer set to port 0 that had their own port before, like gl.inet's
// dnsmasq for each vpn client on 2153. port 53 ones are the main dns and stay off
func vpnDNSPorts(env []byte, port func(section string) string) map[string]string {
	fix := map[string]string{}
	for _, m := range origPort.FindAllSubmatch(env, -1) {
		s, p := string(m[1]), string(m[2])
		if p == "53" || p == "0" || port(s) != "0" {
			continue
		}
		fix[s] = p
	}
	return fix
}

// installers before 1.3.1.2 turned off the dns gl.inet runs for each vpn client too, and updating
// from the dashboard never put it back. gl.inet sends devices to it when the vpn is up, so nothing answered
func glinetVPNDNS() {
	env, err := os.ReadFile(installEnv)
	if err != nil {
		return
	}
	fix := vpnDNSPorts(env, func(s string) string {
		out, _ := exec.Command("uci", "-q", "get", "dhcp."+s+".port").Output()
		return strings.TrimSpace(string(out))
	})
	if len(fix) == 0 {
		return
	}
	for s, p := range fix {
		if err := exec.Command("uci", "set", "dhcp."+s+".port="+p).Run(); err != nil {
			log.Printf("vpn client dns: %s: %v", s, err)
			return
		}
	}
	if err := exec.Command("uci", "commit", "dhcp").Run(); err != nil {
		log.Printf("vpn client dns: %v", err)
		return
	}
	exec.Command("/etc/init.d/dnsmasq", "restart").Run()
	log.Printf("turned GL.iNet's DNS for VPN clients back on, an older install had turned it off")
}
