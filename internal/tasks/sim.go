package tasks

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

// SIMProtos are the cellular protocols the wizard can configure.
var SIMProtos = []string{"qmi", "mbim", "ncm", "modemmanager", "3g"}

// What decides which traffic uses the SIM.
const (
	ViaDomains  = "domains"  // only the listed domains/IPs (pbr destination)
	ViaSources  = "sources"  // only the listed LAN devices (pbr source)
	ViaFailover = "failover" // nothing by default; the SIM backs up the main link
)

// SIMModes are the choices shown in the wizard.
var SIMModes = []string{ViaDomains, ViaSources, ViaFailover}

// SIM routes chosen destinations through a cellular modem while everything
// else keeps using the main internet connection.
type SIM struct {
	Name    string // interface name, e.g. "sim"
	Proto   string // one of SIMProtos
	Device  string // /dev/cdc-wdm0, /dev/ttyUSB0, or the ModemManager modem path
	APN     string
	User    string // optional
	Pass    string // optional
	PIN     string // optional
	Domains string // domains, IPs or CIDRs separated by spaces or commas
	Mode    string // ViaDomains (default), ViaSources or ViaFailover
}

var simName = regexp.MustCompile(`^[a-z][a-z0-9]{0,10}$`)
var apnRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,63}$`)
var devRe = regexp.MustCompile(`^[A-Za-z0-9/._:@-]{1,120}$`)
var pinRe = regexp.MustCompile(`^[0-9]{4,8}$`)

// SplitTargets splits and validates the destinations (lower-cased, unique).
func SplitTargets(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" || seen[f] {
			continue
		}
		if validate.IPv4(f) != nil && validate.CIDR4(f) != nil && validate.Hostname(f) != nil {
			return nil, fmt.Errorf("%q is not a domain name, IPv4 address or network", f)
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("give at least one domain")
	}
	if len(out) > 40 {
		return nil, fmt.Errorf("at most 40 destinations (got %d)", len(out))
	}
	return out, nil
}

// SIMRoute builds the cellular interface (or updates it when it exists), puts
// it in the wan firewall zone and adds a pbr policy for the destinations. The
// second result lists notes to read before applying.
func SIMRoute(s SIM, cfgs []uci.Config) ([]changes.Change, []string, error) {
	if !simName.MatchString(s.Name) {
		return nil, nil, fmt.Errorf("name: lowercase letters and digits, starting with a letter (max 11)")
	}
	okProto := false
	for _, p := range SIMProtos {
		okProto = okProto || p == s.Proto
	}
	if !okProto {
		return nil, nil, fmt.Errorf("unknown protocol %q", s.Proto)
	}
	if !devRe.MatchString(s.Device) {
		return nil, nil, fmt.Errorf("device: e.g. /dev/cdc-wdm0 or /dev/ttyUSB0")
	}
	if !apnRe.MatchString(s.APN) {
		return nil, nil, fmt.Errorf("APN: letters, digits, dots and dashes (ask your SIM provider)")
	}
	if s.PIN != "" && !pinRe.MatchString(s.PIN) {
		return nil, nil, fmt.Errorf("SIM PIN: 4 to 8 digits")
	}
	for _, v := range []string{s.User, s.Pass} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return nil, nil, fmt.Errorf("user name and password may not contain line breaks")
		}
	}
	if s.Mode == "" {
		s.Mode = ViaDomains
	}
	var targets []string
	var err error
	switch s.Mode {
	case ViaDomains:
		targets, err = SplitTargets(s.Domains)
	case ViaSources:
		targets, err = SplitSources(s.Domains)
	case ViaFailover:
	default:
		err = fmt.Errorf("unknown mode %q", s.Mode)
	}
	if err != nil {
		return nil, nil, err
	}
	pbr, havePbr := find(cfgs, "pbr")
	if s.Mode != ViaFailover && !havePbr {
		return nil, nil, fmt.Errorf("the pbr package is not installed: install pbr on the Packages screen first (it does the selective routing)")
	}
	fw, ok := find(cfgs, "firewall")
	if !ok {
		return nil, nil, fmt.Errorf("no firewall configuration found")
	}
	var warn []string
	var b builder
	n := s.Name

	exists := has(cfgs, "network", n)
	if !exists {
		b.add("network", n, "interface")
	} else {
		warn = append(warn, "interface '"+n+"' already exists: its modem settings are updated")
	}
	b.set("network", n, "proto", s.Proto)
	b.set("network", n, "device", s.Device)
	b.set("network", n, "apn", s.APN)
	if s.Proto == "modemmanager" {
		b.set("network", n, "iptype", "ipv4")
	} else {
		b.set("network", n, "pdptype", "ipv4")
	}
	if s.User != "" {
		b.set("network", n, "username", s.User)
		b.set("network", n, "password", s.Pass)
		b.set("network", n, "auth", "pap chap")
	}
	if s.PIN != "" {
		b.set("network", n, "pincode", s.PIN)
	}
	// Keep the router's own DNS settings, and keep the main connection preferred.
	b.set("network", n, "peerdns", "0")
	b.set("network", n, "metric", "200")

	var wan *uci.Section
	for i := range fw.Sections {
		if fw.Sections[i].Type == "zone" && opt(fw.Sections[i], "name") == "wan" {
			wan = &fw.Sections[i]
		}
	}
	if wan == nil {
		return nil, nil, fmt.Errorf("no firewall zone named 'wan' (the SIM needs it for NAT)")
	}
	in := false
	for _, v := range vals(*wan, "network") {
		in = in || v == n
	}
	if !in {
		b.list("firewall", wan.Ref(), "network", n)
	}

	if s.Mode == ViaFailover {
		warn = append(warn, "failover only: nothing uses the SIM until the main connection is down; the metric (200) makes the router fall back to it, and back again when the main link returns",
			"to make that fast, add a watchdog: Tasks > Cron templates > Reconnect an interface")
		return b.out, warn, nil
	}
	pol := n + "_" + s.Mode
	if has(cfgs, "pbr", pol) {
		return nil, nil, fmt.Errorf("a pbr policy '%s' already exists: edit it in Config > pbr", pol)
	}
	b.add("pbr", pol, "policy")
	b.set("pbr", pol, "name", "Via "+n)
	if s.Mode == ViaSources {
		b.set("pbr", pol, "src_addr", strings.Join(targets, " "))
	} else {
		b.set("pbr", pol, "dest_addr", strings.Join(targets, " "))
		warn = append(warn, "domain routing needs dnsmasq-full: install it (Packages) if 'apk list' shows plain dnsmasq")
	}
	b.set("pbr", pol, "interface", n)

	for _, sec := range pbr.Sections {
		if sec.Type == "pbr" && opt(sec, "enabled") != "1" {
			warn = append(warn, "pbr is installed but disabled: enable it (Services) after applying")
		}
	}
	warn = append(warn, "if the main connection drops, other traffic may also fall back to the SIM (it uses data)")
	return b.out, warn, nil
}

// SplitSources validates LAN devices: IPv4 addresses, networks or MACs.
func SplitSources(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" || seen[f] {
			continue
		}
		if validate.IPv4(f) != nil && validate.CIDR4(f) != nil && validate.MAC(f) != nil {
			return nil, fmt.Errorf("%q is not an IPv4 address, network or MAC address", f)
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("give at least one device (IP, network or MAC)")
	}
	return out, nil
}

func vals(s uci.Section, k string) []string { o, _ := s.Option(k); return o.Values }
