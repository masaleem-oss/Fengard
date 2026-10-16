package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeList(t *testing.T, dir, url string, n int) {
	t.Helper()
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "d%d.%s.example\n", i, filepath.Base(url))
	}
	if err := os.WriteFile(filepath.Join(dir, cacheName(url)), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func status(c *Catalog, id string) Status {
	for _, s := range c.Status() {
		if s.ID == id {
			return s
		}
	}
	return Status{}
}

func withFree(t *testing.T, n int64) {
	old := memAvailable
	memAvailable = func() int64 { return n }
	t.Cleanup(func() { memAvailable = old })
}

// a list no profile uses gets counted so people see its size, but takes no memory
func TestIdleListCountedNotLoaded(t *testing.T) {
	dir := t.TempDir()
	writeList(t, dir, "https://x/big", 500)
	c := New(dir, CustomSource{ID: "big", Name: "Big", URL: "https://x/big", Idle: true})
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	st := status(c, "list:big")
	if st.Domains != 500 || !st.Idle {
		t.Fatalf("status = %+v", st)
	}
	if c.Set().Match("d1.big.example")&CustomBit(0) != 0 {
		t.Error("an idle list got loaded")
	}
}

// the biggest list that won't fit in free memory is left out and the rest still load
func TestListThatDoesntFitIsLeftOut(t *testing.T) {
	dir := t.TempDir()
	writeList(t, dir, "https://x/small", 100)
	writeList(t, dir, "https://x/huge", 20000)
	small := fileSize(filepath.Join(dir, cacheName("https://x/small")))
	withFree(t, memMargin+small*loadFactor*2)
	c := New(dir, CustomSource{ID: "small", URL: "https://x/small"}, CustomSource{ID: "huge", URL: "https://x/huge"})
	c.Load()
	if c.Set().Match("d1.small.example") != CustomBit(0) {
		t.Error("the small list didn't load")
	}
	if c.Set().Match("d1.huge.example") != 0 {
		t.Error("the huge list loaded anyway")
	}
	if st := status(c, "list:huge"); !strings.Contains(st.Held, "MB free") || st.Domains != 20000 {
		t.Errorf("huge = %+v", st)
	}
}

// dying part way through a load (the kernel killing us for memory) holds those lists on the next start
func TestCrashedLoadHoldsLists(t *testing.T) {
	dir := t.TempDir()
	writeList(t, dir, "https://x/a", 50)
	os.WriteFile(filepath.Join(dir, loadingFile), []byte("a"), 0o644)
	src := CustomSource{ID: "a", URL: "https://x/a"}
	c := New(dir, src)
	c.Load()
	if !c.Crashed() || c.Set().Match("d1.a.example") != 0 || status(c, "list:a").Held == "" {
		t.Fatalf("crashed %v, status %+v", c.Crashed(), status(c, "list:a"))
	}
	// still held after another restart
	c = New(dir, src)
	c.Load()
	if c.Set().Match("d1.a.example") != 0 {
		t.Fatal("the held list came back on its own")
	}
	// changing the lists tries again
	c.SetCustom([]CustomSource{src})
	if c.Set().Match("d1.a.example") != CustomBit(0) || status(c, "list:a").Held != "" {
		t.Fatal("changing the lists didn't load it again")
	}
	if _, err := os.Stat(filepath.Join(dir, loadingFile)); err == nil {
		t.Error("the loading marker was left behind")
	}
}
