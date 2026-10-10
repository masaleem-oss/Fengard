package policy

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/config"
)

func TestDomainRefreshDoesNotRevertConfigPublication(t *testing.T) {
	e := New()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 2000 {
			e.SetDomains(&catalog.DomainSet{})
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 200 {
			c := config.Default()
			c.Block = []string{fmt.Sprintf("v%d.example", i)}
			e.Rebuild(c, &catalog.DomainSet{})
		}
	}()
	wg.Wait()
	if e.Decide("", "v199.example", time.Now()).Action != Block {
		t.Fatal("domain refresh reverted latest configuration")
	}
	if e.Decide("", "v0.example", time.Now()).Action == Block {
		t.Fatal("old configuration remains active")
	}
}
