package vpn

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// tailscale works behind double nat with no port forward fengard just reads its state
const (
	TailscaleIface = "tailscale0"
	TailscaleCGNAT = "100.64.0.0/10"
)

type TSPeer struct {
	ID       string    `json:"id"` // device identity is ts plus this id
	Name     string    `json:"name"`
	OS       string    `json:"os"`
	IPs      []string  `json:"ips"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"lastSeen"`
	RxBytes  int64     `json:"rxBytes"`
	TxBytes  int64     `json:"txBytes"`
}

func (p TSPeer) Identity() string { return "ts:" + strings.ToLower(p.ID) }

type TSStatus struct {
	Supported bool     `json:"supported"`
	Running   bool     `json:"running"`
	State     string   `json:"state"`
	Self      TSPeer   `json:"self"`
	ExitNode  bool     `json:"exitNode"`
	Peers     []TSPeer `json:"peers"`
	Error     string   `json:"error,omitempty"`
}

func TailscaleSupported() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("tailscale")
	return err == nil
}

var (
	tsMu   sync.Mutex
	tsLast TSStatus
	tsAt   time.Time
)

// cached 10s since the tracker and dashboard both ask
func TailscaleStatus(ctx context.Context) TSStatus {
	tsMu.Lock()
	defer tsMu.Unlock()
	if time.Since(tsAt) < 10*time.Second {
		return tsLast
	}
	tsLast, tsAt = readTailscale(ctx), time.Now()
	return tsLast
}

func readTailscale(ctx context.Context) TSStatus {
	st := TSStatus{Supported: TailscaleSupported()}
	if !st.Supported {
		return st
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		st.State, st.Error = "Stopped", "tailscale is not running"
		return st
	}
	return ParseTailscaleStatus(out)
}

func ParseTailscaleStatus(data []byte) TSStatus {
	type node struct {
		ID             string    `json:"ID"`
		HostName       string    `json:"HostName"`
		DNSName        string    `json:"DNSName"`
		OS             string    `json:"OS"`
		TailscaleIPs   []string  `json:"TailscaleIPs"`
		Online         bool      `json:"Online"`
		LastSeen       time.Time `json:"LastSeen"`
		RxBytes        int64     `json:"RxBytes"`
		TxBytes        int64     `json:"TxBytes"`
		ExitNodeOption bool      `json:"ExitNodeOption"`
	}
	var doc struct {
		BackendState string           `json:"BackendState"`
		Self         *node            `json:"Self"`
		Peer         map[string]*node `json:"Peer"`
	}
	st := TSStatus{Supported: true}
	if err := json.Unmarshal(data, &doc); err != nil {
		st.Error = "unreadable tailscale status: " + err.Error()
		return st
	}
	conv := func(n *node) TSPeer {
		// phones say localhost so use the tailnet name
		name := n.HostName
		if label, _, _ := strings.Cut(n.DNSName, "."); label != "" && (name == "" || name == "localhost") {
			name = label
		}
		return TSPeer{ID: n.ID, Name: name, OS: n.OS, IPs: n.TailscaleIPs, Online: n.Online, LastSeen: n.LastSeen, RxBytes: n.RxBytes, TxBytes: n.TxBytes}
	}
	st.State = doc.BackendState
	st.Running = doc.BackendState == "Running"
	if doc.Self != nil {
		st.Self = conv(doc.Self)
		st.ExitNode = doc.Self.ExitNodeOption
	}
	for _, n := range doc.Peer {
		if n != nil && n.ID != "" {
			st.Peers = append(st.Peers, conv(n))
		}
	}
	sort.Slice(st.Peers, func(i, j int) bool { return strings.ToLower(st.Peers[i].Name) < strings.ToLower(st.Peers[j].Name) })
	return st
}
