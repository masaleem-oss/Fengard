package policy

import (
	"testing"
	"time"

	"github.com/masaleem-oss/Fengard/internal/catalog"
	"github.com/masaleem-oss/Fengard/internal/config"
)

const (
	kidMAC   = "aa:aa:aa:aa:aa:01"
	adultMAC = "aa:aa:aa:aa:aa:02"
	newMAC   = "aa:aa:aa:aa:aa:03"
)

func setup(t *testing.T, mod func(*config.Config)) *Engine {
	t.Helper()
	c := config.Default()
	c.Settings.Timezone = "UTC"
	c.Devices = []config.Device{
		{MAC: kidMAC, Name: "Kid tablet", Group: "kids", Approved: true},
		{MAC: adultMAC, Name: "Laptop", Group: "default", Approved: true},
	}
	c.Allow = []string{"school.example"}
	c.Groups[1].Block = []string{"blocked-for-kids.example"}
	c.Groups[1].Allow = []string{"ok.adult.example"}
	if mod != nil {
		mod(c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	b := catalog.NewBuilder()
	b.Add("adult.example", catalog.MaskOf([]string{"adult"}))
	b.Add("social.example", catalog.MaskOf([]string{"social"}))
	b.Add("malware.example", catalog.MaskOf([]string{"malware"}))
	b.Add("school.example", catalog.MaskOf([]string{"adult"}))
	e := New()
	e.Rebuild(c, b.Build())
	return e
}

var monday10am = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC) // monday

func TestDecide(t *testing.T) {
	e := setup(t, nil)
	cases := []struct {
		mac, domain string
		want        Action
		reason      string
	}{
		{kidMAC, "www.adult.example", Block, "Adult content"},
		{kidMAC, "ok.adult.example", Allow, "Allow rule"}, // group allow beats category
		{kidMAC, "school.example", Allow, "Allow rule"},   // network allow beats category
		{kidMAC, "blocked-for-kids.example", Block, "Block rule"},
		{kidMAC, "social.example", Allow, ""}, // kids group doesnt block social by default
		{kidMAC, "www.google.com", SafeSearch, "SafeSearch enforced"},
		{adultMAC, "www.adult.example", Allow, ""},
		{adultMAC, "malware.example", Block, "Malware & phishing"},
		{adultMAC, "www.google.com", Allow, ""},
		{"", "malware.example", Block, "Malware & phishing"}, // unidentified goes to default group
	}
	for _, c := range cases {
		d := e.Decide(c.mac, c.domain, monday10am)
		if d.Action != c.want || d.Reason != c.reason {
			t.Errorf("Decide(%s, %s) = %v %q; want %v %q", c.mac, c.domain, d.Action, d.Reason, c.want, c.reason)
		}
	}
}

func TestQuarantineAndPause(t *testing.T) {
	e := setup(t, func(c *config.Config) {
		c.Settings.QuarantineNew = true
		c.Devices[1].PausedTill = monday10am.Add(time.Hour)
	})
	if d := e.Decide(newMAC, "example.com", monday10am); d.Action != Quarantine {
		t.Errorf("unknown device with quarantine on: %v", d.Action)
	}
	if d := e.Decide("", "example.com", monday10am); d.Action != Allow {
		t.Errorf("unidentifiable client must not be quarantined: %v", d.Action)
	}
	if d := e.Decide(adultMAC, "example.com", monday10am); d.Action != Paused {
		t.Errorf("paused device: %v", d.Action)
	}
	if d := e.Decide(adultMAC, "example.com", monday10am.Add(2*time.Hour)); d.Action != Allow {
		t.Errorf("pause should expire: %v", d.Action)
	}
	q, off := e.Offline(monday10am)
	if len(q) != 0 || len(off) != 1 || off[0] != adultMAC {
		t.Errorf("Offline = %v, %v", q, off)
	}
}

func TestSchedules(t *testing.T) {
	e := setup(t, func(c *config.Config) {
		c.Groups[1].Schedules = []config.Schedule{
			// bedtime sun to thu nights crossing midnight
			{Name: "Bedtime", Days: []int{0, 1, 2, 3, 4}, Start: "21:30", End: "07:00", BlockAll: true, Enabled: true},
			// homework social blocked weekdays 16 to 18
			{Name: "Homework", Days: []int{1, 2, 3, 4, 5}, Start: "16:00", End: "18:00", Categories: []string{"social"}, Enabled: true},
			{Name: "Disabled", Days: []int{1}, Start: "00:00", End: "23:59", BlockAll: true, Enabled: false},
		}
	})
	at := func(day, h, m int) time.Time { return time.Date(2026, 10, 4+day, h, m, 0, 0, time.UTC) } // day 0 is sunday 4 oct
	cases := []struct {
		t      time.Time
		domain string
		want   Action
	}{
		{at(1, 22, 0), "example.com", Block},    // monday night
		{at(2, 6, 59), "example.com", Block},    // tuesday early morning still mondays window
		{at(2, 7, 0), "example.com", Allow},     // window ended
		{at(5, 23, 0), "example.com", Allow},    // friday night not scheduled
		{at(6, 3, 0), "example.com", Allow},     // saturday 3am follows friday which isnt a bedtime night
		{at(1, 17, 0), "social.example", Block}, // homework time
		{at(1, 17, 0), "example.com", Allow},    // only social during homework
		{at(1, 18, 0), "social.example", Allow}, // homework over
	}
	for i, c := range cases {
		if d := e.Decide(kidMAC, c.domain, c.t); d.Action != c.want {
			t.Errorf("case %d (%s %s): got %v (%s), want %v", i, c.t.Format("Mon 15:04"), c.domain, d.Action, d.Reason, c.want)
		}
	}
	if _, off := e.Offline(at(1, 23, 0)); len(off) != 1 || off[0] != kidMAC {
		t.Errorf("bedtime device should be offline at firewall level, got %v", off)
	}
}

func TestSafeSearchTargets(t *testing.T) {
	cases := map[string]string{
		"www.google.com":       "forcesafesearch.google.com",
		"www.google.com.au":    "forcesafesearch.google.com",
		"google.co.uk":         "forcesafesearch.google.com",
		"mail.google.com":      "",
		"www.youtube.com":      "restrict.youtube.com",
		"www.bing.com":         "strict.bing.com",
		"duckduckgo.com":       "safe.duckduckgo.com",
		"google.evil.attacker": "",
		"example.com":          "",
	}
	for d, want := range cases {
		if got := safeSearchTarget(d); got != want {
			t.Errorf("safeSearchTarget(%q) = %q, want %q", d, got, want)
		}
	}
}

func BenchmarkDecide(b *testing.B) {
	c := config.Default()
	c.Devices = []config.Device{{MAC: kidMAC, Group: "kids", Approved: true}}
	c.Validate()
	bl := catalog.NewBuilder()
	for i := range 500000 {
		bl.Add("d"+itoa(i)+".example.com", 1<<(i%9))
	}
	e := New()
	e.Rebuild(c, bl.Build())
	now := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		e.Decide(kidMAC, "cdn.static.d123456.example.com", now)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
