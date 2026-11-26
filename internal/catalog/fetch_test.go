package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// lists from a restored backup sat at downloading until the daily update or a refresh
func TestFetchMissingDownloadsOnlyNewLists(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("imported.example\n"))
	}))
	defer srv.Close()
	c := New(t.TempDir())
	c.SetCustom([]CustomSource{{ID: "a", Name: "A", URL: srv.URL + "/list"}})
	if c.Set().Match("imported.example")&CustomBit(0) != 0 {
		t.Fatal("blocked before it was downloaded")
	}
	c.FetchMissing(context.Background())
	if c.Set().Match("imported.example")&CustomBit(0) == 0 {
		t.Fatal("the imported list isn't loaded after FetchMissing")
	}
	c.FetchMissing(context.Background())
	if n := hits.Load(); n != 1 {
		t.Fatalf("a list with a cached copy got downloaded again, %d downloads", n)
	}
}
