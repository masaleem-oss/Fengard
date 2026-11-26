package integrity

import (
	"strings"
)

// Labels are what the dashboard and alerts call each watch
var Labels = map[string]string{
	Packages: "Packages",
	Files:    "System files",
	SSHKeys:  "SSH keys",
	Accounts: "Accounts",
	Startup:  "Startup and scheduled jobs",
	Ports:    "Open ports",
	Exposure: "Open to the internet",
}

// describe says what happened in a line the owner can read
func describe(ch Change, owner string) string {
	switch ch.Watch {
	case Packages:
		switch ch.Kind {
		case "added":
			return "Installed " + ch.Item + " " + ch.After
		case "removed":
			return "Removed " + ch.Item
		}
		return "Updated " + ch.Item + " from " + ch.Before + " to " + ch.After
	case Files:
		switch ch.Kind {
		case "added":
			s := "New " + fileWhat(ch.After) + " " + ch.Item
			if owner != "" {
				s += ", from the " + owner + " package"
			}
			return s
		case "removed":
			return ch.Item + " is back to the firmware's copy or gone"
		}
		return ch.Item + ": " + fileChange(ch.Before, ch.After)
	case SSHKeys:
		fp, file, _ := strings.Cut(ch.Item, " ")
		key := ch.After
		if ch.Kind == "removed" {
			key = ch.Before
		}
		if ch.Kind == "removed" {
			return "SSH key removed from " + file + ": " + key + " (" + fp + ")"
		}
		return "New SSH key in " + file + ": " + key + " (" + fp + ")"
	case Accounts:
		v := ch.After
		if ch.Kind == "removed" {
			v = ch.Before
		}
		uid, shell := account(v)
		admin := ""
		if uid == "0" {
			admin = " with full admin rights"
		}
		switch ch.Kind {
		case "added":
			return "New account " + ch.Item + " that can log in" + admin + " (" + shell + ")"
		case "removed":
			return "Account " + ch.Item + " is gone or can't log in any more"
		}
		bu, bs := account(ch.Before)
		if bu != uid {
			return "Account " + ch.Item + " changed from UID " + bu + " to UID " + uid + admin
		}
		return "Account " + ch.Item + " now uses " + shell + " instead of " + bs
	case Startup:
		if svc, ok := strings.CutPrefix(ch.Item, "service "); ok {
			if ch.Kind == "removed" {
				return svc + " no longer starts at boot"
			}
			return svc + " now starts at boot"
		}
		job := strings.TrimPrefix(ch.Item, "cron ")
		user, line, _ := strings.Cut(job, ": ")
		if ch.Kind == "removed" {
			return "Scheduled job for " + user + " removed: " + line
		}
		return "New scheduled job for " + user + ": " + line
	case Ports:
		prog := ch.After
		if ch.Kind == "removed" {
			prog = ch.Before
		}
		by := ""
		if prog != "" {
			by = " (" + prog + ")"
		}
		switch ch.Kind {
		case "added":
			return "New open port " + portText(ch.Item) + by
		case "removed":
			return "Port " + portText(ch.Item) + " closed" + by
		}
		return "Port " + portText(ch.Item) + " is now held by " + ch.After + " instead of " + ch.Before
	case Exposure:
		name := ch.After
		if ch.Kind == "removed" {
			name = ch.Before
		}
		what := exposureText(ch.Item)
		if name != "" {
			what += " (" + name + ")"
		}
		switch ch.Kind {
		case "added":
			return "New: " + what
		case "removed":
			return "Removed: " + what
		}
		return what + " renamed from " + ch.Before + " to " + ch.After
	}
	return ch.Kind + " " + ch.Item
}

func fileWhat(v string) string {
	switch {
	case strings.HasPrefix(v, "link "):
		return "link"
	case v == "deleted":
		return "deletion of"
	}
	return "file"
}

func fileChange(before, after string) string {
	if before == "deleted" || after == "deleted" || strings.HasPrefix(before, "link ") || strings.HasPrefix(after, "link ") {
		if after == "deleted" {
			return "deleted"
		}
		if t, ok := strings.CutPrefix(after, "link "); ok {
			return "now points to " + t
		}
		return "replaced"
	}
	b, a := strings.Fields(before), strings.Fields(after)
	if len(b) < 4 || len(a) < 4 {
		return "changed"
	}
	var parts []string
	if b[3] != a[3] || b[1] != a[1] {
		parts = append(parts, "contents changed")
	}
	if b[2] != a[2] {
		parts = append(parts, "permissions changed from "+b[2]+" to "+a[2])
	}
	if len(parts) == 0 {
		return "changed"
	}
	return strings.Join(parts, ", ")
}

func account(v string) (uid, shell string) {
	f := strings.Fields(v)
	if len(f) >= 4 {
		return f[1], f[3]
	}
	return "", v
}

func portText(item string) string {
	proto, addr, _ := strings.Cut(item, " ")
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return item
	}
	host, port := addr[:i], addr[i+1:]
	on := " on " + host
	if host == "*" || host == "[*]" {
		on = " on all addresses"
		if host == "[*]" {
			on += " (IPv6)"
		}
	}
	return strings.ToUpper(proto) + " " + port + on
}

func exposureText(item string) string {
	if rest, ok := strings.CutPrefix(item, "fengard forward "); ok {
		return "Fengard port forward " + rest
	}
	if rest, ok := strings.CutPrefix(item, "forward "); ok {
		return "port forward " + rest
	}
	if rest, ok := strings.CutPrefix(item, "allow "); ok {
		return "firewall rule letting the internet reach " + rest
	}
	return item
}
