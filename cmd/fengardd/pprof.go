//go:build pprof

// dev only memory profiling, release builds leave it out
//
//	go build -tags pprof ./cmd/fengardd && FENGARD_PPROF=127.0.0.1:6060 ./fengardd ...
package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
)

func init() {
	addr := os.Getenv("FENGARD_PPROF")
	if addr == "" {
		return
	}
	go func() { log.Println(http.ListenAndServe(addr, nil)) }()
}
