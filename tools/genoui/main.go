// go run ./tools/genoui oui.csv internal/devices/oui.txt.gz
// get oui.csv from standards-oui.ieee.org
package main

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// suffixes that just add length
var suffix = regexp.MustCompile(`(?i)[,.]?\s+(co\.?,?\s*ltd\.?|company\s+limited|corporation|corp\.?|incorporated|inc\.?|limited|ltd\.?|llc|gmbh|s\.?a\.?|ag|b\.?v\.?|pty|plc|technologies|technology|electronics|international|holdings?)\.?$`)

func clean(name string) string {
	name = strings.TrimSpace(name)
	for i := 0; i < 4; i++ {
		n := strings.TrimSpace(suffix.ReplaceAllString(name, ""))
		if n == name || n == "" {
			break
		}
		name = n
	}
	name = strings.TrimRight(name, ",. ")
	if name == strings.ToUpper(name) {
		// title case but keep acronyms like LG HP
		words := strings.Fields(name)
		for i, w := range words {
			if len(w) > 3 {
				words[i] = w[:1] + strings.ToLower(w[1:])
			}
		}
		name = strings.Join(words, " ")
	}
	return name
}

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		panic(err)
	}
	var lines []string
	for _, r := range rows[1:] {
		if r[0] != "MA-L" || len(r[1]) != 6 {
			continue
		}
		lines = append(lines, strings.ToUpper(r[1])+"\t"+clean(r[2]))
	}
	sort.Strings(lines)
	out, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	gz, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
	gz.Write([]byte(strings.Join(lines, "\n") + "\n"))
	gz.Close()
	out.Close()
	fi, _ := os.Stat(os.Args[2])
	fmt.Printf("%d vendors, %d KB\n", len(lines), fi.Size()/1024)
}
