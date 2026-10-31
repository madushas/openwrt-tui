// Package tasks builds the staged changes for common jobs (a guest network, a
// port forward, upstream DNS) from a few answers. Each builder checks the
// answers against the current configuration and returns ordinary changes, so
// the result goes through preflight, rollback and smart confirm. Nothing here
// talks to the router.
package tasks

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

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

func has(cfgs []uci.Config, cfg, sec string) bool {
	c, ok := find(cfgs, cfg)
	if !ok {
		return false
	}
	_, ok = c.Section(sec)
	return ok
}

func opt(s uci.Section, k string) string { o, _ := s.Option(k); return o.Value() }

// ---- guest network ----

// Guest describes an isolated Wi-Fi network.
type Guest struct {
	Name    string // interface and zone name, e.g. "guest"
	SSID    string
	Key     string // WPA passphrase
	Octet   int    // the network is 192.168.<Octet>.0/24
	Radio   string // wifi-device to host the SSID, e.g. "radio0"
	Isolate bool   // clients cannot talk to each other
}

var guestName = regexp.MustCompile(`^[a-z][a-z0-9]{0,10}$`)

// GuestNetwork creates the interface, DHCP, firewall zone and SSID. The zone
// refuses everything to the router except DHCP and DNS, and may only reach
// the internet, never the LAN.
func GuestNetwork(g Guest, cfgs []uci.Config) ([]changes.Change, error) {
	if !guestName.MatchString(g.Name) {
		return nil, fmt.Errorf("name: lowercase letters and digits, starting with a letter, at most 11")
	}
	if err := validate.SSID(g.SSID); err != nil {
		return nil, fmt.Errorf("SSID: %w", err)
	}
	if err := validate.WPAKey(g.Key); err != nil {
		return nil, fmt.Errorf("password: %w", err)
	}
	if g.Octet < 1 || g.Octet > 254 {
		return nil, fmt.Errorf("subnet: pick a number from 1 to 254")
	}
	if !has(cfgs, "wireless", g.Radio) {
		return nil, fmt.Errorf("radio %q does not exist", g.Radio)
	}
	for _, ref := range [][2]string{{"network", g.Name}, {"dhcp", g.Name}, {"firewall", g.Name}, {"wireless", g.Name + "_ap"}} {
		if has(cfgs, ref[0], ref[1]) {
			return nil, fmt.Errorf("%s.%s already exists; choose another name", ref[0], ref[1])
		}
	}
	prefix := fmt.Sprintf("192.168.%d.", g.Octet)
	if n, ok := find(cfgs, "network"); ok {
		for _, s := range n.Sections {
			if s.Type == "interface" && strings.HasPrefix(opt(s, "ipaddr"), prefix) {
				return nil, fmt.Errorf("192.168.%d.0/24 is already used by interface %s", g.Octet, s.Ref())
			}
		}
	}
	if fw, ok := find(cfgs, "firewall"); !ok || !hasZone(fw, "wan") {
		return nil, fmt.Errorf("there is no 'wan' firewall zone to give the guest network internet access")
	}
	var b builder
	n, d, z, w := g.Name, g.Name, g.Name, g.Name+"_ap"
	b.add("network", n, "interface")
	b.set("network", n, "proto", "static")
	b.set("network", n, "ipaddr", prefix+"1")
	b.set("network", n, "netmask", "255.255.255.0")

	b.add("dhcp", d, "dhcp")
	b.set("dhcp", d, "interface", n)
	b.set("dhcp", d, "start", "100")
	b.set("dhcp", d, "limit", "150")
	b.set("dhcp", d, "leasetime", "1h")

	b.add("firewall", z, "zone")
	b.set("firewall", z, "name", n)
	b.list("firewall", z, "network", n)
	b.set("firewall", z, "input", "REJECT")
	b.set("firewall", z, "output", "ACCEPT")
	b.set("firewall", z, "forward", "REJECT")
	b.add("firewall", n+"_wan", "forwarding")
	b.set("firewall", n+"_wan", "src", n)
	b.set("firewall", n+"_wan", "dest", "wan")
	b.add("firewall", n+"_dhcp", "rule")
	b.set("firewall", n+"_dhcp", "name", "Allow-DHCP-"+n)
	b.set("firewall", n+"_dhcp", "src", n)
	b.set("firewall", n+"_dhcp", "proto", "udp")
	b.set("firewall", n+"_dhcp", "dest_port", "67")
	b.set("firewall", n+"_dhcp", "target", "ACCEPT")
	b.add("firewall", n+"_dns", "rule")
	b.set("firewall", n+"_dns", "name", "Allow-DNS-"+n)
	b.set("firewall", n+"_dns", "src", n)
	b.set("firewall", n+"_dns", "proto", "tcp udp")
	b.set("firewall", n+"_dns", "dest_port", "53")
	b.set("firewall", n+"_dns", "target", "ACCEPT")

	b.add("wireless", w, "wifi-iface")
	b.set("wireless", w, "device", g.Radio)
	b.set("wireless", w, "mode", "ap")
	b.set("wireless", w, "network", n)
	b.set("wireless", w, "ssid", g.SSID)
	b.set("wireless", w, "encryption", "psk2")
	b.set("wireless", w, "key", g.Key)
	if g.Isolate {
		b.set("wireless", w, "isolate", "1")
	}
	return b.out, nil
}

func hasZone(fw uci.Config, name string) bool {
	for _, s := range fw.Sections {
		if s.Type == "zone" && opt(s, "name") == name {
			return true
		}
	}
	return false
}

// ---- port forward ----

// Forward is one port forward from the internet to an inside host.
type Forward struct {
	Name    string
	Proto   string // "tcp", "udp" or "tcp udp"
	ExtPort string
	IP      string
	IntPort string
}

var fwdName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

var sensitive = map[string]string{"22": "SSH", "23": "telnet", "80": "web", "443": "HTTPS", "3389": "remote desktop", "445": "file sharing"}

// PortForward builds a redirect. The second result is a list of warnings the
// user should read before applying.
func PortForward(f Forward, cfgs []uci.Config) ([]changes.Change, []string, error) {
	if !fwdName.MatchString(f.Name) {
		return nil, nil, fmt.Errorf("name: letters, digits, '-' and '_'")
	}
	if f.Proto != "tcp" && f.Proto != "udp" && f.Proto != "tcp udp" {
		return nil, nil, fmt.Errorf("protocol must be tcp, udp or tcp udp")
	}
	if err := validate.Port(f.ExtPort); err != nil {
		return nil, nil, fmt.Errorf("external port: %w", err)
	}
	if f.IntPort == "" {
		f.IntPort = f.ExtPort
	}
	if err := validate.Port(f.IntPort); err != nil {
		return nil, nil, fmt.Errorf("internal port: %w", err)
	}
	if err := validate.IPv4(f.IP); err != nil {
		return nil, nil, fmt.Errorf("address: %w", err)
	}
	sec := "fwd_" + strings.ReplaceAll(f.Name, "-", "_")
	if has(cfgs, "firewall", sec) {
		return nil, nil, fmt.Errorf("a forward named %q already exists", f.Name)
	}
	ext, _ := strconv.Atoi(f.ExtPort)
	if fw, ok := find(cfgs, "firewall"); ok {
		for _, s := range fw.Sections {
			if s.Type != "redirect" || opt(s, "src") != "wan" || opt(s, "enabled") == "0" {
				continue
			}
			if p, _ := strconv.Atoi(opt(s, "src_dport")); p == ext && sharesProto(opt(s, "proto"), f.Proto) {
				return nil, nil, fmt.Errorf("port %d is already forwarded by %s", ext, s.Ref())
			}
		}
	}
	var warn []string
	if what, bad := sensitive[f.ExtPort]; bad {
		warn = append(warn, fmt.Sprintf("port %s (%s) is constantly scanned from the internet; a VPN is safer", f.ExtPort, what))
	}
	var b builder
	b.add("firewall", sec, "redirect")
	b.set("firewall", sec, "name", f.Name)
	b.set("firewall", sec, "src", "wan")
	b.set("firewall", sec, "src_dport", f.ExtPort)
	b.set("firewall", sec, "dest", "lan")
	b.set("firewall", sec, "dest_ip", f.IP)
	b.set("firewall", sec, "dest_port", f.IntPort)
	b.set("firewall", sec, "proto", f.Proto)
	b.set("firewall", sec, "target", "DNAT")
	return b.out, warn, nil
}

func sharesProto(a, b string) bool {
	if a == "" {
		a = "tcp udp"
	}
	for _, x := range strings.Fields(a) {
		for _, y := range strings.Fields(b) {
			if x == y {
				return true
			}
		}
	}
	return false
}

// ---- upstream DNS ----

// DNSPresets are the choices offered, in display order.
var DNSPresets = []struct {
	Name    string
	Servers []string
}{
	{"Cloudflare", []string{"1.1.1.1", "1.0.0.1"}},
	{"Quad9 (blocks malware domains)", []string{"9.9.9.9", "149.112.112.112"}},
	{"Google", []string{"8.8.8.8", "8.8.4.4"}},
	{"AdGuard (blocks ads)", []string{"94.140.14.14", "94.140.15.15"}},
}

// CurrentDNS returns the upstream servers dnsmasq is configured with.
func CurrentDNS(cfgs []uci.Config) []string {
	if d, ok := find(cfgs, "dhcp"); ok {
		for _, s := range d.Sections {
			if s.Type == "dnsmasq" {
				o, _ := s.Option("server")
				return o.Values
			}
		}
	}
	return nil
}

// UpstreamDNS makes dnsmasq forward to the given servers and ignore the
// provider-supplied ones.
func UpstreamDNS(servers []string, cfgs []uci.Config) ([]changes.Change, []string, error) {
	if len(servers) == 0 {
		return nil, nil, fmt.Errorf("no servers chosen")
	}
	for _, s := range servers {
		if err := validate.IP(s); err != nil {
			return nil, nil, fmt.Errorf("%q: %w", s, err)
		}
	}
	d, ok := find(cfgs, "dhcp")
	if !ok {
		return nil, nil, fmt.Errorf("no dhcp config")
	}
	var sec uci.Section
	found := false
	for _, s := range d.Sections {
		if s.Type == "dnsmasq" {
			sec, found = s, true
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("no dnsmasq section")
	}
	cur := CurrentDNS(cfgs)
	if strings.Join(cur, " ") == strings.Join(servers, " ") && opt(sec, "noresolv") == "1" {
		return nil, nil, fmt.Errorf("DNS is already set to these servers")
	}
	var b builder
	if len(cur) > 0 {
		b.out = append(b.out, changes.Change{Op: changes.OpDelete, Config: "dhcp", Section: sec.Ref(), Option: "server", Old: strings.Join(cur, " ")})
	}
	for _, s := range servers {
		b.list("dhcp", sec.Ref(), "server", s)
	}
	b.set("dhcp", sec.Ref(), "noresolv", "1")
	var warn []string
	if _, ok := find(cfgs, "adblock"); ok {
		warn = append(warn, "adblock is installed: it filters through dnsmasq, so ads stay blocked, but check its own DNS settings")
	}
	if _, ok := find(cfgs, "https-dns-proxy"); ok {
		warn = append(warn, "https-dns-proxy is configured and may already manage upstream DNS")
	}
	return b.out, warn, nil
}
