// Package preflight looks at what the configuration will be after the staged
// changes and reports problems the change would introduce: ways to lose the
// session, conflicting addresses and ports, and settings that stop a service
// from starting. It never talks to the router.
package preflight

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/uci"
)

// Severity orders findings.
type Severity int

const (
	Info Severity = iota
	Warn
	Danger
)

func (s Severity) String() string { return [...]string{"note", "warning", "danger"}[s] }

// Finding is one problem.
type Finding struct {
	Sev  Severity
	Rule string
	Msg  string
	// Lockout marks findings that can cut the session; the UI then requires
	// the rollback timer.
	Lockout bool
}

// Context describes the session.
type Context struct {
	// Host is what the session connects to: an address or a name.
	Host string
}

// Check returns the findings that exist after the change but not before it,
// most severe first.
func Check(before, after []uci.Config, ctx Context) []Finding {
	had := map[string]bool{}
	for _, f := range run(before, before, ctx) {
		had[f.Rule+"|"+f.Msg] = true
	}
	var out []Finding
	for _, f := range run(before, after, ctx) {
		if !had[f.Rule+"|"+f.Msg] {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sev > out[j].Sev })
	return out
}

// NeedsRollback reports whether any finding can cut the session.
func NeedsRollback(fs []Finding) bool {
	for _, f := range fs {
		if f.Lockout {
			return true
		}
	}
	return false
}

func run(before, after []uci.Config, ctx Context) []Finding {
	var out []Finding
	out = append(out, lockout(before, after, ctx)...)
	out = append(out, overlaps(after)...)
	out = append(out, dhcpChecks(after)...)
	out = append(out, ports(after)...)
	out = append(out, wifiKeys(after)...)
	out = append(out, zones(after)...)
	out = append(out, extra(before, after, ctx)...)
	return out
}

// ---- helpers ----

func cfg(cs []uci.Config, name string) uci.Config {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return uci.Config{}
}

func opt(s uci.Section, k string) string {
	o, _ := s.Option(k)
	return o.Value()
}

func isIP(s string) bool { _, err := netip.ParseAddr(s); return err == nil }

// prefix returns an interface's IPv4 network and the router's address in it.
func prefix(s uci.Section) (netip.Prefix, netip.Addr, bool) {
	a := strings.Fields(opt(s, "ipaddr"))
	if len(a) == 0 {
		return netip.Prefix{}, netip.Addr{}, false
	}
	addr := a[0]
	if strings.Contains(addr, "/") {
		p, err := netip.ParsePrefix(addr)
		if err != nil {
			return netip.Prefix{}, netip.Addr{}, false
		}
		return p.Masked(), p.Addr(), true
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil || !ip.Is4() {
		return netip.Prefix{}, netip.Addr{}, false
	}
	bits := 24
	if m := opt(s, "netmask"); m != "" {
		if mb, ok := maskBits(m); ok {
			bits = mb
		}
	}
	p, err := ip.Prefix(bits)
	if err != nil {
		return netip.Prefix{}, netip.Addr{}, false
	}
	return p, ip, true
}

func maskBits(m string) (int, bool) {
	parts := strings.Split(m, ".")
	if len(parts) != 4 {
		return 0, false
	}
	ones, seenZero := 0, false
	for _, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 255 {
			return 0, false
		}
		for i := 7; i >= 0; i-- {
			if v>>uint(i)&1 == 1 {
				if seenZero {
					return 0, false
				}
				ones++
			} else {
				seenZero = true
			}
		}
	}
	return ones, true
}

// ---- rules ----

// isMgmt reports whether an interface is probably the one the session uses.
func isMgmt(s uci.Section, ctx Context) bool {
	if _, addr, ok := prefix(s); ok && isIP(ctx.Host) {
		return addr.String() == ctx.Host
	}
	// The host is a name (router.lan): assume the usual management network.
	return !isIP(ctx.Host) && s.Name == "lan"
}

func lockout(before, after []uci.Config, ctx Context) []Finding {
	var out []Finding
	bn, an := cfg(before, "network"), cfg(after, "network")
	for _, b := range bn.Sections {
		if b.Type != "interface" || !isMgmt(b, ctx) {
			continue
		}
		a, ok := an.Section(b.Ref())
		if !ok {
			out = append(out, Finding{Danger, "lockout-iface", fmt.Sprintf("deletes interface %q, which this session probably uses", b.Name), true})
			continue
		}
		for _, k := range []string{"proto", "ipaddr", "netmask", "device", "disabled"} {
			if opt(b, k) != opt(a, k) {
				out = append(out, Finding{Danger, "lockout-iface", fmt.Sprintf("changes %s of interface %q (%s -> %s); this session probably uses it", k, b.Name, orNone(opt(b, k)), orNone(opt(a, k))), true})
			}
		}
	}
	bd, ad := cfg(before, "dropbear"), cfg(after, "dropbear")
	for _, b := range bd.Sections {
		a, ok := ad.Section(b.Ref())
		if !ok {
			out = append(out, Finding{Danger, "lockout-ssh", "removes the SSH server configuration", true})
			continue
		}
		for _, k := range []string{"Port", "enable", "Interface"} {
			if opt(b, k) != opt(a, k) {
				out = append(out, Finding{Danger, "lockout-ssh", fmt.Sprintf("changes SSH %s (%s -> %s); reconnecting may need the new value", k, orNone(opt(b, k)), orNone(opt(a, k))), true})
			}
		}
	}
	// the firewall zone that holds the management interface must keep accepting input
	bz, az := cfg(before, "firewall"), cfg(after, "firewall")
	for _, b := range bn.Sections {
		if b.Type != "interface" || !isMgmt(b, ctx) {
			continue
		}
		bzone, bok := zoneOf(bz, b.Name)
		azone, aok := zoneOf(az, b.Name)
		if bok && !aok {
			out = append(out, Finding{Danger, "lockout-zone", fmt.Sprintf("removes %q from its firewall zone", b.Name), true})
		} else if bok && aok && opt(bzone, "input") != opt(azone, "input") && opt(azone, "input") != "ACCEPT" {
			out = append(out, Finding{Danger, "lockout-zone", fmt.Sprintf("zone %q would stop accepting input (%s); SSH and the web UI would be blocked", opt(azone, "name"), orNone(opt(azone, "input"))), true})
		}
	}
	return out
}

func zoneOf(fw uci.Config, iface string) (uci.Section, bool) {
	for _, z := range fw.Sections {
		if z.Type != "zone" {
			continue
		}
		o, _ := z.Option("network")
		for _, n := range o.Values {
			for _, f := range strings.Fields(n) {
				if f == iface {
					return z, true
				}
			}
		}
	}
	return uci.Section{}, false
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func overlaps(after []uci.Config) []Finding {
	var out []Finding
	type ifp struct {
		name string
		p    netip.Prefix
	}
	var all []ifp
	for _, s := range cfg(after, "network").Sections {
		if s.Type != "interface" || s.Name == "loopback" || opt(s, "disabled") == "1" {
			continue
		}
		if p, _, ok := prefix(s); ok {
			all = append(all, ifp{s.Name, p})
		}
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i].p.Overlaps(all[j].p) {
				out = append(out, Finding{Warn, "subnet-overlap", fmt.Sprintf("interfaces %q and %q use overlapping networks (%s, %s); routing between them breaks", all[i].name, all[j].name, all[i].p, all[j].p), false})
			}
		}
	}
	return out
}

func dhcpChecks(after []uci.Config) []Finding {
	var out []Finding
	nets := map[string]netip.Prefix{} // interface -> network
	for _, s := range cfg(after, "network").Sections {
		if p, _, ok := prefix(s); ok && s.Type == "interface" && s.Name != "loopback" {
			nets[s.Name] = p
		}
	}
	d := cfg(after, "dhcp")
	for _, s := range d.Sections {
		if s.Type != "dhcp" || opt(s, "ignore") == "1" {
			continue
		}
		p, ok := nets[opt(s, "interface")]
		if !ok {
			continue
		}
		start, e1 := strconv.Atoi(opt(s, "start"))
		limit, e2 := strconv.Atoi(opt(s, "limit"))
		if e1 == nil && e2 == nil && p.Bits() <= 30 {
			hosts := 1<<uint(32-p.Bits()) - 2
			if start+limit-1 > hosts {
				out = append(out, Finding{Warn, "dhcp-pool", fmt.Sprintf("the DHCP pool on %q (start %d, %d addresses) runs past the end of %s", opt(s, "interface"), start, limit, p), false})
			}
		}
	}
	seenIP, seenMAC := map[string]bool{}, map[string]bool{}
	for _, s := range d.Sections {
		if s.Type != "host" {
			continue
		}
		ip := opt(s, "ip")
		if ip != "" {
			if seenIP[ip] {
				out = append(out, Finding{Warn, "lease-dup-ip", fmt.Sprintf("address %s is reserved for more than one device", ip), false})
			}
			seenIP[ip] = true
			if a, err := netip.ParseAddr(ip); err == nil {
				in := false
				for _, p := range nets {
					if p.Contains(a) {
						in = true
					}
				}
				if len(nets) > 0 && !in {
					out = append(out, Finding{Warn, "lease-off-net", fmt.Sprintf("reserved address %s is not inside any of the router's networks", ip), false})
				}
			}
		}
		for _, m := range strings.Fields(strings.ToLower(opt(s, "mac"))) {
			if seenMAC[m] {
				out = append(out, Finding{Warn, "lease-dup-mac", fmt.Sprintf("MAC %s has more than one reservation", m), false})
			}
			seenMAC[m] = true
		}
	}
	return out
}

func ports(after []uci.Config) []Finding {
	var out []Finding
	ssh := ""
	for _, s := range cfg(after, "dropbear").Sections {
		if p := opt(s, "Port"); p != "" && opt(s, "enable") != "0" {
			ssh = p
		}
	}
	if ssh == "" {
		return nil
	}
	for _, s := range cfg(after, "uhttpd").Sections {
		for _, k := range []string{"listen_http", "listen_https"} {
			o, _ := s.Option(k)
			for _, v := range o.Values {
				for _, l := range strings.Fields(v) {
					if i := strings.LastIndex(l, ":"); i >= 0 && l[i+1:] == ssh {
						out = append(out, Finding{Warn, "port-conflict", fmt.Sprintf("SSH and the web server both listen on port %s", ssh), false})
					}
				}
			}
		}
	}
	return out
}

func wifiKeys(after []uci.Config) []Finding {
	var out []Finding
	for _, s := range cfg(after, "wireless").Sections {
		if s.Type != "wifi-iface" || opt(s, "mode") == "sta" || opt(s, "disabled") == "1" {
			continue
		}
		enc := opt(s, "encryption")
		if enc == "" || enc == "none" || strings.HasPrefix(enc, "owe") || strings.Contains(enc, "wpa") && strings.Contains(enc, "eap") {
			continue
		}
		if opt(s, "key") == "" && opt(s, "sae_password") == "" && opt(s, "ppsk") == "" {
			out = append(out, Finding{Danger, "wifi-no-key", fmt.Sprintf("network %q uses %s but has no password; the radio will not start it", opt(s, "ssid"), enc), false})
		}
	}
	return out
}

func zones(after []uci.Config) []Finding {
	var out []Finding
	where := map[string][]string{}
	for _, z := range cfg(after, "firewall").Sections {
		if z.Type != "zone" {
			continue
		}
		o, _ := z.Option("network")
		seen := map[string]bool{}
		for _, n := range o.Values {
			for _, f := range strings.Fields(n) {
				if !seen[f] {
					seen[f] = true
					where[f] = append(where[f], opt(z, "name"))
				}
			}
		}
	}
	var names []string
	for n := range where {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if len(where[n]) > 1 {
			out = append(out, Finding{Warn, "multi-zone", fmt.Sprintf("interface %q is in more than one firewall zone (%s)", n, strings.Join(where[n], ", ")), false})
		}
	}
	return out
}
