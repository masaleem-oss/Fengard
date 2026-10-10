package catalog

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentCustomLoadsKeepMaskLayoutTogether(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	sources := []CustomSource{{ID: "a", Name: "A", URL: "https://a.example/list"}, {ID: "b", Name: "B", URL: "https://b.example/list"}}
	for _, src := range sources {
		if err := os.WriteFile(filepath.Join(dir, cacheName(src.URL)), []byte(src.ID+".example\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.OnChange = func() {
		custom := c.Custom()
		if len(custom) == 1 && c.Set().Match(custom[0].ID+".example")&CustomBit(0) == 0 {
			t.Error("published set uses a different custom-list layout")
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 20 {
			if err := c.Load(); err != nil {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 20 {
			c.SetCustom([]CustomSource{sources[i%2]})
		}
	}()
	wg.Wait()
	custom := c.Custom()
	if len(custom) != 1 || custom[0].ID != "b" || c.Set().Match("b.example")&CustomBit(0) == 0 || c.Set().Match("a.example")&CustomBit(0) != 0 {
		t.Fatal("latest layout and domain set differ")
	}
}
