package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/masaleem-oss/Fengard/internal/persist"
)

const initScript = "/etc/init.d/fengard"

var wanNetsLine = regexp.MustCompile(`(?m)^WAN_NETS='([^']*)'$`)

// wanNets is the wan side networks the installer found, out of install.env
func wanNets(env []byte) []string {
	m := wanNetsLine.FindSubmatch(env)
	if m == nil {
		return nil
	}
	return strings.Fields(string(m[1]))
}

// wanDevices picks the devices these networks are on now out of ubus call network.interface dump.
// a network that's down still names the device it's set to use, one like a repeater link that
// has none yet shows up once it connects
func wanDevices(dump []byte, nets []string) []string {
	var v struct {
		Interface []struct {
			Name string `json:"interface"`
			L3   string `json:"l3_device"`
			Dev  string `json:"device"`
		} `json:"interface"`
	}
	if json.Unmarshal(dump, &v) != nil {
		return nil
	}
	var out []string
	for _, i := range v.Interface {
		if !slices.Contains(nets, i.Name) {
			continue
		}
		d := i.L3
		if d == "" {
			d = i.Dev
		}
		// @wan style aliases aren't devices
		if d != "" && !strings.HasPrefix(d, "@") && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// wanWatch keeps the wan interfaces current while fengard runs. the service script used to
// restart fengard whenever a wan network went up or down so it would be started with the new
// ones, which took dns away for a moment every time a repeater's uplink blinked
type wanWatch struct {
	flag []string // what the service script passed at start
	nets []string // nil off a router, then the flag is all there is
	dump func() ([]byte, error)

	mu   sync.Mutex
	seen []string // found since the start, on top of the flag
}

func newWANWatch(flag []string, router bool) *wanWatch {
	w := &wanWatch{flag: flag}
	if !router {
		return w
	}
	if env, err := os.ReadFile(installEnv); err == nil {
		w.nets = wanNets(env)
	}
	w.dump = func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "ubus", "call", "network.interface", "dump").Output()
	}
	return w
}

// Devices is the interfaces from the start plus every one a wan network has been on since. one
// that went away stays listed, so the firewall still treats it as the internet side the moment
// it's back, and a rule for an interface that isn't there matches nothing
func (w *wanWatch) Devices() []string {
	if len(w.nets) == 0 || w.dump == nil {
		return slices.Clone(w.flag)
	}
	var now []string
	if dump, err := w.dump(); err == nil {
		now = wanDevices(dump, w.nets)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, d := range now {
		if !slices.Contains(w.flag, d) && !slices.Contains(w.seen, d) {
			w.seen = append(w.seen, d)
			log.Printf("wan: %s is an internet link too, it came up after Fengard started", d)
		}
	}
	return append(slices.Clone(w.flag), w.seen...)
}

const (
	wanTriggers   = "for n in $LAN_NET $EXTRA_NETS $WAN_NETS; do\n\t\tprocd_add_reload_interface_trigger"
	noWANTriggers = "for n in $LAN_NET $EXTRA_NETS; do\n\t\tprocd_add_reload_interface_trigger"
)

// dropWANTriggers takes the wan networks out of the service script's reload triggers
func dropWANTriggers(script []byte) ([]byte, bool) {
	if !bytes.Contains(script, []byte(wanTriggers)) {
		return script, false
	}
	return bytes.Replace(script, []byte(wanTriggers), []byte(noWANTriggers), 1), true
}

// updating from the dashboard only swaps the program, so a service script from an older installer
// still restarts fengard on every wan change. this rewrites that one line, then has procd read
// the script again so the old triggers go now and not at the next reboot. the reload leaves
// fengard running unless its command line changed meanwhile, same as any other reload
func fixInitTriggers(ctx context.Context) {
	script, err := os.ReadFile(initScript)
	if err != nil {
		return
	}
	fixed, changed := dropWANTriggers(script)
	if !changed {
		return
	}
	if err := persist.WriteFile(initScript, fixed, 0o755); err != nil {
		log.Printf("service script: %v", err)
		return
	}
	log.Printf("service script: Fengard no longer restarts when the internet link goes down or comes back")
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	if out, err := exec.Command(initScript, "reload").CombinedOutput(); err != nil {
		log.Printf("service script: reload: %v %s", err, bytes.TrimSpace(out))
	}
}
