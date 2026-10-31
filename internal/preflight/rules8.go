package preflight

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/madushakv/owrt-tui/internal/uci"
)

// Rules for the advanced editors: bridges, VLANs, routes and the firewall's
// references. They look at the configuration after the change, so a problem
// that was already there is not blamed on the change.

func ifaceNames(nc uci.Config) map[string]bool {
	m := map[string]bool{"loopback": true}
	for _, s := range nc.Sections {
		if s.Type == "interface" {
			m[s.Name] = true
		}
	}
	return m
}

func zoneNames(fw uci.Config) map[string]int {
	m := map[string]int{}
	for _, s := range fw.Sections {
		if s.Type == "zone" {
			m[opt(s, "name")]++
		}
	}
	return m
}

func fields(s uci.Section, k string) []string {
	o, _ := s.Option(k)
	var out []string
	for _, v := range o.Values {
		out = append(out, strings.Fields(v)...)
	}
	return out
}

// firewallRefs finds dangling and duplicate firewall references.
func firewallRefs(after []uci.Config) []Finding {
	fw, nw := cfg(after, "firewall"), cfg(after, "network")
	if len(fw.Sections) == 0 {
		return nil
	}
	var out []Finding
	zones, ifaces := zoneNames(fw), ifaceNames(nw)
	var names []string
	for n := range zones {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if zones[n] > 1 {
			out = append(out, Finding{Danger, "zone-dup", fmt.Sprintf("two firewall zones are named %q; the firewall will not load", n), false})
		}
	}
	if len(nw.Sections) > 0 {
		for _, z := range fw.Sections {
			if z.Type != "zone" {
				continue
			}
			for _, n := range fields(z, "network") {
				if !ifaces[n] {
					out = append(out, Finding{Warn, "zone-iface", fmt.Sprintf("zone %q lists interface %q, which does not exist", opt(z, "name"), n), false})
				}
			}
		}
	}
	ref := func(what, kind, z string) {
		if z != "" && z != "*" && zones[z] == 0 {
			out = append(out, Finding{Danger, "zone-ref", fmt.Sprintf("%s refers to zone %q, which does not exist; the firewall would refuse to load", what, z), false})
		}
	}
	for _, s := range fw.Sections {
		switch s.Type {
		case "forwarding":
			what := fmt.Sprintf("forwarding %s", s.Ref())
			ref(what, "src", opt(s, "src"))
			ref(what, "dest", opt(s, "dest"))
			if opt(s, "src") == "" || opt(s, "dest") == "" {
				out = append(out, Finding{Danger, "zone-ref", what + " needs both a source and a destination zone", false})
			}
		case "rule", "redirect":
			what := fmt.Sprintf("%s %q", s.Type, orNone(opt(s, "name")))
			ref(what, "src", opt(s, "src"))
			ref(what, "dest", opt(s, "dest"))
		}
	}
	return out
}

// bridgeChecks looks at device sections (bridges) and bridge-vlans.
func bridgeChecks(after []uci.Config) []Finding {
	nw := cfg(after, "network")
	var out []Finding
	owner := map[string]string{}
	bridges := map[string]bool{}
	for _, s := range nw.Sections {
		if s.Type != "device" || opt(s, "type") != "bridge" {
			continue
		}
		name := opt(s, "name")
		bridges[name] = true
		for _, p := range fields(s, "ports") {
			if o, dup := owner[p]; dup && o != name {
				out = append(out, Finding{Danger, "bridge-port", fmt.Sprintf("port %q is in both %s and %s; a port can belong to only one bridge", p, o, name), false})
			}
			owner[p] = name
		}
	}
	for _, s := range nw.Sections {
		if s.Type == "interface" {
			if d := opt(s, "device"); strings.HasPrefix(d, "br-") && !strings.Contains(d, ".") && !bridges[d] && len(nw.Sections) > 0 {
				out = append(out, Finding{Warn, "bridge-missing", fmt.Sprintf("interface %q uses device %q, but no bridge with that name is defined (it works only if the system creates it)", s.Name, d), false})
			}
		}
	}
	seen := map[string]bool{}
	for _, s := range nw.Sections {
		if s.Type != "bridge-vlan" {
			continue
		}
		k := opt(s, "device") + "/" + opt(s, "vlan")
		if seen[k] {
			out = append(out, Finding{Danger, "vlan-dup", fmt.Sprintf("VLAN %s is defined twice on %s", opt(s, "vlan"), opt(s, "device")), false})
		}
		seen[k] = true
		if d := opt(s, "device"); d != "" && !bridges[d] {
			out = append(out, Finding{Warn, "vlan-bridge", fmt.Sprintf("VLAN %s is on %q, which is not a defined bridge", opt(s, "vlan"), d), false})
		}
	}
	return out
}

// bridgeLockout: changing the ports or VLANs of the bridge that carries the
// management interface can cut the session.
func bridgeLockout(before, after []uci.Config, ctx Context) []Finding {
	bn, an := cfg(before, "network"), cfg(after, "network")
	var out []Finding
	for _, mi := range bn.Sections {
		if mi.Type != "interface" || !isMgmt(mi, ctx) {
			continue
		}
		dev := strings.SplitN(opt(mi, "device"), ".", 2)[0]
		if dev == "" {
			continue
		}
		sig := func(nc uci.Config) string {
			var parts []string
			for _, s := range nc.Sections {
				switch {
				case s.Type == "device" && opt(s, "name") == dev:
					p := fields(s, "ports")
					sort.Strings(p)
					parts = append(parts, "ports:"+strings.Join(p, ","), "vlanfilter:"+opt(s, "vlan_filtering"))
				case s.Type == "bridge-vlan" && opt(s, "device") == dev:
					p := fields(s, "ports")
					sort.Strings(p)
					parts = append(parts, "vlan"+opt(s, "vlan")+":"+strings.Join(p, ","))
				}
			}
			sort.Strings(parts)
			return strings.Join(parts, ";")
		}
		if sig(bn) != sig(an) {
			out = append(out, Finding{Danger, "lockout-bridge", fmt.Sprintf("changes the ports or VLANs of %s, which carries interface %q that this session probably uses", dev, mi.Name), true})
		}
	}
	return out
}

func isDefault(t string) bool {
	return t == "0.0.0.0/0" || t == "default" || t == "0.0.0.0" || t == "::/0"
}

// routeChecks looks at static routes.
func routeChecks(after []uci.Config) []Finding {
	nw := cfg(after, "network")
	var out []Finding
	ifaces := ifaceNames(nw)
	type key struct{ t, g, i string }
	seen := map[key]bool{}
	for _, s := range nw.Sections {
		if s.Type != "route" && s.Type != "route6" {
			continue
		}
		target := opt(s, "target")
		if m := opt(s, "netmask"); m != "" && !strings.Contains(target, "/") {
			if b, ok := maskBits(m); ok {
				target = fmt.Sprintf("%s/%d", target, b)
			}
		}
		iface, gw := opt(s, "interface"), opt(s, "gateway")
		k := key{target, gw, iface}
		if seen[k] {
			out = append(out, Finding{Warn, "route-dup", fmt.Sprintf("route to %s via %s is defined twice", target, orNone(gw)), false})
		}
		seen[k] = true
		if iface != "" && !ifaces[iface] {
			out = append(out, Finding{Warn, "route-iface", fmt.Sprintf("route to %s uses interface %q, which does not exist", target, iface), false})
		}
		if gw != "" && s.Type == "route" && iface != "" {
			if is, ok := nw.Section(iface); ok {
				if p, _, ok := prefix(is); ok {
					if a, err := netip.ParseAddr(gw); err == nil && !p.Contains(a) {
						out = append(out, Finding{Warn, "route-gateway", fmt.Sprintf("gateway %s of the route to %s is not inside %s (%s); the route will not install", gw, target, iface, p), false})
					}
				}
			}
		}
		if isDefault(target) {
			out = append(out, Finding{Warn, "route-default", "a static default route changes where all internet traffic goes; if the gateway is wrong the router loses the internet", true})
		}
	}
	return out
}

// ruleLockout: a rule that rejects everything from the management zone to the router.
func ruleLockout(before, after []uci.Config, ctx Context) []Finding {
	fw, nw := cfg(after, "firewall"), cfg(after, "network")
	var out []Finding
	for _, mi := range nw.Sections {
		if mi.Type != "interface" || !isMgmt(mi, ctx) {
			continue
		}
		z, ok := zoneOf(fw, mi.Name)
		if !ok {
			continue
		}
		zn := opt(z, "name")
		for _, r := range fw.Sections {
			if r.Type != "rule" || opt(r, "src") != zn || opt(r, "dest") != "" || opt(r, "enabled") == "0" {
				continue
			}
			if t := opt(r, "target"); t != "REJECT" && t != "DROP" {
				continue
			}
			if opt(r, "src_ip") != "" || opt(r, "src_mac") != "" {
				continue
			}
			ports := fields(r, "dest_port")
			hitsSSH := len(ports) == 0
			for _, p := range ports {
				if p == "22" || p == "443" || p == "80" {
					hitsSSH = true
				}
			}
			if hitsSSH && (opt(r, "proto") == "" || strings.Contains(opt(r, "proto"), "tcp") || opt(r, "proto") == "all") {
				out = append(out, Finding{Danger, "lockout-rule", fmt.Sprintf("rule %q blocks traffic from zone %q to the router, which includes SSH and the web UI", orNone(opt(r, "name")), zn), true})
			}
		}
	}
	return out
}

func extra(before, after []uci.Config, ctx Context) []Finding {
	var out []Finding
	out = append(out, firewallRefs(after)...)
	out = append(out, bridgeChecks(after)...)
	out = append(out, bridgeLockout(before, after, ctx)...)
	out = append(out, routeChecks(after)...)
	out = append(out, ruleLockout(before, after, ctx)...)
	return out
}
