package web

import (
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"
)

var comments = regexp.MustCompile(`(?s)/\*.*?\*/`)

// page has to print exactly what html/template did for our pages, hostile values included
func TestPageMatchesHTMLTemplate(t *testing.T) {
	bad := `<script>alert("x")</script> & 'q'`
	same := func(name, src string, data any, ours any) {
		t.Helper()
		var want, got strings.Builder
		if err := template.Must(template.New(name).Parse(src)).Execute(&want, data); err != nil {
			t.Fatalf("%s: html/template: %v", name, err)
		}
		if err := mustPage(src).Execute(&got, ours); err != nil {
			t.Fatalf("%s: page: %v", name, err)
		}
		// html/template also drops css and js comments, keeping them changes nothing on the page
		w, g := want.String(), comments.ReplaceAllString(got.String(), " ")
		if w != g {
			i := 0
			for i < len(w) && i < len(g) && w[i] == g[i] {
				i++
			}
			lo := max(0, i-80)
			t.Fatalf("%s differs at %d\nwant %q\ngot  %q", name, i, w[lo:min(len(w), i+80)], g[lo:min(len(g), i+80)])
		}
	}

	for i, d := range []map[string]any{
		{"Kind": "blocked", "Title": "This site is blocked", "Message": "Not allowed.", "Domain": "ads.example.com", "Reason": "Ads", "Device": "Phone", "Group": "Kids", "Time": "1 Jan 2026, 10:00", "Network": "Home", "CanAsk": true, "Person": false},
		{"Kind": "paused", "Title": bad, "Message": bad, "Domain": bad, "Reason": bad, "Device": bad, "Group": "", "Time": "x", "Network": bad, "CanAsk": false, "Person": true},
		{"Kind": "pending", "Title": "t", "Message": "m", "Domain": "d", "Reason": "r", "Device": "d", "Group": "g", "Time": "t", "Network": "n", "CanAsk": false, "Person": false},
		{"Kind": "allowed", "Title": "t", "Message": "m", "Domain": "d", "Reason": "r", "Device": "d", "Group": "g", "Time": "t", "Network": "n", "CanAsk": false, "Person": false},
	} {
		same("block"+string(rune('0'+i)), blockPageHTML, d, d)
	}

	full := personView{Name: bad, Status: "on", StatusText: "Internet is on", Used: 75, Limit: 120, Bonus: 15, Left: 60, Pct: 55,
		Limits:   []limitView{{Name: "YouTube", Used: 30, Limit: 30, Left: 0, Pct: 100}, {Name: bad, Used: 5, Limit: 60, Left: 55, Pct: 8}},
		Activity: []limitView{{Name: "Games", Used: 40}},
		Devices:  []personDevice{{Name: "iPad", Used: 50}, {Name: bad}}}
	for i, d := range []portalData{
		{Network: "Home", Device: "Phone", Hours: hm, Dash: "0"},
		{Network: "Home", Device: bad, Hours: hm, Pending: true, Dash: "0"},
		{Network: bad, Device: "Phone", Hours: hm, Dash: "0", View: personView{Name: "Kids", Left: -1}},
		{Network: "Home", Device: "Phone", Hours: hm, Known: true, Dash: "118.8", View: full},
		{Network: "Home", Device: "Phone", Hours: hm, Known: true, Dash: "0", View: personView{Name: "Sam", Status: "timeup", StatusText: "Time's up", Used: 90, Limit: 90, Left: 0, Pct: 100, Limits: []limitView{}, Activity: []limitView{}, Devices: []personDevice{}}},
		{Network: "Home", Device: "Phone", Hours: hm, Known: true, Dash: "0", View: personView{Name: "Al", Status: "on", StatusText: "on", Used: 0, Left: -1}},
	} {
		same("portal"+string(rune('0'+i)), portalHTML, d, d)
	}

	welcome, err := os.ReadFile("../../features/welcome/welcome.html")
	if err != nil {
		t.Fatal(err)
	}
	type tmplWelcome struct {
		Network string
		Blocks  []string
		Joined  bool
		Custom  template.HTML
	}
	for i, d := range []welcomeData{
		{Network: "Home", Blocks: []string{"Malware", bad}},
		{Network: bad, Joined: true},
		{Network: "Home", Custom: rawHTML(`<h1 style="color:red">Hi & welcome</h1>`)},
		{Network: "Home", Custom: rawHTML(`<p>x</p>`), Joined: true},
	} {
		same("welcome"+string(rune('0'+i)), string(welcome), tmplWelcome{d.Network, d.Blocks, d.Joined, template.HTML(d.Custom)}, d)
	}

	for _, u := range []string{"https://192.168.8.2:8843/", ""} {
		same("bootstrap", bootstrapHTML, struct{ URL string }{u}, struct{ URL string }{u})
	}
}
