package changes

import (
	"strings"
	"testing"
)

func TestSetDedupeAndRevert(t *testing.T) {
	var s Set
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Add(Change{Op: OpSet, Config: "network", Section: "lan", Option: "ipaddr", Value: "10.0.0.1", Old: "192.168.1.1"}))
	must(s.Add(Change{Op: OpSet, Config: "network", Section: "lan", Option: "ipaddr", Value: "10.1.1.1"}))
	if s.Len() != 1 {
		t.Fatalf("expected dedupe, got %d", s.Len())
	}
	it := s.Items()[0]
	if it.Old != "192.168.1.1" || it.Value != "10.1.1.1" {
		t.Fatalf("original value must be kept for the diff: %+v", it)
	}
	must(s.Add(Change{Op: OpSet, Config: "network", Section: "lan", Option: "ipaddr", Value: "192.168.1.1"}))
	if s.Len() != 0 {
		t.Fatalf("setting back to original should unstage, got %d", s.Len())
	}
}

func TestValidateRejectsInjection(t *testing.T) {
	var s Set
	bad := []Change{
		{Op: OpSet, Config: "network; reboot", Section: "lan", Option: "x", Value: "1"},
		{Op: OpSet, Config: "network", Section: "lan", Option: "a b", Value: "1"},
		{Op: OpSet, Config: "network", Section: "lan", Option: "x", Value: "1\nuci delete network"},
		{Op: OpSet, Config: "network", Section: "lan", Value: "1"},
		{Op: "rm", Config: "network", Section: "lan"},
	}
	for _, c := range bad {
		if err := s.Add(c); err == nil {
			t.Errorf("expected rejection: %+v", c)
		}
	}
}

func TestCommandQuoting(t *testing.T) {
	c := Change{Op: OpSet, Config: "wireless", Section: "@wifi-iface[0]", Option: "ssid", Value: "Bob's $(reboot) WiFi"}
	got := c.Command()
	want := `uci set 'wireless.@wifi-iface[0].ssid=Bob'\''s $(reboot) WiFi'`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestGuard(t *testing.T) {
	var s Set
	_ = s.Add(Change{Op: OpSet, Config: "system", Section: "@system[0]", Option: "hostname", Value: "x"})
	if r, _ := s.Guard(); r != RiskNone || s.RecommendedMode() != CommitOnly {
		t.Fatalf("system edit should be safe, got %v", r)
	}
	_ = s.Add(Change{Op: OpSet, Config: "network", Section: "lan", Option: "ipaddr", Value: "10.0.0.1"})
	if r, f := s.Guard(); r != RiskHigh || len(f) != 1 || s.RecommendedMode() != WithRollback {
		t.Fatalf("ipaddr change should be high risk: %v %+v", r, f)
	}
	var d Set
	_ = d.Add(Change{Op: OpSet, Config: "dropbear", Section: "@dropbear[0]", Option: "Port", Value: "2222"})
	if r, _ := d.Guard(); r != RiskHigh {
		t.Fatal("dropbear should be high risk")
	}
}

func TestScriptCommitOnly(t *testing.T) {
	var s Set
	_ = s.Add(Change{Op: OpSet, Config: "dhcp", Section: "lan", Option: "start", Value: "50"})
	sc, err := s.Script(Plan{ID: "chg-test-1", Mode: CommitOnly})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cp -a /etc/config/.", "uci set 'dhcp.lan.start=50'", "uci commit dhcp", "reload_config"} {
		if !strings.Contains(sc, want) {
			t.Errorf("script missing %q:\n%s", want, sc)
		}
	}
	if strings.Contains(sc, "nohup") {
		t.Error("commit-only must not start a watchdog")
	}
}

func TestScriptRollbackOrder(t *testing.T) {
	var s Set
	_ = s.Add(Change{Op: OpSet, Config: "network", Section: "lan", Option: "ipaddr", Value: "10.0.0.1"})
	sc, err := s.Script(Plan{ID: "chg-test-2", Mode: WithRollback, Timeout: 45})
	if err != nil {
		t.Fatal(err)
	}
	wd := strings.Index(sc, "nohup")
	commit := strings.Index(sc, "uci commit")
	if wd < 0 || commit < 0 || wd > commit {
		t.Fatalf("watchdog must start before commit:\n%s", sc)
	}
	if !strings.Contains(sc, "sleep 45") {
		t.Error("timeout missing")
	}
}

func TestScriptRejectsBadID(t *testing.T) {
	var s Set
	_ = s.Add(Change{Op: OpSet, Config: "dhcp", Section: "lan", Option: "start", Value: "1"})
	if _, err := s.Script(Plan{ID: "x; reboot"}); err == nil {
		t.Fatal("bad id accepted")
	}
	if _, err := (&Set{}).Script(Plan{ID: "chg-test-3"}); err == nil {
		t.Fatal("empty set accepted")
	}
}
