package devices

import (
	"bufio"
	"compress/gzip"
	"log"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// the ieee ma-l registry from the manufacturer names add-on, tools/genoui makes the file.
// sorted prefixes pointing into one string of unique names, about a third of a map's memory
type vendorTable struct {
	keys  []uint32
	name  []uint16 // which name each prefix has
	ends  []uint32 // where each name stops in names
	names string
}

var (
	vendorMu   sync.Mutex
	vendorPath string
	vendors    *vendorTable
)

// UseVendors points at the registry, it loads the first time a name is asked for.
// an empty path drops the names and the memory they took
func UseVendors(path string) {
	vendorMu.Lock()
	vendorPath, vendors = path, nil
	vendorMu.Unlock()
}

func loadVendors(path string) (*vendorTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	type row struct {
		key  uint32
		name string
	}
	var rows []row
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) < 8 || line[6] != '\t' {
			continue
		}
		if n, err := strconv.ParseUint(string(line[:6]), 16, 32); err == nil {
			rows = append(rows, row{uint32(n), string(line[7:])})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b row) int { return int(a.key) - int(b.key) })
	t := &vendorTable{keys: make([]uint32, 0, len(rows)), name: make([]uint16, 0, len(rows))}
	seen := map[string]uint16{}
	var names strings.Builder
	for _, r := range rows {
		i, ok := seen[r.name]
		if !ok {
			if len(t.ends) == 1<<16 {
				continue
			}
			i = uint16(len(t.ends))
			seen[r.name] = i
			names.WriteString(r.name)
			t.ends = append(t.ends, uint32(names.Len()))
		}
		t.keys = append(t.keys, r.key)
		t.name = append(t.name, i)
	}
	t.names = names.String()
	t.ends = slices.Clip(t.ends)
	return t, nil
}

func Vendor(mac string) string {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) < 3 || IsRandomized(mac) {
		return ""
	}
	vendorMu.Lock()
	if vendors == nil && vendorPath != "" {
		var err error
		if vendors, err = loadVendors(vendorPath); err != nil {
			log.Printf("manufacturer names: %v", err)
			vendorPath = ""
		}
	}
	t := vendors
	vendorMu.Unlock()
	if t == nil {
		return ""
	}
	i, ok := slices.BinarySearch(t.keys, uint32(hw[0])<<16|uint32(hw[1])<<8|uint32(hw[2]))
	if !ok {
		return ""
	}
	n := t.name[i]
	start := uint32(0)
	if n > 0 {
		start = t.ends[n-1]
	}
	return t.names[start:t.ends[n]]
}

// locally administered bit is what phones use for private wifi addresses
func IsRandomized(mac string) bool {
	hw, err := net.ParseMAC(mac)
	return err == nil && len(hw) > 0 && hw[0]&0x02 != 0
}
