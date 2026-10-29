// Package clients merges what the router knows about each device on the
// network (DHCP leases, neighbour table, Wi-Fi stations, static reservations,
// block rules, routing policy) into one list, and builds the staged changes
// for the things a user does to a client. It never talks to the router.
package clients

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// Script reads the sources. Read-only.
const Script = `echo "##leases"; cat /tmp/dhcp.leases 2>/dev/null
echo "##neigh"; ip -4 neigh show 2>/dev/null
for i in $(iw dev 2>/dev/null | awk '$1=="Interface"{print $2}'); do echo "##sta $i"; iw dev "$i" station dump 2>/dev/null; done
`

// Raw is the parsed output of Script.
type Raw struct {
	Leases   []Lease
	Neigh    []Neigh
	Stations []Station
}

type Lease struct {
	Expiry int64
	MAC    string
	IP     string
	Name   string
}

type Neigh struct {
	IP, Dev, MAC, State string
}

type Station struct {
	MAC, Iface    string
	Signal        int // dBm, 0 = unknown
	RxBytes       int64
	TxBytes       int64
	ConnectedSecs int
}

var macRe = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if len(s) > 63 {
		s = s[:63]
	}
	if s == "*" {
		return ""
	}
	return s
}

// ParseRaw reads the output of Script.
func ParseRaw(out string) Raw {
	var r Raw
	sec, iface := "", ""
	var cur *Station
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "##") {
			f := strings.Fields(l[2:])
			sec, iface, cur = "", "", nil
			if len(f) > 0 {
				sec = f[0]
			}
			if len(f) > 1 {
				iface = f[1]
			}
			continue
		}
		switch sec {
		case "leases":
			f := strings.Fields(l)
			if len(f) >= 4 && macRe.MatchString(lower(f[1])) {
				exp, _ := strconv.ParseInt(f[0], 10, 64)
				r.Leases = append(r.Leases, Lease{exp, lower(f[1]), f[2], cleanName(f[3])})
			}
		case "neigh":
			f := strings.Fields(l)
			n := Neigh{}
			if len(f) < 4 {
				continue
			}
			n.IP = f[0]
			for i := 1; i+1 < len(f); i++ {
				switch f[i] {
				case "dev":
					n.Dev = f[i+1]
				case "lladdr":
					n.MAC = lower(f[i+1])
				}
			}
			n.State = f[len(f)-1]
			if macRe.MatchString(n.MAC) {
				r.Neigh = append(r.Neigh, n)
			}
		case "sta":
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "Station ") {
				f := strings.Fields(t)
				if len(f) >= 2 && macRe.MatchString(lower(f[1])) {
					r.Stations = append(r.Stations, Station{MAC: lower(f[1]), Iface: iface})
					cur = &r.Stations[len(r.Stations)-1]
				}
				continue
			}
			if cur == nil {
				continue
			}
			k, v, ok := strings.Cut(t, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			num := func() int64 { f := strings.Fields(v); n, _ := strconv.ParseInt(firstField(f), 10, 64); return n }
			switch k {
			case "signal":
				cur.Signal = int(num())
			case "rx bytes":
				cur.RxBytes = num()
			case "tx bytes":
				cur.TxBytes = num()
			case "connected time":
				cur.ConnectedSecs = int(num())
			}
		}
	}
	return r
}

func firstField(f []string) string {
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// Client is one device.
type Client struct {
	MAC, IP, Name string
	Dev           string // network device it was seen on (for wake-on-LAN)
	WifiIface     string // set for Wi-Fi clients
	Signal        int
	RxBytes       int64
	TxBytes       int64
	Connected     int // seconds, Wi-Fi only
	Online        bool
	Idle          bool // wired, in the neighbour table but not recently confirmed
	Static        bool // has a reserved address
	Named         bool // has a name in a host section
	HostSec       string
	Blocked       bool
	BlockSec      string
	Route         string // pbr interface this client is forced through, if any
	RouteSec      string
}

// Merge combines the raw data with the configuration.
func Merge(raw Raw, cfgs []uci.Config) []Client {
	by := map[string]*Client{}
	get := func(mac string) *Client {
		if c := by[mac]; c != nil {
			return c
		}
		c := &Client{MAC: mac}
		by[mac] = c
		return c
	}
	for _, l := range raw.Leases {
		c := get(l.MAC)
		c.IP, c.Name = l.IP, l.Name
	}
	for _, n := range raw.Neigh {
		switch n.State {
		case "FAILED", "INCOMPLETE":
			continue
		}
		c := get(n.MAC)
		if c.IP == "" {
			c.IP = n.IP
		}
		c.Dev = n.Dev
		c.Online = n.State == "REACHABLE" || n.State == "DELAY" || n.State == "PROBE"
		c.Idle = n.State == "STALE"
	}
	for _, s := range raw.Stations {
		c := get(s.MAC)
		c.WifiIface, c.Signal, c.RxBytes, c.TxBytes, c.Connected = s.Iface, s.Signal, s.RxBytes, s.TxBytes, s.ConnectedSecs
		c.Online, c.Idle = true, false
	}
	for _, cfg := range cfgs {
		for _, s := range cfg.Sections {
			switch {
			case cfg.Name == "dhcp" && s.Type == "host":
				o, _ := s.Option("mac")
				for _, m := range o.Values {
					for _, mac := range strings.Fields(lower(m)) {
						if !macRe.MatchString(mac) {
							continue
						}
						c := get(mac)
						c.HostSec = s.Ref()
						if n, ok := s.Option("name"); ok && n.Value() != "" {
							c.Name, c.Named = n.Value(), true
						}
						if ip, ok := s.Option("ip"); ok && ip.Value() != "" {
							c.Static = true
							if c.IP == "" {
								c.IP = ip.Value()
							}
						}
					}
				}
			case cfg.Name == "firewall" && s.Type == "rule" && opt(s, "target") != "ACCEPT" && opt(s, "enabled") != "0":
				if mac := lower(opt(s, "src_mac")); macRe.MatchString(mac) && strings.HasPrefix(s.Name, blockPrefix) {
					c := get(mac)
					c.Blocked, c.BlockSec = true, s.Ref()
				}
			}
		}
	}
	for _, cfg := range cfgs {
		if cfg.Name != "pbr" {
			continue
		}
		for _, s := range cfg.Sections {
			if s.Type != "policy" || opt(s, "enabled") == "0" {
				continue
			}
			for _, a := range strings.Fields(strings.ReplaceAll(opt(s, "src_addr"), ",", " ")) {
				for _, c := range by {
					if lower(a) == c.MAC || (c.IP != "" && a == c.IP) {
						c.Route, c.RouteSec = opt(s, "interface"), s.Ref()
					}
				}
			}
		}
	}
	out := make([]*Client, 0, len(by))
	for _, c := range by {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Online != b.Online {
			return a.Online
		}
		return ipKey(a.IP) < ipKey(b.IP)
	})
	res := make([]Client, len(out))
	for i, c := range out {
		res[i] = *c
	}
	return res
}

func opt(s uci.Section, k string) string { o, _ := s.Option(k); return o.Value() }

func ipKey(ip string) string {
	p := strings.Split(ip, ".")
	for i := range p {
		p[i] = fmt.Sprintf("%03s", p[i])
	}
	return strings.Join(p, ".")
}

// Display is the name to show: the given name, else the MAC.
func (c Client) Display() string {
	if c.Name != "" {
		return c.Name
	}
	return "(unnamed)"
}

// ---- actions, as staged changes ----

const (
	blockPrefix = "owrt_block_"
	routePrefix = "owrt_route_"
)

func macID(mac string) string { return strings.ReplaceAll(lower(mac), ":", "") }

var nameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,61}[A-Za-z0-9])?$`)

// Rename names the client. A host section is created if there is none.
func Rename(c Client, name string) ([]changes.Change, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("a name may use letters, digits, '.', '-' and '_' (no spaces)")
	}
	if c.HostSec != "" {
		return []changes.Change{{Op: changes.OpSet, Config: "dhcp", Section: c.HostSec, Option: "name", Value: name, Old: c.Name}}, nil
	}
	sec := "lease_" + macID(c.MAC)
	return []changes.Change{
		{Op: changes.OpAddSect, Config: "dhcp", Section: sec, Type: "host"},
		{Op: changes.OpSet, Config: "dhcp", Section: sec, Option: "mac", Value: c.MAC},
		{Op: changes.OpSet, Config: "dhcp", Section: sec, Option: "name", Value: name},
	}, nil
}

// MakeStatic reserves the client's current address.
func MakeStatic(c Client) ([]changes.Change, error) {
	if c.IP == "" {
		return nil, fmt.Errorf("this client has no address yet")
	}
	if c.Static {
		return nil, fmt.Errorf("already has a reserved address")
	}
	if c.HostSec != "" {
		return []changes.Change{{Op: changes.OpSet, Config: "dhcp", Section: c.HostSec, Option: "ip", Value: c.IP}}, nil
	}
	sec := "lease_" + macID(c.MAC)
	ch := []changes.Change{
		{Op: changes.OpAddSect, Config: "dhcp", Section: sec, Type: "host"},
		{Op: changes.OpSet, Config: "dhcp", Section: sec, Option: "mac", Value: c.MAC},
		{Op: changes.OpSet, Config: "dhcp", Section: sec, Option: "ip", Value: c.IP},
	}
	if c.Name != "" && nameRe.MatchString(c.Name) {
		ch = append(ch, changes.Change{Op: changes.OpSet, Config: "dhcp", Section: sec, Option: "name", Value: c.Name})
	}
	return ch, nil
}

// SetBlocked adds or removes a rule that stops the client's traffic being
// forwarded. The device can still reach the router itself.
func SetBlocked(c Client, on bool) []changes.Change {
	if !on {
		if c.BlockSec == "" {
			return nil
		}
		return []changes.Change{{Op: changes.OpDelete, Config: "firewall", Section: c.BlockSec, Old: "block " + c.MAC}}
	}
	sec := blockPrefix + macID(c.MAC)
	set := func(k, v string) changes.Change {
		return changes.Change{Op: changes.OpSet, Config: "firewall", Section: sec, Option: k, Value: v}
	}
	return []changes.Change{
		{Op: changes.OpAddSect, Config: "firewall", Section: sec, Type: "rule"},
		set("name", "Block "+c.MAC), set("src", "*"), set("dest", "*"), set("src_mac", c.MAC), set("target", "REJECT"),
	}
}

// SetRoute forces the client through an interface using pbr; iface "" removes
// the policy. Only possible when the pbr config exists (caller checks).
func SetRoute(c Client, iface string) ([]changes.Change, error) {
	if iface == "" {
		if c.RouteSec == "" {
			return nil, nil
		}
		return []changes.Change{{Op: changes.OpDelete, Config: "pbr", Section: c.RouteSec, Old: "route " + c.MAC}}, nil
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`).MatchString(iface) {
		return nil, fmt.Errorf("invalid interface %q", iface)
	}
	if c.RouteSec != "" {
		return []changes.Change{{Op: changes.OpSet, Config: "pbr", Section: c.RouteSec, Option: "interface", Value: iface, Old: c.Route}}, nil
	}
	if c.IP == "" {
		return nil, fmt.Errorf("this client has no address yet")
	}
	sec := routePrefix + macID(c.MAC)
	return []changes.Change{
		{Op: changes.OpAddSect, Config: "pbr", Section: sec, Type: "policy"},
		{Op: changes.OpSet, Config: "pbr", Section: sec, Option: "name", Value: "owrt-" + macID(c.MAC)},
		{Op: changes.OpSet, Config: "pbr", Section: sec, Option: "src_addr", Value: c.IP},
		{Op: changes.OpSet, Config: "pbr", Section: sec, Option: "interface", Value: iface},
		{Op: changes.OpSet, Config: "pbr", Section: sec, Option: "enabled", Value: "1"},
	}, nil
}

// RouteChoices lists interfaces a client can be routed through: wan-like and
// WireGuard interfaces from the network config.
func RouteChoices(cfgs []uci.Config) []string {
	var out []string
	for _, c := range cfgs {
		if c.Name != "network" {
			continue
		}
		for _, s := range c.Sections {
			if s.Type != "interface" || s.Name == "loopback" || s.Name == "lan" {
				continue
			}
			p := opt(s, "proto")
			if s.Name == "wan" || s.Name == "wwan" || p == "wireguard" || p == "pppoe" {
				out = append(out, s.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// HasPBR reports whether a pbr config exists.
func HasPBR(cfgs []uci.Config) bool {
	for _, c := range cfgs {
		if c.Name == "pbr" {
			return true
		}
	}
	return false
}
