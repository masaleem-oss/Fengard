package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/masaleem-oss/Fengard/internal/catalog"
)

func heap() float64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

func main() {
	before := heap()
	c := catalog.New(os.Args[1])
	c.Load()
	after := heap()
	fmt.Printf("%d domains: %.1f MB steady-state\n", c.Set().Len(), after-before)
	runtime.KeepAlive(c)
}
