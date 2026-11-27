package main

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"testing"
)

// trimmed from a gl-mt3000 repeating a 2.4 GHz network: no cable in the wan port, the uplink is wwan on apcli0
const mt3000Dump = `{"interface": [
	{"interface": "lan", "up": true, "l3_device": "br-lan", "device": "br-lan"},
	{"interface": "guest", "up": false, "device": "br-guest"},
	{"interface": "wan", "up": false, "device": "eth0"},
	{"interface": "wan6", "up": false, "device": "@wan"},
	{"interface": "wwan", "up": true, "l3_device": "apcli0", "device": "apcli0"},
	{"interface": "wgclient", "up": false}
]}`

func TestWANNets(t *testing.T) {
	env := []byte("LAN_NET='lan'\nEXTRA_NETS='guest'\nWAN_NETS='wan wan6 wwan wgclient'\nFG_DIR='/etc/fengard'\n")
	if got := wanNets(env); !slices.Equal(got, []string{"wan", "wan6", "wwan", "wgclient"}) {
		t.Errorf("got %v", got)
	}
	if got := wanNets([]byte("LAN_NET='lan'\n")); got != nil {
		t.Errorf("got %v from an env without them", got)
	}
}

func TestWANDevices(t *testing.T) {
	nets := []string{"wan", "wan6", "wwan", "wgclient"}
	if got := wanDevices([]byte(mt3000Dump), nets); !slices.Equal(got, []string{"eth0", "apcli0"}) {
		t.Errorf("got %v", got)
	}
	if got := wanDevices([]byte("not json"), nets); got != nil {
		t.Errorf("got %v", got)
	}
}

// the uplink joining after fengard started is picked up without a restart, and stays listed
// through a blip since the service script named it at the next start anyway
func TestWANWatch(t *testing.T) {
	dump := `{"interface": [{"interface": "wan", "up": false, "device": "eth0"}, {"interface": "wwan", "up": false}]}`
	var fail error
	w := &wanWatch{flag: []string{"eth0"}, nets: []string{"wan", "wan6", "wwan"}, dump: func() ([]byte, error) { return []byte(dump), fail }}
	if got := w.Devices(); !slices.Equal(got, []string{"eth0"}) {
		t.Errorf("before the uplink: %v", got)
	}
	dump = mt3000Dump
	if got := w.Devices(); !slices.Equal(got, []string{"eth0", "apcli0"}) {
		t.Errorf("with the uplink: %v", got)
	}
	fail = errors.New("ubus is restarting")
	if got := w.Devices(); !slices.Equal(got, []string{"eth0", "apcli0"}) {
		t.Errorf("without ubus: %v", got)
	}
	fail, dump = nil, `{"interface": [{"interface": "wan", "up": false, "device": "eth0"}, {"interface": "wwan", "up": false}]}`
	if got := w.Devices(); !slices.Equal(got, []string{"eth0", "apcli0"}) {
		t.Errorf("with the uplink down again: %v", got)
	}
	// a computer install has nothing to ask
	if got := newWANWatch([]string{"wan", "eth0"}, false).Devices(); !slices.Equal(got, []string{"wan", "eth0"}) {
		t.Errorf("off a router: %v", got)
	}
}

func TestDropWANTriggers(t *testing.T) {
	old := []byte("service_triggers() {\n\tfor n in $LAN_NET $EXTRA_NETS $WAN_NETS; do\n\t\tprocd_add_reload_interface_trigger \"$n\"\n\tdone\n}\n" +
		"\tfor n in $WAN_NETS; do wans=\"$wans $(fg_dev \"$n\")\"; done\n")
	fixed, changed := dropWANTriggers(old)
	want := "service_triggers() {\n\tfor n in $LAN_NET $EXTRA_NETS; do\n\t\tprocd_add_reload_interface_trigger \"$n\"\n\tdone\n}\n" +
		"\tfor n in $WAN_NETS; do wans=\"$wans $(fg_dev \"$n\")\"; done\n"
	if !changed || string(fixed) != want {
		t.Errorf("changed %v:\n%s", changed, fixed)
	}
	if again, changed := dropWANTriggers(fixed); changed || !bytes.Equal(again, fixed) {
		t.Error("changed a script that was already right")
	}

	// the installer writes it that way itself now, and still in the shape this looks for
	inst, err := os.ReadFile("../../install/router-install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(inst, []byte(wanTriggers)) || !bytes.Contains(inst, []byte(noWANTriggers)) {
		t.Error("the installer's service script reloads on the wan networks, or its triggers changed shape")
	}
}
