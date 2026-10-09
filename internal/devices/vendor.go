package devices

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"net"
	"strconv"
	"sync"
)

// ieee ma-l registry regenerated with tools/genoui
//
//go:embed oui.txt.gz
var ouiGz []byte

var (
	ouiOnce sync.Once
	ouiMap  map[uint32]string
)

func loadOUI() {
	ouiMap = make(map[uint32]string, 40000)
	zr, err := gzip.NewReader(bytes.NewReader(ouiGz))
	if err != nil {
		return
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) < 8 || line[6] != '\t' {
			continue
		}
		if n, err := strconv.ParseUint(string(line[:6]), 16, 32); err == nil {
			ouiMap[uint32(n)] = string(line[7:])
		}
	}
}

func Vendor(mac string) string {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) < 3 || IsRandomized(mac) {
		return ""
	}
	ouiOnce.Do(loadOUI)
	return ouiMap[uint32(hw[0])<<16|uint32(hw[1])<<8|uint32(hw[2])]
}

// locally administered bit is what phones use for private wifi addresses
func IsRandomized(mac string) bool {
	hw, err := net.ParseMAC(mac)
	return err == nil && len(hw) > 0 && hw[0]&0x02 != 0
}
