//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// openwrt firewall hook sends SIGUSR1 after it reloads so rules get reapplied
func resyncSignals() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	return ch
}
