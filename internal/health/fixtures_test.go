package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/uci"
)

func fixture(t *testing.T, name string) []uci.Config {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "routers", name, "uci.txt"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := uci.ParseExport(string(b))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return c
}

var wanFacts = map[string]Facts{
	"firewall-behind": {WanIP: "192.168.1.2", UpDev: "wan"},
	"home-router":     {WanIP: "203.0.113.77", UpDev: "wan"},
	"sim-only":        {WanIP: "10.45.1.9", UpDev: "wwan0"},
}

func TestRolesAreDetectedFromSyntheticAndTypicalConfigs(t *testing.T) {
	for name, want := range map[string]Role{
		"firewall-behind": RoleBehind, // synthetic fixture: static WAN behind an upstream router
		"home-router":     RoleRouter,
		"ap-only":         RoleAP,
		"sim-only":        RoleCellular, // no interface called wan: the modem is the uplink
	} {
		if got := Detect(fixture(t, name), wanFacts[name]); got != want {
			t.Errorf("%s: role %q, want %q", name, got, want)
		}
	}
}

func TestBehindRouterFindings(t *testing.T) {
	fs, role := CheckRole(Input{Configs: fixture(t, "firewall-behind"), Facts: Facts{SSHKeys: -1, WanIP: "192.168.1.2", UpDev: "wan"}})
	if role != RoleBehind {
		t.Fatalf("role %q", role)
	}
	for _, id := range []string{"dup-list:firewall.@zone[1].network", "multi-zone:direct", "multi-zone:vpn", "pbr-dup:@policy[3]", "fwd-dangling:@forwarding[1]:src"} {
		if find(fs, id) == nil {
			t.Errorf("missing %s in %s", id, ids(fs))
		}
	}
	// the 498 repeated entries must not be counted as 498 zones
	if f := find(fs, "multi-zone:wwan"); f != nil && strings.Contains(f.Title, "498") {
		t.Errorf("repeated entries counted as zones: %s", f.Title)
	}
	if f := find(fs, "multi-zone:direct"); f == nil || !strings.Contains(f.Detail, "uses a password") {
		t.Errorf("Wi-Fi facts missing: %+v", f)
	}
}

func TestRoleTunesFindings(t *testing.T) {
	home := fixture(t, "home-router")
	if f := find(Check(Input{Configs: home, Facts: Facts{SSHKeys: -1}}), "wan-forward"); f == nil || f.Sev == Info {
		t.Fatalf("home router: WAN port forward should warn: %+v", f)
	}
	f := find(Check(Input{Configs: home, Role: RoleBehind, Facts: Facts{SSHKeys: -1}}), "wan-forward")
	if f == nil || f.Sev != Info || !strings.Contains(f.Detail, "behind another router") {
		t.Fatalf("behind: should be a note with the reason: %+v", f)
	}
	ap := Check(Input{Configs: home, Role: RoleAP, Facts: Facts{SSHKeys: -1}})
	if find(ap, "wan-forward") != nil || find(ap, "open-zone") != nil {
		t.Fatalf("AP role must hide firewall findings: %s", ids(ap))
	}
}

func TestRoleStoreRoundTrip(t *testing.T) {
	d := t.TempDir()
	if LoadRole(d, "r") != RoleAuto {
		t.Fatal("default must be auto")
	}
	if err := SaveRole(d, "r", RoleAP); err != nil {
		t.Fatal(err)
	}
	if LoadRole(d, "r") != RoleAP {
		t.Fatal("not saved")
	}
}

func TestSIMOnlyRouterKeepsEveryCheckWorking(t *testing.T) {
	cfgs := fixture(t, "sim-only")
	facts := Facts{SSHKeys: -1, WanIP: "100.72.5.9", UpDev: "wwan0", UpBytes: 3 << 30}
	fs, role := CheckRole(Input{Configs: cfgs, Facts: facts})
	if role != RoleCellular {
		t.Fatalf("role %q", role)
	}
	if find(fs, "cgnat") == nil {
		t.Errorf("CGNAT not noticed: %s", ids(fs))
	}
	if f := find(fs, "sim-usage"); f == nil || !strings.Contains(f.Title, "3.0 GB") {
		t.Errorf("data usage missing: %+v", f)
	}
	if f := find(fs, "wan-forward"); f == nil || f.Sev != Info || !strings.Contains(f.Detail, "Mobile carriers") {
		t.Errorf("forward on SIM should be a note with the reason: %+v", f)
	}
	// a carrier-assigned 10.x address must not make it look like "behind another router"
	if r := Detect(cfgs, Facts{WanIP: "10.45.1.9", UpDev: "wwan0"}); r != RoleCellular {
		t.Errorf("private carrier address changed the role: %q", r)
	}
	// without CGNAT the usual stuff still runs: the lan zone is fine, no false alarms
	if f := find(fs, "multi-zone:wwan"); f != nil {
		t.Errorf("false positive: %+v", f)
	}
}

func TestParseFactsUplinkUsage(t *testing.T) {
	f := ParseFacts("##wanip\n100.72.5.9\n##updev\nwwan0\n1000\n2000\n")
	if f.UpDev != "wwan0" || f.UpBytes != 3000 || f.WanIP != "100.72.5.9" {
		t.Fatalf("%+v", f)
	}
	if !IsCGNAT("100.64.0.1") || !IsCGNAT("100.127.255.254") || IsCGNAT("100.128.0.1") || IsCGNAT("100.63.0.1") || IsCGNAT("10.0.0.1") {
		t.Fatal("CGNAT range wrong")
	}
}

// The fixture has a leftover wwan (ncm) interface in its wan zone, but its
// default route is the ethernet wan: it must not be taken for a SIM router,
// with or without the runtime fact.
func TestLeftoverModemDoesNotMakeItCellular(t *testing.T) {
	cfgs := fixture(t, "firewall-behind")
	for _, f := range []Facts{{}, {UpDev: "wan"}, {UpDev: "eth1", WanIP: "192.168.1.2"}} {
		if r := Detect(cfgs, f); r == RoleCellular {
			t.Errorf("facts %+v: wrongly cellular", f)
		}
	}
	if r := Detect(cfgs, Facts{UpDev: "wwan0"}); r != RoleCellular {
		t.Errorf("default route on a modem must win: %q", r)
	}
}

func TestDNSBypassAndVPNLeakRules(t *testing.T) {
	// the synthetic fixture already forces port 53 to the router: nothing to report
	fs := Check(Input{Configs: fixture(t, "firewall-behind"), Facts: Facts{SSHKeys: -1, UpDev: "wan", WanIP: "192.168.1.2"}})
	if find(fs, "dns-bypass") != nil || find(fs, "dns-vpn-leak") != nil {
		t.Errorf("false DNS finding on the synthetic fixture: %s", ids(fs))
	}
	const base = `package dhcp
config dnsmasq
	list server '9.9.9.9'
package firewall
config zone
	option name 'lan'
	list network 'lan'
`
	if find(Check(Input{Configs: parse(t, base)}), "dns-bypass") == nil {
		t.Error("custom upstream DNS without a port 53 redirect must be noted")
	}
	forced := base + "config redirect\n\toption src 'lan'\n\toption target 'DNAT'\n\toption src_dport '53'\n"
	if find(Check(Input{Configs: parse(t, forced)}), "dns-bypass") != nil {
		t.Error("forced DNS still reported")
	}
	if find(Check(Input{Configs: parse(t, "package dhcp\nconfig dnsmasq\n\toption noresolv '0'\npackage firewall\nconfig zone\n\toption name 'lan'\n")}), "dns-bypass") != nil {
		t.Error("nothing is filtered, so nothing can be bypassed")
	}
	vpn := `package network
config interface 'wan'
	option proto 'dhcp'
config interface 'vpn'
	option proto 'wireguard'
config wireguard_vpn
	list allowed_ips '0.0.0.0/0'
	option route_allowed_ips '1'
`
	if f := find(Check(Input{Configs: parse(t, vpn)}), "dns-vpn-leak"); f == nil || f.Sev != Warn {
		t.Errorf("full-tunnel VPN with ISP DNS must warn: %+v", f)
	}
	fixed := vpn + "package dhcp\nconfig dnsmasq\n\toption noresolv '1'\n\tlist server '10.2.0.1'\n"
	if find(Check(Input{Configs: parse(t, fixed)}), "dns-vpn-leak") != nil {
		t.Error("fixed upstream DNS still flagged")
	}
}

func TestDNSRedirectToAnotherHostDoesNotCount(t *testing.T) {
	cfg := parse(t, "package dhcp\nconfig dnsmasq\n\tlist server '9.9.9.9'\npackage firewall\nconfig zone\n\toption name 'lan'\nconfig redirect\n\toption src 'lan'\n\toption target 'DNAT'\n\toption src_dport '53'\n\toption dest_ip '192.168.1.9'\n")
	if find(Check(Input{Configs: cfg}), "dns-bypass") == nil {
		t.Error("a port 53 redirect to another host is not the router's own DNS")
	}
}
