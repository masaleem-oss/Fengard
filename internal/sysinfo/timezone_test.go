package sysinfo

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestZoneName(t *testing.T) {
	cases := map[string]string{
		"config system\n\toption hostname 'GL-MT3000'\n\toption zonename 'Asia/Karachi'\n\toption timezone 'PKT-5'\n": "Asia/Karachi",
		"config system\n\toption zonename 'America/New York'\n":                                                       "America/New_York",
		"config system\n\toption zonename UTC\n":                                                                      "UTC",
		"config system\n\toption timezone 'GMT0'\n":                                                                   "",
	}
	for in, want := range cases {
		got := ZoneName(in)
		if got != want {
			t.Errorf("ZoneName = %q, want %q", got, want)
		}
		if got != "" {
			if _, err := time.LoadLocation(got); err != nil {
				t.Errorf("%q doesn't load: %v", got, err)
			}
		}
	}
}
