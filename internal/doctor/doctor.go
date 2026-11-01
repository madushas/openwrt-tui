// Package doctor checks that the router has what each owrt-tui feature needs
// (commands, files, ubus objects, kernel settings) and says what is missing,
// which feature it affects and which package provides it. It only reads.
package doctor

import (
	"fmt"
	"strings"
)

// Level says how much a missing item matters.
type Level int

const (
	Core     Level = iota // the tool cannot work well without it
	Feature               // one feature is unavailable without it
	Optional              // a nice-to-have, or only for some setups
)

type spec struct {
	kind    string // cmd, file, ubus
	name    string
	level   Level
	feature string
	pkg     string
}

var specs = []spec{
	{"cmd", "uci", Core, "reading and changing configuration", "uci"},
	{"cmd", "ubus", Core, "network, Wi-Fi and service status", "ubus"},
	{"cmd", "jsonfilter", Core, "reading status from ubus", "jsonfilter"},
	{"cmd", "logread", Feature, "Logs and Events", "logd"},
	{"cmd", "ip", Feature, "Network and routes", "ip-tiny"},
	{"cmd", "iw", Feature, "Wi-Fi channels and widths (Wi-Fi forms)", "iw"},
	{"cmd", "iwinfo", Optional, "Wi-Fi client signal details", "iwinfo"},
	{"cmd", "nft", Optional, "firewall rule counters", "nftables-json"},
	{"cmd", "crontab", Feature, "Cron screen and cron templates", "busybox"},
	{"file", "/etc/init.d/cron", Feature, "scheduled jobs actually running", "busybox"},
	{"cmd", "sysupgrade", Feature, "firmware image check", "base-files"},
	{"cmd", "tar", Feature, "configuration backup", "tar"},
	{"cmd", "wg", Optional, "WireGuard status, QR devices and watchdog", "wireguard-tools"},
	{"cmd", "apk", Optional, "package manager (25.12 and newer)", "apk-mbedtls"},
	{"cmd", "opkg", Optional, "package manager (24.10 and older)", "opkg"},
	{"file", "/proc/net/nf_conntrack", Feature, "per-device traffic on Clients", "kmod-nf-conntrack"},
	{"file", "/etc/config/pbr", Optional, "SIM or VPN routing by domain or device", "pbr"},
	{"file", "/etc/config/adblock", Optional, "ad blocking", "adblock"},
	{"file", "/etc/config/sqm", Optional, "traffic shaping", "sqm-scripts"},
	{"ubus", "network.wireless", Feature, "Wi-Fi status", "netifd / wpad"},
	{"ubus", "service", Feature, "Services screen", "procd"},
	{"cmd", "uqmi", Optional, "QMI modems (SIM)", "uqmi"},
	{"cmd", "mmcli", Optional, "ModemManager modems (SIM)", "modemmanager"},
	{"cmd", "gcom", Optional, "NCM and 3G modems (SIM)", "comgt"},
}

// Script prints one line per check, then a few values. It only reads.
func Script() string {
	var b strings.Builder
	b.WriteString("echo '##owrt-doctor'\n")
	for _, s := range specs {
		switch s.kind {
		case "cmd":
			fmt.Fprintf(&b, "command -v %s >/dev/null 2>&1 && echo 'ok cmd %s' || echo 'no cmd %s'\n", s.name, s.name, s.name)
		case "file":
			fmt.Fprintf(&b, "[ -e %s ] && echo 'ok file %s' || echo 'no file %s'\n", s.name, s.name, s.name)
		case "ubus":
			fmt.Fprintf(&b, "ubus list %s >/dev/null 2>&1 && echo 'ok ubus %s' || echo 'no ubus %s'\n", s.name, s.name, s.name)
		}
	}
	b.WriteString("echo \"val uid $(id -u)\"\n")
	b.WriteString("echo \"val acct $(cat /proc/sys/net/netfilter/nf_conntrack_acct 2>/dev/null)\"\n")
	b.WriteString("echo \"val nftset $(dnsmasq --version 2>/dev/null | grep -c nftset)\"\n")
	return b.String()
}

// Item is one result.
type Item struct {
	Name    string
	OK      bool
	Level   Level
	Feature string
	Hint    string // what to do when not OK
}

// Report is the parsed result.
type Report struct {
	Items []Item
}

// Parse reads the output of Script. Unknown lines are ignored.
func Parse(out string) Report {
	have := map[string]bool{}
	vals := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(l))
		switch {
		case len(f) == 3 && (f[0] == "ok" || f[0] == "no"):
			have[f[1]+" "+f[2]] = f[0] == "ok"
		case len(f) >= 2 && f[0] == "val":
			vals[f[1]] = strings.Join(f[2:], " ")
		}
	}
	var r Report
	for _, s := range specs {
		ok, seen := have[s.kind+" "+s.name]
		if !seen {
			continue // the probe did not get that far: say nothing rather than guess
		}
		it := Item{Name: s.name, OK: ok, Level: s.level, Feature: s.feature}
		if !ok {
			it.Hint = "install package " + s.pkg
			if s.kind == "file" && s.level == Optional {
				it.Hint = "install package " + s.pkg + " if you want this"
			}
		}
		r.Items = append(r.Items, it)
	}
	// the package manager: one of apk/opkg is enough
	apk, opkg := find(r, "apk"), find(r, "opkg")
	if apk != nil && opkg != nil && (apk.OK || opkg.OK) {
		apk.OK, opkg.OK = true, true
		apk.Hint, opkg.Hint = "", ""
	}
	if v, ok := vals["uid"]; ok && v != "" {
		r.Items = append(r.Items, Item{Name: "root login", OK: v == "0", Level: Core, Feature: "everything that changes the router",
			Hint: "connect as root (ssh root@router); other users cannot run uci commit or init scripts"})
	}
	if v, ok := vals["acct"]; ok {
		it := Item{Name: "connection byte counters", OK: v == "1", Level: Optional, Feature: "traffic per device on Clients",
			Hint: "run: sysctl -w net.netfilter.nf_conntrack_acct=1 (add it to /etc/sysctl.conf to keep it)"}
		r.Items = append(r.Items, it)
	}
	if v, ok := vals["nftset"]; ok {
		r.Items = append(r.Items, Item{Name: "dnsmasq with nftset", OK: v != "" && v != "0", Level: Optional,
			Feature: "routing chosen domains through a VPN or SIM", Hint: "replace dnsmasq with dnsmasq-full (Packages)"})
	}
	return r
}

func find(r Report, name string) *Item {
	for i := range r.Items {
		if r.Items[i].Name == name {
			return &r.Items[i]
		}
	}
	return nil
}

// Problems counts missing items by level.
func (r Report) Problems() (core, feature, optional int) {
	for _, it := range r.Items {
		if it.OK {
			continue
		}
		switch it.Level {
		case Core:
			core++
		case Feature:
			feature++
		default:
			optional++
		}
	}
	return
}

// Text renders the report for the terminal or a pasted bug report.
func (r Report) Text() string {
	var b strings.Builder
	label := map[Level]string{Core: "REQUIRED", Feature: "FEATURE ", Optional: "optional"}
	core, feat, opt := r.Problems()
	for _, it := range r.Items {
		if it.OK {
			fmt.Fprintf(&b, "  ok        %s  (%s)\n", it.Name, it.Feature)
		}
	}
	for _, lv := range []Level{Core, Feature, Optional} {
		for _, it := range r.Items {
			if !it.OK && it.Level == lv {
				fmt.Fprintf(&b, "  MISSING   %s  [%s] %s: %s\n", it.Name, label[lv], it.Feature, it.Hint)
			}
		}
	}
	fmt.Fprintf(&b, "\n%d required, %d feature and %d optional item(s) missing\n", core, feat, opt)
	return b.String()
}
