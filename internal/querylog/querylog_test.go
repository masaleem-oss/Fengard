package querylog

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/store"
)

func TestStatsSurviveRestart(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logdb, _ := db.Log("queries")

	l := New(100, logdb, nil)
	now := time.Now()
	l.Add(Entry{Time: now.Add(-30 * time.Hour), Domain: "old.example", Action: "blocked"}) // outside 24h
	for i := range 10 {
		l.Add(Entry{Time: now.Add(-time.Duration(i) * time.Minute), Domain: "ads.example", Device: "Phone", Action: "blocked", Category: "ads"})
	}
	l.Add(Entry{Time: now, Domain: "ok.example", Device: "Phone", Action: "allowed"})
	time.Sleep(1500 * time.Millisecond) // let the async writer flush

	r := New(100, logdb, nil) // restart
	st := r.Stats()
	if st.Total != 11 || st.Blocked != 10 {
		t.Fatalf("restored total=%d blocked=%d, want 11/10", st.Total, st.Blocked)
	}
	if len(st.Categories) != 1 || st.Categories[0].Name != "ads" || st.Categories[0].Count != 10 {
		t.Errorf("categories = %+v", st.Categories)
	}
	if got := r.Recent(1); len(got) != 1 || got[0].Domain != "ok.example" {
		t.Errorf("newest recent entry = %+v", got)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := len(r.History(Filter{Limit: 1000})); n != 12 {
		t.Errorf("history has %d entries after restore, want 12 (restore must not re-persist)", n)
	}
}
