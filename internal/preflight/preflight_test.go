package preflight

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/model"
	"github.com/madushakv/owrt-tui/internal/uci"
)

const base = `package network

config interface 'lan'
	option device 'br-lan'
	option proto 'static'
	option ipaddr '192.168.10.1'
	option netmask '255.255.255.0'

config interface 'direct'
	option proto 'static'
	option ipaddr '192.168.30.1'
	option netmask '255.255.255.0'

package dhcp

config dhcp 'lan'
	option interface 'lan'
	option start '100'
	option limit '150'

config host
	option name 'tv'
	option ip '192.168.10.212'
	list mac '64:82:14:47:41:0F'

package dropbear

config dropbear 'main'
	option enable '1'
	option Port '22'

package uhttpd

config uhttpd 'main'
	list listen_http '0.0.0.0:80'

package firewall

config zone
	option name 'lan'
	option input 'ACCEPT'
	list network 'lan'

config zone
	option name 'free'
	option input 'REJECT'
	list network 'direct'

package wireless

config wifi-iface 'a'
	option ssid 'Net'
	option encryption 'psk2'
	option key 'secret-pass'
`

func parse(t *testing.T) []uci.Config {
	c, err := uci.ParseExport(base)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func check(t *testing.T, host string, ch ...changes.Change) []Finding {
	t.Helper()
	var set changes.Set
	for _, c := range ch {
		if err := set.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	b := parse(t)
	return Check(b, model.Overlay(b, &set), Context{Host: host})
}

func has(fs []Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func set(cfg, sec, opt, val string) changes.Change {
	return changes.Change{Op: changes.OpSet, Config: cfg, Section: sec, Option: opt, Value: val}
}

func TestNothingChangedNothingReported(t *testing.T) {
	if fs := check(t, "192.168.10.1"); len(fs) != 0 {
		t.Fatalf("%+v", fs)
	}
}

func TestChangingTheManagementAddressIsALockout(t *testing.T) {
	fs := check(t, "192.168.10.1", set("network", "lan", "ipaddr", "192.168.20.1"))
	if !has(fs, "lockout-iface") || !NeedsRollback(fs) {
		t.Fatalf("%+v", fs)
	}
	// the same edit on the other interface is not a lock-out
	if fs := check(t, "192.168.10.1", set("network", "direct", "ipaddr", "192.168.31.1")); NeedsRollback(fs) {
		t.Fatalf("unrelated interface flagged: %+v", fs)
	}
	// a host name means "probably lan"
	if fs := check(t, "router.lan", set("network", "lan", "netmask", "255.255.0.0")); !NeedsRollback(fs) {
		t.Fatal("named host: lan change not flagged")
	}
}

func TestSSHAndFirewallLockouts(t *testing.T) {
	if fs := check(t, "192.168.10.1", set("dropbear", "main", "Port", "2222")); !has(fs, "lockout-ssh") {
		t.Fatalf("%+v", fs)
	}
	if fs := check(t, "192.168.10.1", set("firewall", "@zone[0]", "input", "REJECT")); !has(fs, "lockout-zone") {
		t.Fatalf("%+v", fs)
	}
	del := changes.Change{Op: changes.OpDelete, Config: "network", Section: "lan"}
	if fs := check(t, "192.168.10.1", del); !has(fs, "lockout-iface") {
		t.Fatalf("%+v", fs)
	}
}

func TestOverlapPoolAndLeaseChecks(t *testing.T) {
	if fs := check(t, "x", set("network", "direct", "ipaddr", "192.168.10.77")); !has(fs, "subnet-overlap") {
		t.Fatalf("%+v", fs)
	}
	if fs := check(t, "x", set("dhcp", "lan", "limit", "300")); !has(fs, "dhcp-pool") {
		t.Fatalf("%+v", fs)
	}
	if fs := check(t, "x", set("dhcp", "@host[0]", "ip", "10.9.9.9")); !has(fs, "lease-off-net") {
		t.Fatalf("%+v", fs)
	}
	var s changes.Set
	s.Add(changes.Change{Op: changes.OpAddSect, Config: "dhcp", Section: "dup", Type: "host"})
	s.Add(set("dhcp", "dup", "ip", "192.168.10.212"))
	s.Add(set("dhcp", "dup", "mac", "64:82:14:47:41:0f"))
	b := parse(t)
	fs := Check(b, model.Overlay(b, &s), Context{})
	if !has(fs, "lease-dup-ip") || !has(fs, "lease-dup-mac") {
		t.Fatalf("%+v", fs)
	}
}

func TestPortsWifiAndZones(t *testing.T) {
	if fs := check(t, "x", set("dropbear", "main", "Port", "80")); !has(fs, "port-conflict") {
		t.Fatalf("%+v", fs)
	}
	del := changes.Change{Op: changes.OpDelete, Config: "wireless", Section: "a", Option: "key"}
	fs := check(t, "x", del)
	if !has(fs, "wifi-no-key") || fs[0].Sev != Danger {
		t.Fatalf("%+v", fs)
	}
	if fs := check(t, "x", changes.Change{Op: changes.OpAddList, Config: "firewall", Section: "@zone[1]", Option: "network", Value: "lan"}); !has(fs, "multi-zone") {
		t.Fatalf("%+v", fs)
	}
}

func TestExistingProblemsAreNotBlamedOnTheChange(t *testing.T) {
	b := parse(t)
	// make the router already inconsistent, then change something unrelated
	var pre changes.Set
	pre.Add(changes.Change{Op: changes.OpAddList, Config: "firewall", Section: "@zone[1]", Option: "network", Value: "lan"})
	broken := model.Overlay(b, &pre)
	var s changes.Set
	s.Add(set("dhcp", "lan", "start", "101"))
	if fs := Check(broken, model.Overlay(broken, &s), Context{}); len(fs) != 0 {
		t.Fatalf("pre-existing problem reported: %+v", fs)
	}
}

func TestMessagesStayShortAndPlain(t *testing.T) {
	for _, f := range check(t, "192.168.10.1", set("network", "lan", "ipaddr", "192.168.20.1")) {
		if strings.Contains(f.Msg, "\n") || len(f.Msg) > 200 {
			t.Fatalf("bad message %q", f.Msg)
		}
	}
}
