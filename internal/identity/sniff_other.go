//go:build !linux

package identity

import "context"

// only routers run linux, dev builds just go without fingerprints
func Sniff(ctx context.Context, ifaces []string, got func(mac string, p Print)) {}
