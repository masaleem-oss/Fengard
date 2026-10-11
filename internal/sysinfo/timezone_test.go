package sysinfo

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestZoneFromPath(t *testing.T) {
	cases := map[string]string{
		"/usr/share/zoneinfo/Australia/Melbourne":  "Australia/Melbourne",
		"../usr/share/zoneinfo/posix/Asia/Karachi": "Asia/Karachi",
		"/tmp/localtime":                  "",
		"/usr/share/zoneinfo/Not/A_Place": "",
	}
	for in, want := range cases {
		if got := zoneFromPath(in); got != want {
			t.Errorf("zoneFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

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
