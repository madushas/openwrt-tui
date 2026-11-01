package tasks

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// Shaped like the real router: a leftover ncm interface, a wan zone, pbr.
const simCfg = `package network
config interface 'wwan'
	option device '/dev/ttyUSB0'
	option proto 'ncm'
package firewall
config zone
	option name 'lan'
	list network 'lan'
config zone
	option name 'wan'
	list network 'wan'
	option masq '1'
package pbr
config pbr 'config'
	option enabled '1'
config policy 'vpn_bypass'
	option dest_addr '192.248.2.10'
	option interface 'wan'
`

func simCfgs(t *testing.T) []uci.Config {
	c, err := uci.ParseExport(simCfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSIMRoute(t *testing.T) {
	ch, warn, err := SIMRoute(SIM{Name: "sim", Proto: "qmi", Device: "/dev/cdc-wdm0", APN: "internet",
		Domains: "Example.com, api.example.com 10.1.0.0/16 example.com"}, simCfgs(t))
	if err != nil {
		t.Fatal(err)
	}
	var s changes.Set
	for _, c := range ch {
		if err := s.Add(c); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
	}
	var text []string
	for _, c := range ch {
		text = append(text, c.Describe())
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{"example.com api.example.com 10.1.0.0/16", "peerdns", "metric", "pbr.sim_domains.interface"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if !strings.Contains(strings.Join(warn, "|"), "dnsmasq-full") {
		t.Error("no dnsmasq-full warning")
	}
	if !strings.Contains(all, "@zone[1]") && !strings.Contains(all, "wan") {
		t.Errorf("sim not added to wan zone:\n%s", all)
	}
}

func TestSIMRouteReusesExistingInterface(t *testing.T) {
	ch, _, err := SIMRoute(SIM{Name: "wwan", Proto: "ncm", Device: "/dev/ttyUSB0", APN: "x", Domains: "a.example"}, simCfgs(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Op == changes.OpAddSect && c.Config == "network" {
			t.Fatal("must not recreate an existing interface")
		}
	}
}

func TestSIMRouteRejectsBadInput(t *testing.T) {
	base := SIM{Name: "sim", Proto: "qmi", Device: "/dev/cdc-wdm0", APN: "internet", Domains: "a.example"}
	for name, mut := range map[string]func(*SIM){
		"name":   func(s *SIM) { s.Name = "Sim 1" },
		"proto":  func(s *SIM) { s.Proto = "ppp" },
		"device": func(s *SIM) { s.Device = "/dev/x; reboot" },
		"apn":    func(s *SIM) { s.APN = "a b" },
		"pin":    func(s *SIM) { s.PIN = "12" },
		"domain": func(s *SIM) { s.Domains = "bad_domain!" },
		"nodoms": func(s *SIM) { s.Domains = " , " },
		"passnl": func(s *SIM) { s.User, s.Pass = "u", "a\nb" },
	} {
		s := base
		mut(&s)
		if _, _, err := SIMRoute(s, simCfgs(t)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	noPbr, _ := uci.ParseExport("package firewall\nconfig zone\n\toption name 'wan'\n")
	if _, _, err := SIMRoute(base, noPbr); err == nil || !strings.Contains(err.Error(), "pbr") {
		t.Errorf("missing pbr not explained: %v", err)
	}
}

func TestSIMModes(t *testing.T) {
	base := SIM{Name: "sim", Proto: "qmi", Device: "/dev/cdc-wdm0", APN: "internet"}
	src := base
	src.Mode, src.Domains = ViaSources, "192.168.50.50, AA:BB:CC:DD:EE:FF"
	ch, warn, err := SIMRoute(src, simCfgs(t))
	if err != nil {
		t.Fatal(err)
	}
	var d []string
	for _, c := range ch {
		d = append(d, c.Describe())
	}
	all := strings.Join(d, "\n")
	if !strings.Contains(all, "src_addr") || strings.Contains(all, "dest_addr") || strings.Contains(strings.Join(warn, " "), "dnsmasq-full") {
		t.Errorf("sources mode wrong:\n%s\n%v", all, warn)
	}
	fo := base
	fo.Mode = ViaFailover
	ch, _, err = SIMRoute(fo, simCfgs(t)) // no domains needed, no pbr touched
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Config == "pbr" {
			t.Fatal("failover must not add routing policies")
		}
	}
	noPbr, _ := uci.ParseExport("package firewall\nconfig zone\n\toption name 'wan'\n")
	if _, _, err := SIMRoute(fo, noPbr); err != nil {
		t.Fatalf("failover must work without pbr: %v", err)
	}
	bad := src
	bad.Domains = "not a device!"
	if _, _, err := SIMRoute(bad, simCfgs(t)); err == nil {
		t.Fatal("bad device accepted")
	}
}
