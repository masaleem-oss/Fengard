package vpn

import "testing"

func TestParseTailscaleStatus(t *testing.T) {
	doc := `{"BackendState":"Running","Self":{"ID":"nSELF1","HostName":"GL-MT3000","OS":"linux","TailscaleIPs":["100.101.102.103","fd7a:115c:a1e0::1"],"Online":true,"ExitNodeOption":true},
	"Peer":{"nodekey:aa":{"ID":"nPHONE1","HostName":"localhost","DNSName":"iphone.tail1234.ts.net.","OS":"iOS","TailscaleIPs":["100.64.0.9"],"Online":true,"LastSeen":"2026-10-09T09:00:00Z","RxBytes":10,"TxBytes":20},
	"nodekey:bb":{"ID":"nLAP1","HostName":"laptop","OS":"windows","TailscaleIPs":["100.64.0.10"],"Online":false}}}`
	st := ParseTailscaleStatus([]byte(doc))
	if !st.Running || !st.ExitNode || st.Self.Name != "GL-MT3000" || st.Self.IPs[0] != "100.101.102.103" {
		t.Fatalf("self/state wrong: %+v", st)
	}
	if len(st.Peers) != 2 || st.Peers[0].Name != "iphone" || st.Peers[0].Identity() != "ts:nphone1" || !st.Peers[0].Online || st.Peers[1].Online {
		t.Fatalf("peers wrong: %+v", st.Peers)
	}
	if st := ParseTailscaleStatus([]byte("not json")); st.Error == "" {
		t.Fatal("garbage accepted")
	}
}
