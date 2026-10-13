package dnsserver

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// SO_REUSEPORT so the kernel spreads packets over several sockets on the same address
func listenUDP(ctx context.Context, addr string, shared bool) (net.PacketConn, error) {
	lc := net.ListenConfig{}
	if shared {
		lc.Control = func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			}); err != nil {
				return err
			}
			return serr
		}
	}
	return lc.ListenPacket(ctx, "udp", addr)
}
