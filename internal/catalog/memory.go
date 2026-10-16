package catalog

import (
	"bufio"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// loading a list peaks at about 2.4 times its file size on the heap, the rest is gc headroom
const loadFactor = 3

// keep this much free for everything else on the router
const memMargin = 24 << 20

// bytes the kernel could hand out right now, 0 when we can't tell (not linux)
var memAvailable = func() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "MemAvailable:" {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func mb(n int64) int64 { return (n + 1<<20 - 1) >> 20 }

// fit leaves out the biggest custom lists until loading the rest fits in free memory. skip holds why a
// list isn't loaded, "" for the ones that are
func (c *Catalog) fit(custom []CustomSource, skip []string) {
	free := memAvailable()
	if free <= 0 {
		return
	}
	var need int64
	for _, cat := range Categories {
		for _, src := range cat.Sources {
			need += fileSize(filepath.Join(c.dir, cacheName(src))) * loadFactor
		}
	}
	size := make([]int64, len(custom))
	var order []int
	for i, cs := range custom {
		if skip[i] != "" {
			continue
		}
		size[i] = fileSize(filepath.Join(c.dir, cacheName(cs.URL))) * loadFactor
		need += size[i]
		order = append(order, i)
	}
	slices.SortFunc(order, func(a, b int) int { return cmp.Compare(size[b], size[a]) })
	for _, i := range order {
		if need <= free-memMargin {
			return
		}
		skip[i] = fmt.Sprintf("Not loaded: it needs about %d MB and the router has %d MB free", mb(size[i]), mb(free))
		need -= size[i]
	}
}

// a load that never finished means the router ran out of memory and killed us part way, so the
// custom lists in it wait until someone changes the lists
const (
	loadingFile = "loading"
	heldFile    = "held"
)

func (c *Catalog) held() map[string]bool {
	b, _ := os.ReadFile(filepath.Join(c.dir, heldFile))
	out := map[string]bool{}
	for _, id := range strings.Fields(string(b)) {
		out[id] = true
	}
	return out
}

// checkCrash runs once before the first load
func (c *Catalog) checkCrash() {
	b, err := os.ReadFile(filepath.Join(c.dir, loadingFile))
	if err != nil {
		return
	}
	os.Remove(filepath.Join(c.dir, loadingFile))
	ids := strings.Fields(string(b))
	if len(ids) == 0 {
		return
	}
	os.WriteFile(filepath.Join(c.dir, heldFile), []byte(strings.Join(ids, "\n")), 0o644)
	c.crashed = true
}

// Crashed is true when the last run died loading blocklists and its custom lists are on hold
func (c *Catalog) Crashed() bool { return c.crashed }

func (c *Catalog) release() { os.Remove(filepath.Join(c.dir, heldFile)) }

// Stop is for a normal shutdown, being stopped part way through a load isn't running out of memory
func (c *Catalog) Stop() {
	c.stopped.Store(true)
	os.Remove(filepath.Join(c.dir, loadingFile))
}
