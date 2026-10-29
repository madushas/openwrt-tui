package health

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/services"
	"github.com/madushakv/owrt-tui/internal/uci"
)

func parse(t *testing.T, s string) []uci.Config {
	t.Helper()
	c, err := uci.ParseExport(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ids(fs []Finding) string {
	var s []string
	for _, f := range fs {
		s = append(s, f.ID)
	}
	return strings.Join(s, ",")
}

func find(fs []Finding, prefix string) *Finding {
	for i := range fs {
		if strings.HasPrefix(fs[i].ID, prefix) {
			return &fs[i]
		}
	}
	return nil
}

func TestDuplicateListFixKeepsEachValueOnce(t *testing.T) {
	var b strings.Builder
	b.WriteString("package firewall\n\nconfig zone\n\toption name 'wan'\n")
	for i := 0; i < 50; i++ {
		b.WriteString("\tlist network 'wan'\n\tlist network 'wwan'\n")
	}
	f := find(Check(Input{Configs: parse(t, b.String()), Facts: Facts{SSHKeys: -1}}), "dup-list")
	if f == nil || !strings.Contains(f.Detail, "100 values, only 2") {
		t.Fatalf("%+v", f)
	}
	if len(f.Fix) != 3 || f.Fix[0].Op != changes.OpDelete || f.Fix[0].Option != "network" {
		t.Fatalf("fix %+v", f.Fix)
	}
}

func TestMultiZone(t *testing.T) {
	c := parse(t, "package firewall\n\nconfig zone\n\toption name 'wan'\n\tlist network 'wwan'\n\nconfig zone\n\toption name 'vpn'\n\tlist network 'wwan'\n")
	f := find(Check(Input{Configs: c, Facts: Facts{SSHKeys: -1}}), "multi-zone:wwan")
	if f == nil || f.Sev != Bad || f.Fix != nil {
		t.Fatalf("%+v", f)
	}
}

func TestOpenZoneFixOnlyWhenDHCPAndDNSAreAllowed(t *testing.T) {
	base := "package firewall\n\nconfig zone\n\toption name 'direct'\n\toption input 'ACCEPT'\n\tlist network 'direct'\n"
	f := find(Check(Input{Configs: parse(t, base), Facts: Facts{SSHKeys: -1}}), "open-zone:direct")
	if f == nil || f.Fix != nil || !strings.Contains(f.Detail, "add those rules first") {
		t.Fatalf("fix offered without DHCP/DNS rules: %+v", f)
	}
	rules := base + "\nconfig rule\n\toption src 'direct'\n\toption dest_port '67'\n\toption target 'ACCEPT'\n\nconfig rule\n\toption src 'direct'\n\toption dest_port '53'\n\toption target 'ACCEPT'\n"
	f = find(Check(Input{Configs: parse(t, rules), Facts: Facts{SSHKeys: -1}}), "open-zone:direct")
	if f == nil || len(f.Fix) != 1 || f.Fix[0].Value != "REJECT" {
		t.Fatalf("%+v", f)
	}
	lan := "package firewall\n\nconfig zone\n\toption name 'lan'\n\toption input 'ACCEPT'\n"
	if f := find(Check(Input{Configs: parse(t, lan), Facts: Facts{SSHKeys: -1}}), "open-zone"); f != nil {
		t.Fatal("lan flagged")
	}
}

func TestWanExposure(t *testing.T) {
	c := parse(t, "package firewall\n\nconfig redirect\n\toption name 'nas'\n\toption src 'wan'\n\toption src_dport '22'\n\toption dest_ip '192.168.1.5'\n\nconfig redirect\n\toption src 'wan'\n\toption src_dport '51820'\n")
	fs := Check(Input{Configs: c, Facts: Facts{SSHKeys: -1}})
	if len(fs) != 1 || fs[0].Fix[0].Option != "enabled" {
		t.Fatalf("%s", ids(fs))
	}
}

func TestSSHFixNeedsAKey(t *testing.T) {
	c := parse(t, "package dropbear\n\nconfig dropbear\n\toption Port '22'\n")
	f := find(Check(Input{Configs: c, Facts: Facts{SSHKeys: 0}}), "ssh-password")
	if f == nil || f.Fix != nil || !strings.Contains(f.Detail, "lock you out") {
		t.Fatalf("%+v", f)
	}
	f = find(Check(Input{Configs: c, Facts: Facts{SSHKeys: 2}}), "ssh-password")
	if f == nil || len(f.Fix) != 2 {
		t.Fatalf("%+v", f)
	}
	if find(Check(Input{Configs: c, Facts: Facts{SSHKeys: -1}}), "ssh-password") != nil {
		t.Fatal("reported without knowing about keys")
	}
	off := parse(t, "package dropbear\n\nconfig dropbear\n\toption PasswordAuth 'off'\n")
	if find(Check(Input{Configs: off, Facts: Facts{SSHKeys: 2}}), "ssh-password") != nil {
		t.Fatal("already off")
	}
}

func TestCountry(t *testing.T) {
	c := parse(t, "package wireless\n\nconfig wifi-device 'radio0'\n\toption channel '6'\n")
	if find(Check(Input{Configs: c, Facts: Facts{SSHKeys: -1}}), "no-country:radio0") == nil {
		t.Fatal("missed")
	}
}

func TestFactsRules(t *testing.T) {
	out := "##overlay\noverlayfs:/overlay 14000 13700 300 98% /overlay\n##release\n25.12.2\n##sshkeys\n1\n##cron\n0 4 * * * reboot\n*/15 * * * * /usr/bin/ddns_update.sh token=abc123 >> /root/ddns.log 2>&1\n*/15 * * * * /usr/bin/x >> /tmp/x.log\n"
	f := ParseFacts(out)
	if f.OverlayFreeKB != 300 || f.OverlayUsedPct != 98 || f.SSHKeys != 1 || f.Release != "25.12.2" || len(f.Cron) != 3 {
		t.Fatalf("%+v", f)
	}
	f.Services = []services.Service{{Name: "adblock", Enabled: true, State: services.Stopped}, {Name: "uhttpd", Enabled: true, State: services.Stopped}}
	fs := Check(Input{Facts: f})
	if find(fs, "overlay-full") == nil || find(fs, "svc-stopped:adblock") == nil || find(fs, "svc-stopped:uhttpd") != nil {
		t.Fatalf("%s", ids(fs))
	}
	cf := find(fs, "cron-flash:/root/ddns.log")
	if cf == nil || strings.Contains(cf.Detail, "abc123") || find(fs, "cron-flash:/tmp") != nil {
		t.Fatalf("%+v", cf)
	}
}

func TestOrderingAndCleanRouter(t *testing.T) {
	if fs := Check(Input{Facts: Facts{SSHKeys: -1}}); len(fs) != 0 {
		t.Fatalf("%s", ids(fs))
	}
}
