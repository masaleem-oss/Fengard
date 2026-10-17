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
