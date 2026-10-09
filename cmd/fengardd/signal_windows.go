//go:build windows

package main

import "os"

// no SIGUSR1 on windows and the firewall isnt used here anyway
func resyncSignals() <-chan os.Signal { return make(chan os.Signal) }
