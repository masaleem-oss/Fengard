//go:build !linux

package dnsserver

import (
	"context"
	"net"
)

// only linux shares a port between sockets here, everything else reads with one
func listenUDP(ctx context.Context, addr string, _ bool) (net.PacketConn, error) {
	return (&net.ListenConfig{}).ListenPacket(ctx, "udp", addr)
}
