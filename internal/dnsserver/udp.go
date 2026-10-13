package dnsserver

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/miekg/dns"
)

// the dns library starts a goroutine and a buffer per udp packet with no limit, and a flood of
// random names took memory past 80mb. udp is read here instead and at most maxInFlight lookups run
// at once, the rest wait in the kernel's socket buffer which drops them when it's full
const maxInFlight = 128

// clients ask in well under this, edns tops out at 4096 in practice
const maxQuery = 4096

var queryBufs = sync.Pool{New: func() any { b := make([]byte, maxQuery); return &b }}

func (s *Server) serveUDP(ctx context.Context, pc net.PacketConn, slots chan struct{}) error {
	for {
		b := queryBufs.Get().(*[]byte)
		n, addr, err := pc.ReadFrom(*b)
		if err != nil {
			queryBufs.Put(b)
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		// waiting here is the cap, the kernel holds what comes in meanwhile
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			queryBufs.Put(b)
			return nil
		}
		go func() {
			defer func() {
				queryBufs.Put(b)
				<-slots
			}()
			s.handleUDP(pc, addr, (*b)[:n])
		}()
	}
}

func (s *Server) handleUDP(pc net.PacketConn, addr net.Addr, p []byte) {
	m := new(dns.Msg)
	// garbage and answers get nothing back, replying to them is how servers become amplifiers
	if m.Unpack(p) != nil || m.Response {
		return
	}
	w := &udpWriter{pc: pc, addr: addr}
	switch {
	case m.Opcode != dns.OpcodeQuery:
		s.reply(w, m, dns.RcodeNotImplemented)
	case len(m.Question) != 1:
		s.reply(w, m, dns.RcodeFormatError)
	default:
		s.ServeDNS(w, m)
	}
}

type udpWriter struct {
	pc   net.PacketConn
	addr net.Addr
}

func (w *udpWriter) LocalAddr() net.Addr  { return w.pc.LocalAddr() }
func (w *udpWriter) RemoteAddr() net.Addr { return w.addr }
func (w *udpWriter) WriteMsg(m *dns.Msg) error {
	b, err := m.Pack()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
func (w *udpWriter) Write(b []byte) (int, error) { return w.pc.WriteTo(b, w.addr) }
func (w *udpWriter) Close() error                { return nil }
func (w *udpWriter) TsigStatus() error           { return nil }
func (w *udpWriter) TsigTimersOnly(bool)         {}
func (w *udpWriter) Hijack()                     {}
