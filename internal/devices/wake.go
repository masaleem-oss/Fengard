package devices

import (
	"bytes"
	"errors"
	"net"
)

// device needs wol turned on in its firmware
func Wake(mac string) error {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return errors.New("invalid MAC address")
	}
	packet := bytes.Repeat([]byte{0xff}, 6)
	packet = append(packet, bytes.Repeat(hw, 16)...)

	targets := []net.IP{net.IPv4bcast}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				bcast := make(net.IP, 4)
				for i := range bcast {
					bcast[i] = n.IP.To4()[i] | ^n.Mask[i]
				}
				targets = append(targets, bcast)
			}
		}
	}
	var sent bool
	for _, ip := range targets {
		for _, port := range []int{9, 7} {
			c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip, Port: port})
			if err != nil {
				continue
			}
			if _, err := c.Write(packet); err == nil {
				sent = true
			}
			c.Close()
		}
	}
	if !sent {
		return errors.New("couldn't send the wake-up packet")
	}
	return nil
}
