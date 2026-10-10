package presence

import (
	"context"
	"testing"
	"time"
)

func TestArrivesStraightAwayLeavesAfterAWhile(t *testing.T) {
	on := map[string]bool{}
	var got []string
	w := &Watcher{
		Tracked: func() []string { return []string{"AA:BB:CC:DD:EE:01"} },
		Clients: func(context.Context) (map[string]bool, error) { return on, nil },
		OnChange: func(mac string, home bool, _ time.Time) {
			if home {
				got = append(got, "arrived")
			} else {
				got = append(got, "left")
			}
		},
	}
	ctx := context.Background()
	t0 := time.Now()
	w.Check(ctx, t0)
	on["aa:bb:cc:dd:ee:01"] = true
	w.Check(ctx, t0.Add(time.Minute))
	delete(on, "aa:bb:cc:dd:ee:01")
	// a sleeping phone drops off for a few minutes
	w.Check(ctx, t0.Add(5*time.Minute))
	if len(got) != 1 || got[0] != "arrived" {
		t.Fatalf("got %v", got)
	}
	w.Check(ctx, t0.Add(12*time.Minute))
	if len(got) != 2 || got[1] != "left" {
		t.Fatalf("got %v", got)
	}
	if d := w.Devices(); len(d) != 1 || d[0].Home || !d[0].Since.Equal(t0.Add(time.Minute)) {
		t.Fatalf("left time should be when it was last seen: %+v", d)
	}
}

func TestReadsIwinfo(t *testing.T) {
	iw := "apclix0   ESSID: \"Upstream\"\n          Mode: Client  Channel: 100\n\nra0       ESSID: \"Home\"\n          Access Point: 12:C3:C0:39:EC:89\n          Mode: Master  Channel: 11\n\nrax0      ESSID: \"Home\"\n          Mode: Master  Channel: 36\n"
	if ap := accessPoints(iw); len(ap) != 2 || ap[0] != "ra0" || ap[1] != "rax0" {
		t.Fatalf("access points %v", ap)
	}
	list := "CE:5B:5A:C8:1E:95  -29 dBm / unknown (SNR -29)  0 ms ago\n\tRX: 573.0 MBit/s, HE-MCS 11\n\texpected throughput: unknown\n"
	if c := ParseAssoc(list); !c["ce:5b:5a:c8:1e:95"] || len(c) != 1 {
		t.Fatalf("clients %v", c)
	}
}
