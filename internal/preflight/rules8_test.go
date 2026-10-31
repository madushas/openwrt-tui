package preflight

import (
	"testing"

	"github.com/madushakv/owrt-tui/internal/uci"
)

func cf(t *testing.T, s string) []uci.Config {
	t.Helper()
	c, err := uci.ParseExport(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const net8 = `package network

config device 'br_lan'
	option type 'bridge'
	option name 'br-lan'
	list ports 'lan1'
	list ports 'lan2'

config interface 'lan'
	option device 'br-lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
	option netmask '255.255.255.0'

config interface 'wan'
	option device 'wan'
	option proto 'dhcp'

package firewall

config zone
	option name 'lan'
	option input 'ACCEPT'
	list network 'lan'

config zone
	option name 'wan'
	option input 'REJECT'
	list network 'wan'

config forwarding
	option src 'lan'
	option dest 'wan'
`

func with(t *testing.T, repl ...string) []uci.Config {
	s := net8
	for i := 0; i+1 < len(repl); i += 2 {
		if !contains(s, repl[i]) {
			t.Fatalf("test setup: %q not found", repl[i])
		}
		s = replace(s, repl[i], repl[i+1])
	}
	return cf(t, s)
}

func contains(s, sub string) bool { return len(sub) > 0 && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func replace(s, old, new string) string {
	i := indexOf(s, old)
	return s[:i] + new + s[i+len(old):]
}

func TestCleanConfigHasNoFindings(t *testing.T) {
	b := cf(t, net8)
	if fs := Check(b, b, Context{Host: "192.168.1.1"}); len(fs) != 0 {
		t.Fatalf("%+v", fs)
	}
}

func TestDanglingFirewallReferences(t *testing.T) {
	b := cf(t, net8)
	a := with(t, "option dest 'wan'", "option dest 'wann'")
	fs := Check(b, a, Context{Host: "192.168.1.1"})
	if !has(fs, "zone-ref") {
		t.Fatalf("%+v", fs)
	}
	a = with(t, "list network 'wan'", "list network 'wan'\n\tlist network 'ghost'")
	if fs := Check(b, a, Context{Host: "192.168.1.1"}); !has(fs, "zone-iface") {
		t.Fatalf("%+v", fs)
	}
	a = with(t, "config zone\n\toption name 'wan'", "config zone\n\toption name 'lan'")
	if fs := Check(b, a, Context{Host: "192.168.1.1"}); !has(fs, "zone-dup") {
		t.Fatalf("%+v", fs)
	}
	// a problem that was already there is not blamed on a later change
	if fs := Check(a, a, Context{Host: "192.168.1.1"}); len(fs) != 0 {
		t.Fatalf("pre-existing problem reported: %+v", fs)
	}
}

func TestBridgeLockoutAndPorts(t *testing.T) {
	b := cf(t, net8)
	// removing a port of the bridge that carries the management interface
	a := with(t, "\tlist ports 'lan2'\n", "")
	fs := Check(b, a, Context{Host: "192.168.1.1"})
	if !has(fs, "lockout-bridge") || !NeedsRollback(fs) {
		t.Fatalf("%+v", fs)
	}
	// a VLAN added on that bridge
	a = with(t, "package firewall", "config bridge-vlan\n\toption device 'br-lan'\n\toption vlan '10'\n\tlist ports 'lan1:t'\n\npackage firewall")
	if fs := Check(b, a, Context{Host: "192.168.1.1"}); !has(fs, "lockout-bridge") {
		t.Fatalf("%+v", fs)
	}
	// a port in two bridges
	a = with(t, "config interface 'lan'", "config device 'br_x'\n\toption type 'bridge'\n\toption name 'br-x'\n\tlist ports 'lan1'\n\nconfig interface 'lan'")
	if fs := Check(b, a, Context{Host: "192.168.1.1"}); !has(fs, "bridge-port") {
		t.Fatalf("%+v", fs)
	}
	// duplicate VLAN
	v := "config bridge-vlan\n\toption device 'br-lan'\n\toption vlan '10'\n\nconfig bridge-vlan\n\toption device 'br-lan'\n\toption vlan '10'\n\npackage firewall"
	a = with(t, "package firewall", v)
	if fs := Check(b, a, Context{Host: "192.168.1.1"}); !has(fs, "vlan-dup") {
		t.Fatalf("%+v", fs)
	}
	// interface on a bridge that is not defined
	a = with(t, "option device 'wan'", "option device 'br-nowhere'")
	if fs := Check(b, a, Context{Host: "192.168.1.1"}); !has(fs, "bridge-missing") {
		t.Fatalf("%+v", fs)
	}
	// a different host (not on br-lan) is not locked out by it
	if fs := Check(b, with(t, "\tlist ports 'lan2'\n", ""), Context{Host: "10.0.0.1"}); has(fs, "lockout-bridge") {
		t.Fatalf("%+v", fs)
	}
}

func TestRoutes(t *testing.T) {
	b := cf(t, net8)
	add := func(r string) []uci.Config {
		return with(t, "package firewall", r+"\npackage firewall")
	}
	r := "config route\n\toption interface 'lan'\n\toption target '10.5.0.0/16'\n\toption gateway '192.168.1.254'\n"
	if fs := Check(b, add(r), Context{Host: "192.168.1.1"}); len(fs) != 0 {
		t.Fatalf("a good route flagged: %+v", fs)
	}
	if fs := Check(b, add(r+"\nconfig route\n\toption interface 'lan'\n\toption target '10.5.0.0/16'\n\toption gateway '192.168.1.254'\n"), Context{Host: "x"}); !has(fs, "route-dup") {
		t.Fatalf("%+v", fs)
	}
	bad := "config route\n\toption interface 'lan'\n\toption target '10.6.0.0/16'\n\toption gateway '172.16.0.1'\n"
	if fs := Check(b, add(bad), Context{Host: "x"}); !has(fs, "route-gateway") {
		t.Fatalf("%+v", fs)
	}
	ghost := "config route\n\toption interface 'nope'\n\toption target '10.6.0.0/16'\n"
	if fs := Check(b, add(ghost), Context{Host: "x"}); !has(fs, "route-iface") {
		t.Fatalf("%+v", fs)
	}
	def := "config route\n\toption interface 'wan'\n\toption target '0.0.0.0/0'\n\toption gateway '1.2.3.4'\n"
	fs := Check(b, add(def), Context{Host: "x"})
	if !has(fs, "route-default") || !NeedsRollback(fs) {
		t.Fatalf("a default route must force rollback: %+v", fs)
	}
	// legacy target+netmask form is understood
	old := "config route\n\toption interface 'lan'\n\toption target '10.7.0.0'\n\toption netmask '255.255.0.0'\n\toption gateway '192.168.1.254'\n"
	if fs := Check(b, add(old), Context{Host: "x"}); len(fs) != 0 {
		t.Fatalf("%+v", fs)
	}
}

func TestRuleThatBlocksTheRouter(t *testing.T) {
	b := cf(t, net8)
	r := "config rule\n\toption name 'Block-LAN'\n\toption src 'lan'\n\toption target 'DROP'\n"
	a := cf(t, net8+"\n"+r)
	fs := Check(b, a, Context{Host: "192.168.1.1"})
	if !has(fs, "lockout-rule") || !NeedsRollback(fs) {
		t.Fatalf("%+v", fs)
	}
	// only for one device is fine
	ok := cf(t, net8+"\n"+r+"\toption src_mac 'aa:bb:cc:00:00:01'\n")
	if fs := Check(b, ok, Context{Host: "192.168.1.1"}); has(fs, "lockout-rule") {
		t.Fatalf("%+v", fs)
	}
	// a forwarding-only rule (with a dest) is fine
	fwd := cf(t, net8+"\n"+r+"\toption dest 'wan'\n")
	if fs := Check(b, fwd, Context{Host: "192.168.1.1"}); has(fs, "lockout-rule") {
		t.Fatalf("%+v", fs)
	}
}
