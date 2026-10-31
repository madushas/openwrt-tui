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
	for _, id := range []string{"dup-list:firewall.@zone[1].network", "multi-zone:direct", "multi-zone:eduvpn", "pbr-dup:@policy[3]", "fwd-dangling:@forwarding[1]:src"} {
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
