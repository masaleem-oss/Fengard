package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/masaleem-oss/Fengard/internal/catalog"
)

func heap() float64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

// what the os sees the heap holding, the number that runs a router out of memory
func held() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapSys - m.HeapReleased
}

func main() {
	// same soft limit the installer gives a 512 mb router
	debug.SetMemoryLimit(96 << 20)
	before := heap()
	debug.FreeOSMemory()
	base := held()
	var peak uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if h := held(); h > peak {
				peak = h
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	start := time.Now()
	c := catalog.New(os.Args[1])
	c.Load()
	took := time.Since(start)
	close(stop)
	<-done
	after := heap()
	fmt.Printf("%d domains: %.1f MB steady-state, %.1f MB peak while loading, %v\n",
		c.Set().Len(), after-before, float64(peak-base)/(1<<20), took.Round(time.Millisecond))
	runtime.KeepAlive(c)
}
