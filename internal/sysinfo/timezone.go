package sysinfo

import (
	"os"
	"strings"
	"time"
)

// openwrt keeps the timezone in uci and has no /etc/localtime so go would think every router is on utc
// this points time.Local at the zone set in the router settings when there is one
func UseRouterTimezone() string {
	if os.Getenv("TZ") != "" {
		return ""
	}
	if _, err := os.Stat("/etc/localtime"); err == nil {
		return ""
	}
	b, err := os.ReadFile("/etc/config/system")
	if err != nil {
		return ""
	}
	name := ZoneName(string(b))
	if name == "" {
		return ""
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return ""
	}
	time.Local = loc
	return name
}

// the zonename option from /etc/config/system, openwrt writes spaces where the tz database has underscores
func ZoneName(system string) string {
	for _, line := range strings.Split(system, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "option" && f[1] == "zonename" {
			v := strings.Join(f[2:], " ")
			v = strings.Trim(v, `'"`)
			return strings.ReplaceAll(v, " ", "_")
		}
	}
	return ""
}
