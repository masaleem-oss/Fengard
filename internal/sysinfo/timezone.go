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

// RouterZone is the city name of the routers own zone like Asia/Karachi or "" when it cant tell
// utc doesnt count since thats what openwrt ships with before anyone sets it
func RouterZone() string {
	name := ""
	if b, err := os.ReadFile("/etc/config/system"); err == nil {
		name = ZoneName(string(b))
	}
	// other linux boxes link localtime into the zoneinfo folder
	if t, err := os.Readlink("/etc/localtime"); err == nil && name == "" {
		name = zoneFromPath(t)
	}
	return cityZone(name)
}

func cityZone(name string) string {
	switch strings.TrimPrefix(name, "Etc/") {
	case "", "UTC", "UCT", "GMT", "Zulu", "Universal", "Greenwich":
		return ""
	}
	if _, err := time.LoadLocation(name); err != nil {
		return ""
	}
	return name
}

func zoneFromPath(p string) string {
	_, name, ok := strings.Cut(p, "zoneinfo/")
	if !ok {
		return ""
	}
	name = strings.TrimPrefix(strings.TrimPrefix(name, "posix/"), "right/")
	if _, err := time.LoadLocation(name); err != nil {
		return ""
	}
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
