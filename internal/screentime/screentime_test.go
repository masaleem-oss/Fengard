package screentime

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

func TestCountsMinutesOncePerPerson(t *testing.T) {
	tr := New(nil, time.UTC)
	at := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	// one lookup a minute is just background noise
	tr.Note("phone", "kid", "push.apple.com", nil, at)
	if tr.Used("kid", "") != 0 {
		t.Fatal("a single lookup counted as screen time")
	}
	for _, d := range []string{"a.com", "b.com", "c.com"} {
		tr.Note("phone", "kid", d, []string{"app:fortnite", "cat:gaming"}, at)
	}
	// two devices busy in the same minute count once
	for _, d := range []string{"x.com", "y.com", "z.com"} {
		tr.Note("tablet", "kid", d, nil, at.Add(20*time.Second))
	}
	if got := tr.Used("kid", ""); got != 1 {
		t.Fatalf("total = %d, want 1", got)
	}
	tr.Note("tablet", "kid", "q.com", []string{"app:fortnite"}, at.Add(time.Minute))
	if got := tr.Used("kid", "app:fortnite"); got != 2 {
		t.Fatalf("fortnite = %d, want 2", got)
	}
	if got := tr.Used("kid", "cat:gaming"); got != 1 {
		t.Fatalf("gaming = %d, want 1", got)
	}
	dm := tr.DeviceMinutes("kid")
	if dm["phone"] != 1 || dm["tablet"] != 1 {
		t.Fatalf("device minutes %v", dm)
	}
	if tr.AddBonus("kid", 30) != 30 || tr.Bonus("kid") != 30 {
		t.Fatal("bonus")
	}
}

func TestPersistsAcrossRestart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	kv, _ := db.KV("screentime")
	tr := New(kv, time.Local)
	now := time.Now()
	for _, d := range []string{"a.com", "b.com", "c.com"} {
		tr.Note("phone", "kid", d, []string{"app:roblox"}, now)
	}
	tr.AddBonus("kid", 15)
	if err := tr.Flush(); err != nil {
		t.Fatal(err)
	}
	tr2 := New(kv, time.Local)
	if tr2.Used("kid", "") != 1 || tr2.Used("kid", "app:roblox") != 1 || tr2.Bonus("kid") != 15 {
		t.Fatalf("restore lost data: %d %d %d", tr2.Used("kid", ""), tr2.Used("kid", "app:roblox"), tr2.Bonus("kid"))
	}
}

// last minutes of the day used to fall off the end of the array
func TestLastMinuteOfDay(t *testing.T) {
	var m minuteSet
	if !m.set(minutesPerDay-1) || m.count() != 1 {
		t.Fatal("minute 1439 not counted")
	}
}
