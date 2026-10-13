//go:build ignore

// draws the readme memory chart from run.sh's ram.csv, a light and a dark svg
//
//	go run dev/memcompare/chart.go ram.csv docs/
package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type theme struct {
	name, surface, ink, ink2, grid string
	series                         map[string]string
}

// the first three slots of the dataviz reference palette, checked for colour blindness in both modes
var themes = []theme{
	{"light", "#fcfcfb", "#0b0b0b", "#52514e", "#e4e3df", map[string]string{
		"Fengard 1.3.1.3": "#2a78d6", "AdGuard Home": "#eb6834", "Fengard 1.3.1.1": "#1baf7a"}},
	{"dark", "#1a1a19", "#f0efec", "#c3c2b7", "#33332f", map[string]string{
		"Fengard 1.3.1.3": "#3987e5", "AdGuard Home": "#d95926", "Fengard 1.3.1.1": "#199e70"}},
}

// legend order, fixed so a colour always means the same program
var order = []string{"Fengard 1.3.1.3", "AdGuard Home", "Fengard 1.3.1.1"}

type pt struct{ t, mb float64 }

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		panic(err)
	}
	data := map[string][]pt{}
	for _, r := range rows[1:] {
		t, _ := strconv.ParseFloat(r[0], 64)
		kb, _ := strconv.ParseFloat(r[2], 64)
		name := r[1]
		if strings.HasPrefix(name, "AdGuard Home") {
			name = "AdGuard Home"
		}
		data[name] = append(data[name], pt{t, kb / 1024})
	}

	const W, H = 900.0, 420.0
	const left, right, top, bottom = 56.0, 150.0, 92.0, 52.0
	pw, ph := W-left-right, H-top-bottom
	ymax := 120.0
	x := func(t float64) float64 { return left + t/360*pw }
	y := func(mb float64) float64 { return top + ph - mb/ymax*ph }

	for _, th := range themes {
		var b strings.Builder
		fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" font-family="-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif" role="img" aria-labelledby="t d">`+"\n", W, H, W, H)
		b.WriteString(`<title id="t">Memory with the same 534k blocked domains</title>` + "\n")
		fmt.Fprintf(&b, `<desc id="d">After a cold start, average total memory: Fengard 1.3.1.3 %.0f MB, Fengard 1.3.1.1 %.0f MB, AdGuard Home %.0f MB.</desc>`+"\n",
			avg(data["Fengard 1.3.1.3"]), avg(data["Fengard 1.3.1.1"]), avg(data["AdGuard Home"]))
		fmt.Fprintf(&b, `<rect width="%.0f" height="%.0f" rx="8" fill="%s"/>`+"\n", W, H, th.surface)
		fmt.Fprintf(&b, `<text x="%.0f" y="32" font-size="17" font-weight="600" fill="%s">Memory with the same 534k blocked domains</text>`+"\n", left, th.ink)
		fmt.Fprintf(&b, `<text x="%.0f" y="54" font-size="13" fill="%s">Total memory (RSS) after a cold start, sampled every 5 s with a few lookups. x86-64 Linux, 2 CPU cores</text>`+"\n", left, th.ink2)
		// legend, swatch then name in text colour
		lx := left
		for _, n := range order {
			fmt.Fprintf(&b, `<rect x="%.0f" y="68" width="14" height="4" rx="2" fill="%s"/><text x="%.0f" y="74" font-size="13" fill="%s">%s</text>`+"\n", lx, th.series[n], lx+20, th.ink, n)
			lx += 20 + float64(len(n))*7.2 + 22
		}
		// grid and axes, quiet
		for v := 0.0; v <= ymax; v += 20 {
			fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`, left, left+pw, y(v), y(v), th.grid)
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="12" text-anchor="end" fill="%s">%.0f MB</text>`+"\n", left-8, y(v)+4, th.ink2, v)
		}
		for m := 0.0; m <= 6; m++ {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="12" text-anchor="middle" fill="%s">%.0f min</text>`+"\n", x(m*60), top+ph+22, th.ink2, m)
		}
		// lines, 2px, then the value at each end in text colour beside a dot in the series colour
		for _, n := range order {
			pts := data[n]
			var d strings.Builder
			for i, p := range pts {
				cmd := "L"
				if i == 0 {
					cmd = "M"
				}
				fmt.Fprintf(&d, "%s%.1f %.1f ", cmd, x(p.t), y(p.mb))
			}
			fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`+"\n", strings.TrimSpace(d.String()), th.series[n])
			last := pts[len(pts)-1]
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="4" fill="%s" stroke="%s" stroke-width="2"/>`, x(last.t), y(last.mb), th.series[n], th.surface)
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="13" fill="%s"><tspan font-weight="600">%.0f MB</tspan> %s</text>`+"\n", x(last.t)+10, y(last.mb)+4, th.ink, last.mb, short(n))
		}
		b.WriteString("</svg>\n")
		out := filepath.Join(os.Args[2], "memory-"+th.name+".svg")
		if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
			panic(err)
		}
		fmt.Println("wrote", out)
	}
}

// after the first minute, once everything has loaded its lists
func avg(pts []pt) float64 {
	s, n := 0.0, 0
	for _, p := range pts {
		if p.t >= 60 {
			s += p.mb
			n++
		}
	}
	return math.Round(s / float64(n))
}

func short(n string) string {
	switch n {
	case "Fengard 1.3.1.3":
		return "1.3.1.3"
	case "Fengard 1.3.1.1":
		return "1.3.1.1"
	}
	return "AdGuard"
}
