package identity

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// only udp to port 67 gets past the kernel, so dns and everything else never wakes us up
var dhcpFilter = []bpf.Instruction{
	bpf.LoadAbsolute{Off: 12, Size: 2},                          // ethertype
	bpf.JumpIf{Cond: bpf.JumpEqual, Val: 0x0800, SkipFalse: 8},  // ipv4
	bpf.LoadAbsolute{Off: 23, Size: 1},                          // protocol
	bpf.JumpIf{Cond: bpf.JumpEqual, Val: 17, SkipFalse: 6},      // udp
	bpf.LoadAbsolute{Off: 20, Size: 2},                          // flags and fragment offset
	bpf.JumpIf{Cond: bpf.JumpBitsSet, Val: 0x1fff, SkipTrue: 4}, // not the first fragment
	bpf.LoadMemShift{Off: 14},                                   // x is the ip header length
	bpf.LoadIndirect{Off: 16, Size: 2},                          // udp destination port
	bpf.JumpIf{Cond: bpf.JumpEqual, Val: 67, SkipFalse: 1},
	bpf.RetConstant{Val: 1600},
	bpf.RetConstant{Val: 0},
}

// Sniff listens for devices asking for an address on the lan bridges, it only reads
// what dnsmasq gets anyway and never answers
func Sniff(ctx context.Context, ifaces []string, got func(mac string, p Print)) {
	prog, err := bpf.Assemble(dhcpFilter)
	if err != nil {
		log.Printf("dhcp fingerprints: %v", err)
		return
	}
	filter := make([]unix.SockFilter, len(prog))
	for i, r := range prog {
		filter[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	for _, name := range ifaces {
		ifc, err := net.InterfaceByName(name)
		if err != nil {
			continue
		}
		fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_IP)))
		if err != nil {
			log.Printf("dhcp fingerprints off: %v", err)
			return
		}
		fprog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
		if err := unix.SetsockoptSockFprog(fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &fprog); err != nil {
			unix.Close(fd)
			log.Printf("dhcp fingerprints off on %s: %v", name, err)
			continue
		}
		if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_IP), Ifindex: ifc.Index}); err != nil {
			unix.Close(fd)
			log.Printf("dhcp fingerprints off on %s: %v", name, err)
			continue
		}
		// wake up now and then to notice shutdown
		unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1})
		go read(ctx, fd, got)
	}
}

func read(ctx context.Context, fd int, got func(string, Print)) {
	defer unix.Close(fd)
	buf := make([]byte, 1600)
	for ctx.Err() == nil {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil || n < 14+20+8 {
			continue
		}
		ip := buf[14:n]
		ihl := int(ip[0]&0x0f) * 4
		if ihl < 20 || len(ip) < ihl+8 {
			continue
		}
		udp := ip[ihl:]
		if int(binary.BigEndian.Uint16(udp[4:6])) < 8 {
			continue
		}
		if mac, p, ok := ParseDHCP(udp[8:], time.Now()); ok {
			got(mac, p)
		}
	}
}

// network order whatever the cpu is, mips routers are big endian
func htons(v uint16) uint16 { return binary.NativeEndian.Uint16([]byte{byte(v >> 8), byte(v)}) }
