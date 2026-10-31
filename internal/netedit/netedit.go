// Package netedit builds the staged changes for creating and deleting network
// interfaces, including the clean-up that keeps the rest of the configuration
// consistent (firewall zones, DHCP pools, routes). Nothing here talks to the
// router; cross-checks of the result are left to the preflight rules.
package netedit

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

// Interface describes a new logical interface.
type Interface struct {
	Name     string
	Proto    string // static, dhcp, dhcpv6, pppoe, none
	Device   string // bridge or port, e.g. br-lan, wan, br-lan.10
	Address  string // static: CIDR such as 192.168.20.1/24
	Gateway  string // static, optional
	DNS      []string
	Username string // pppoe
	Password string // pppoe
	Zone     string // firewall zone to put it in, optional
	DHCP     bool   // static only: also serve DHCP on it
}

var (
	ifName  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,14}$`)
	devName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)
)

// Protos lists the protocols this editor can create.
var Protos = []string{"static", "dhcp", "dhcpv6", "pppoe", "none"}

type builder struct{ out []changes.Change }

func (b *builder) add(cfg, sec, typ string) {
	b.out = append(b.out, changes.Change{Op: changes.OpAddSect, Config: cfg, Section: sec, Type: typ})
}
func (b *builder) set(cfg, sec, k, v string) {
	b.out = append(b.out, changes.Change{Op: changes.OpSet, Config: cfg, Section: sec, Option: k, Value: v})
}
func (b *builder) list(cfg, sec, k, v string) {
	b.out = append(b.out, changes.Change{Op: changes.OpAddList, Config: cfg, Section: sec, Option: k, Value: v})
}

func find(cfgs []uci.Config, name string) (uci.Config, bool) {
	for _, c := range cfgs {
		if c.Name == name {
			return c, true
		}
	}
	return uci.Config{}, false
}

func opt(s uci.Section, k string) string { o, _ := s.Option(k); return o.Value() }

func fields(s uci.Section, k string) []string {
	o, _ := s.Option(k)
	var out []string
	for _, v := range o.Values {
		out = append(out, strings.Fields(v)...)
	}
	return out
}

// usedPrefixes lists the IPv4 ranges interfaces already use, with their owner.
func usedPrefixes(nw uci.Config) map[netip.Prefix]string {
	out := map[netip.Prefix]string{}
	for _, s := range nw.Sections {
		if s.Type != "interface" {
			continue
		}
		for _, a := range fields(s, "ipaddr") {
			var p netip.Prefix
			if strings.Contains(a, "/") {
				pp, err := netip.ParsePrefix(a)
				if err != nil {
					continue
				}
				p = pp.Masked()
			} else if ip, err := netip.ParseAddr(a); err == nil {
				bits := 24
				if m, err := netip.ParseAddr(opt(s, "netmask")); err == nil {
					bits = 0
					for _, by := range m.AsSlice() {
						for ; by&0x80 != 0; by <<= 1 {
							bits++
						}
					}
				}
				pp, err := ip.Prefix(bits)
				if err != nil {
					continue
				}
				p = pp
			} else {
				continue
			}
			out[p] = s.Name
		}
	}
	return out
}

// NewInterface builds an interface and, if asked, its firewall zone entry and
// DHCP pool.
func NewInterface(p Interface, cfgs []uci.Config) ([]changes.Change, error) {
	if !ifName.MatchString(p.Name) {
		return nil, fmt.Errorf("name: lowercase letters, digits and '_', starting with a letter, at most 15")
	}
	if p.Name == "loopback" {
		return nil, fmt.Errorf("loopback is reserved")
	}
	nw, ok := find(cfgs, "network")
	if !ok {
		return nil, fmt.Errorf("no network config")
	}
	if _, exists := nw.Section(p.Name); exists {
		return nil, fmt.Errorf("an interface or section named %q already exists", p.Name)
	}
	valid := false
	for _, x := range Protos {
		if x == p.Proto {
			valid = true
		}
	}
	if !valid {
		return nil, fmt.Errorf("protocol must be one of %s", strings.Join(Protos, ", "))
	}
	if p.Device != "" && !devName.MatchString(p.Device) {
		return nil, fmt.Errorf("device: letters, digits, '.', '-' and '_' (e.g. br-lan, wan, br-lan.10)")
	}
	for _, d := range p.DNS {
		if err := validate.IP(d); err != nil {
			return nil, fmt.Errorf("DNS %q: %w", d, err)
		}
	}
	var b builder
	n := p.Name
	b.add("network", n, "interface")
	b.set("network", n, "proto", p.Proto)
	if p.Device != "" {
		b.set("network", n, "device", p.Device)
	}
	switch p.Proto {
	case "static":
		pf, err := netip.ParsePrefix(p.Address)
		if err != nil || !pf.Addr().Is4() || pf.Bits() < 8 || pf.Bits() > 30 {
			return nil, fmt.Errorf("address: use CIDR such as 192.168.20.1/24")
		}
		for u, owner := range usedPrefixes(nw) {
			if u.Overlaps(pf.Masked()) {
				return nil, fmt.Errorf("%s overlaps %s used by interface %s", pf.Masked(), u, owner)
			}
		}
		b.set("network", n, "ipaddr", p.Address)
		if p.Gateway != "" {
			g, err := netip.ParseAddr(p.Gateway)
			if err != nil || !pf.Contains(g) {
				return nil, fmt.Errorf("gateway must be an address inside %s", pf.Masked())
			}
			b.set("network", n, "gateway", p.Gateway)
		}
	case "pppoe":
		if p.Username == "" || p.Password == "" {
			return nil, fmt.Errorf("PPPoE needs a username and a password")
		}
		if strings.ContainsAny(p.Username+p.Password, "\n\r\x00") {
			return nil, fmt.Errorf("username and password may not contain line breaks")
		}
		b.set("network", n, "username", p.Username)
		b.set("network", n, "password", p.Password)
	}
	for _, d := range p.DNS {
		b.list("network", n, "dns", d)
	}
	if p.Zone != "" {
		fw, ok := find(cfgs, "firewall")
		if !ok {
			return nil, fmt.Errorf("no firewall config to put the interface in a zone")
		}
		found := false
		for _, s := range fw.Sections {
			if s.Type == "zone" && opt(s, "name") == p.Zone {
				b.list("firewall", s.Ref(), "network", n)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("firewall zone %q does not exist", p.Zone)
		}
	}
	if p.DHCP {
		if p.Proto != "static" {
			return nil, fmt.Errorf("a DHCP server needs a static interface")
		}
		b.add("dhcp", n, "dhcp")
		b.set("dhcp", n, "interface", n)
		b.set("dhcp", n, "start", "100")
		b.set("dhcp", n, "limit", "150")
		b.set("dhcp", n, "leasetime", "12h")
	}
	return b.out, nil
}

// DeleteInterface removes an interface and everything that points at it:
// its place in firewall zones, its DHCP pool and its static routes. The
// warnings list things it cannot fix itself.
func DeleteInterface(name string, cfgs []uci.Config) ([]changes.Change, []string, error) {
	if name == "loopback" {
		return nil, nil, fmt.Errorf("loopback cannot be deleted")
	}
	nw, ok := find(cfgs, "network")
	if !ok {
		return nil, nil, fmt.Errorf("no network config")
	}
	sec, exists := nw.Section(name)
	if !exists || sec.Type != "interface" {
		return nil, nil, fmt.Errorf("%q is not an interface", name)
	}
	var out []changes.Change
	var warn []string
	if fw, ok := find(cfgs, "firewall"); ok {
		for _, z := range fw.Sections {
			if z.Type != "zone" {
				continue
			}
			for _, v := range func() []string { o, _ := z.Option("network"); return o.Values }() {
				if v == name {
					out = append(out, changes.Change{Op: changes.OpDelList, Config: "firewall", Section: z.Ref(), Option: "network", Value: name})
				} else if strings.Contains(" "+v+" ", " "+name+" ") {
					warn = append(warn, fmt.Sprintf("zone %s lists %q inside a combined entry %q; edit it by hand", opt(z, "name"), name, v))
				}
			}
		}
	}
	if d, ok := find(cfgs, "dhcp"); ok {
		for _, s := range d.Sections {
			if s.Type == "dhcp" && opt(s, "interface") == name {
				out = append(out, changes.Change{Op: changes.OpDelete, Config: "dhcp", Section: s.Ref(), Old: "DHCP pool on " + name})
			}
		}
	}
	for _, s := range nw.Sections {
		if (s.Type == "route" || s.Type == "route6") && opt(s, "interface") == name {
			out = append(out, changes.Change{Op: changes.OpDelete, Config: "network", Section: s.Ref(), Old: "route " + opt(s, "target")})
		}
	}
	if w, ok := find(cfgs, "wireless"); ok {
		for _, s := range w.Sections {
			if s.Type == "wifi-iface" && opt(s, "network") == name {
				warn = append(warn, fmt.Sprintf("Wi-Fi network %q is attached to %s; change or delete it on the Wi-Fi screen", opt(s, "ssid"), name))
			}
		}
	}
	for _, s := range nw.Sections {
		if s.Type == "interface" && s.Name != name && opt(s, "device") != "" && strings.HasPrefix(opt(s, "device"), "@"+name) {
			warn = append(warn, fmt.Sprintf("interface %s is an alias of %s", s.Name, name))
		}
	}
	out = append(out, changes.Change{Op: changes.OpDelete, Config: "network", Section: name, Old: "interface " + name})
	return out, warn, nil
}
