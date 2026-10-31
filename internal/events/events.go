// Package events turns raw logread lines into a short timeline of things a
// person cares about: devices joining and leaving, interfaces going up and
// down, logins, cron jobs and reboots. Everything else is ignored.
package events

import (
	"regexp"
	"strings"

	"github.com/madushakv/owrt-tui/internal/logs"
)

// Kind is the type of event.
type Kind string

const (
	WifiJoin  Kind = "wifi-join"
	WifiLeave Kind = "wifi-leave"
	DHCP      Kind = "dhcp"
	IfaceUp   Kind = "iface-up"
	IfaceDown Kind = "iface-down"
	LinkUp    Kind = "link-up"
	LinkDown  Kind = "link-down"
	SSHOK     Kind = "ssh-ok"
	SSHFail   Kind = "ssh-fail"
	Cron      Kind = "cron"
	Boot      Kind = "boot"
)

// Group is the filter bucket an event belongs to.
type Group string

const (
	GClients  Group = "clients"
	GNetwork  Group = "network"
	GSecurity Group = "security"
	GSystem   Group = "system"
)

// Groups lists the buckets in display order.
var Groups = []Group{GClients, GNetwork, GSecurity, GSystem}

// Severity of an event, for colouring.
type Severity int

const (
	Info Severity = iota
	Warn
	Bad
)

// Event is one timeline entry.
type Event struct {
	Time    string // as logged
	Kind    Kind
	Group   Group
	Sev     Severity
	Subject string // MAC, interface, user or command
	Name    string // friendly name for a MAC, when known
	Detail  string
}

// Label is a short human description.
func (e Event) Label() string {
	who := e.Subject
	if e.Name != "" {
		who = e.Name
	}
	switch e.Kind {
	case WifiJoin:
		return who + " joined Wi-Fi " + e.Detail
	case WifiLeave:
		return who + " left Wi-Fi " + e.Detail
	case DHCP:
		return who + " got " + e.Detail
	case IfaceUp:
		return "interface " + e.Subject + " is up"
	case IfaceDown:
		return "interface " + e.Subject + " " + e.Detail
	case LinkUp:
		return "link up on " + e.Subject
	case LinkDown:
		return "link down on " + e.Subject
	case SSHOK:
		return "SSH login for " + e.Subject + " " + e.Detail
	case SSHFail:
		return "failed SSH login for " + e.Subject + " " + e.Detail
	case Cron:
		return "cron ran: " + e.Detail
	case Boot:
		return "router booted"
	}
	return e.Detail
}

var (
	reSta   = regexp.MustCompile(`^(\S+): AP-STA-(CONNECTED|DISCONNECTED) ([0-9a-fA-F:]{17})`)
	reDHCP  = regexp.MustCompile(`^DHCPACK\((\S+)\) (\S+) ([0-9a-fA-F:]{17}) ?(\S*)`)
	reIface = regexp.MustCompile(`^Interface '([^']+)' (is now up|is now down|has link connectivity loss|is disabled|is setting up now)`)
	reLink  = regexp.MustCompile(`^Network device '([^']+)' link is (up|down)`)
	reOK    = regexp.MustCompile(`^(?:Pubkey|Password) auth succeeded for '([^']+)'(?: with .*?)? from (\S+)`)
	reBad   = regexp.MustCompile(`^(?:Bad password attempt|Login attempt for nonexistent user)(?: for)? '?([^' ]*)'? from (\S+)`)
	reCron  = regexp.MustCompile(`^USER (\S+) pid \d+ cmd (.*)$`)
)

// Parse converts one log entry; ok is false when it is not interesting.
func Parse(e logs.Entry) (Event, bool) {
	ev := Event{Time: e.Time}
	m := e.Msg
	switch {
	case e.Proc == "hostapd":
		if g := reSta.FindStringSubmatch(m); g != nil {
			ev.Subject, ev.Group, ev.Detail = strings.ToLower(g[3]), GClients, "("+g[1]+")"
			ev.Kind = WifiJoin
			if g[2] == "DISCONNECTED" {
				ev.Kind = WifiLeave
			}
			return ev, true
		}
	case strings.HasPrefix(e.Proc, "dnsmasq-dhcp"):
		if g := reDHCP.FindStringSubmatch(m); g != nil {
			ev.Kind, ev.Group, ev.Subject = DHCP, GClients, strings.ToLower(g[3])
			ev.Detail = g[2] + " on " + g[1]
			ev.Name = g[4]
			return ev, true
		}
	case e.Proc == "netifd":
		if g := reIface.FindStringSubmatch(m); g != nil {
			ev.Group, ev.Subject = GNetwork, g[1]
			switch g[2] {
			case "is now up":
				ev.Kind = IfaceUp
			case "is now down", "is disabled":
				ev.Kind, ev.Detail, ev.Sev = IfaceDown, "went down", Warn
			case "has link connectivity loss":
				ev.Kind, ev.Detail, ev.Sev = IfaceDown, "lost connectivity", Warn
			default:
				return ev, false // "setting up" is noise
			}
			return ev, true
		}
		if g := reLink.FindStringSubmatch(m); g != nil {
			ev.Group, ev.Subject, ev.Kind = GNetwork, g[1], LinkUp
			if g[2] == "down" {
				ev.Kind, ev.Sev = LinkDown, Warn
			}
			return ev, true
		}
	case e.Proc == "dropbear":
		if g := reOK.FindStringSubmatch(m); g != nil {
			ev.Kind, ev.Group, ev.Subject, ev.Detail = SSHOK, GSecurity, g[1], "from "+hostOnly(g[2])
			return ev, true
		}
		if g := reBad.FindStringSubmatch(m); g != nil {
			ev.Kind, ev.Group, ev.Sev, ev.Subject, ev.Detail = SSHFail, GSecurity, Bad, g[1], "from "+hostOnly(g[2])
			return ev, true
		}
	case e.Proc == "crond":
		if g := reCron.FindStringSubmatch(m); g != nil {
			ev.Kind, ev.Group, ev.Subject, ev.Detail = Cron, GSystem, g[1], g[2]
			return ev, true
		}
	case e.Proc == "syslogd" && strings.HasPrefix(m, "started"):
		ev.Kind, ev.Group = Boot, GSystem
		return ev, true
	case e.Proc == "kernel" && strings.Contains(m, "Linux version"):
		ev.Kind, ev.Group = Boot, GSystem
		return ev, true
	}
	return ev, false
}

func hostOnly(hp string) string {
	if i := strings.LastIndex(hp, ":"); i > 0 && !strings.Contains(hp[i:], "]") {
		return hp[:i]
	}
	return hp
}

// Build parses entries (oldest first, as logread prints them) and returns the
// events newest first. MAC addresses are named from DHCP hostnames seen in the
// same batch, then from extra (MAC -> name), which wins.
func Build(in []logs.Entry, extra map[string]string) []Event {
	var evs []Event
	names := map[string]string{}
	for _, l := range in {
		if ev, ok := Parse(l); ok {
			if ev.Kind == DHCP && ev.Name != "" && ev.Name != "*" {
				names[ev.Subject] = ev.Name
			}
			evs = append(evs, ev)
		}
	}
	for i := range evs {
		ev := &evs[i]
		if ev.Group != GClients {
			continue
		}
		if n := extra[ev.Subject]; n != "" {
			ev.Name = n
		} else if n := names[ev.Subject]; n != "" {
			ev.Name = n
		} else if ev.Name == "*" {
			ev.Name = ""
		}
	}
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}
	return evs
}

// Filter keeps events in the group; an empty group keeps all.
func Filter(in []Event, g Group) []Event {
	if g == "" {
		return in
	}
	var out []Event
	for _, e := range in {
		if e.Group == g {
			out = append(out, e)
		}
	}
	return out
}

// BruteForce reports source addresses with at least n failed SSH logins.
func BruteForce(in []Event, n int) map[string]int {
	c := map[string]int{}
	for _, e := range in {
		if e.Kind == SSHFail {
			c[strings.TrimPrefix(e.Detail, "from ")]++
		}
	}
	for k, v := range c {
		if v < n {
			delete(c, k)
		}
	}
	return c
}
