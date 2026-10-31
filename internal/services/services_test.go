package services

import (
	"context"
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/transport"
)

const fixture = `svc dnsmasq 1 19
svc dropbear 1 19
svc firewall 1 19
svc led 1 96
svc network 1 20
svc odd 0
garbage line
##ubus
{
  "dnsmasq": {"instances": {"instance1": {"running": true, "pid": 1234}}},
  "dropbear": {"instances": {"instance1": {"running": true}, "instance2": {"running": false}}},
  "firewall": {"instances": {}},
  "network": {"instances": {"a": {"running": false}}}
}
`

func TestParseList(t *testing.T) {
	got := ParseList(fixture)
	if len(got) != 6 {
		t.Fatalf("want 6 services (garbage skipped), got %d: %+v", len(got), got)
	}
	byName := map[string]Service{}
	for _, s := range got {
		byName[s.Name] = s
	}
	cases := []struct {
		name    string
		enabled bool
		start   int
		state   State
	}{
		{"dnsmasq", true, 19, Running},
		{"dropbear", true, 19, Running}, // any running instance wins
		{"firewall", true, 19, Unknown}, // no instances: not procd-tracked
		{"network", true, 20, Stopped},
		{"led", true, 96, Unknown},
		{"odd", false, 0, Unknown}, // a row with only three fields must not panic
	}
	for _, c := range cases {
		s := byName[c.name]
		if s.Enabled != c.enabled || s.Start != c.start || s.State != c.state {
			t.Errorf("%s = %+v, want enabled=%v start=%d state=%v", c.name, s, c.enabled, c.start, c.state)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Fatalf("not sorted: %s before %s", got[i-1].Name, got[i].Name)
		}
	}
}

func TestParseListToleratesBadJSON(t *testing.T) {
	got := ParseList("svc a 1 10\n##ubus\n{not json")
	if len(got) != 1 || got[0].State != Unknown {
		t.Fatalf("got %+v", got)
	}
}

func TestCommandRejectsInjection(t *testing.T) {
	for _, name := range []string{"", "a b", "x;reboot", "$(id)", "a/b", "x`id`", "a\nb"} {
		if _, err := Command(name, Restart); err == nil {
			t.Errorf("name %q should be rejected", name)
		}
	}
	if _, err := Command("dnsmasq", Action("rm -rf /")); err == nil {
		t.Error("unknown action should be rejected")
	}
	cmd, err := Command("dnsmasq", Restart)
	if err != nil || cmd != "/etc/init.d/dnsmasq restart" {
		t.Errorf("got %q, %v", cmd, err)
	}
}

func TestWarning(t *testing.T) {
	if Warning("dropbear", Restart) == "" || Warning("network", Stop) == "" {
		t.Error("expected warnings for dropbear restart and network stop")
	}
	if Warning("dropbear", Start) != "" || Warning("dnsmasq", Restart) != "" {
		t.Error("unexpected warning")
	}
}

func TestDo(t *testing.T) {
	f := (&transport.Fake{}).
		OnResult("/etc/init.d/good", transport.Result{Stdout: "ok"}).
		OnResult("/etc/init.d/bad", transport.Result{Stdout: "no such thing", ExitCode: 1})
	if out, err := Do(context.Background(), f, "good", Start); err != nil || out != "ok" {
		t.Errorf("good: %q %v", out, err)
	}
	_, err := Do(context.Background(), f, "bad", Start)
	if err == nil || !strings.Contains(err.Error(), "no such thing") {
		t.Errorf("bad: %v", err)
	}
	if n := len(f.Calls); n != 2 {
		t.Errorf("expected 2 remote calls, got %d", n)
	}
	if _, err := Do(context.Background(), f, "x;y", Start); err == nil || len(f.Calls) != 2 {
		t.Error("an invalid name must be rejected before any remote call")
	}
}

// Real procd output from an OpenWrt 25.12 router: adblock and pbr are
// one-shot services whose instance ended cleanly, which is not a crash.
func TestOneShotServicesAreNotStopped(t *testing.T) {
	js := `{"adblock":{"instances":{"adblock":{"running":false,"exit_code":0}}},
"pbr":{"instances":{"main":{"running":false,"exit_code":0}}},
"crashy":{"instances":{"i":{"running":false,"exit_code":1}}},
"gone":{"instances":{"i":{"running":false}}},
"cron":{"instances":{"instance1":{"running":true,"pid":1234}}}}`
	got := parseProcd(js)
	for _, n := range []string{"adblock", "pbr"} {
		if _, ok := got[n]; ok {
			t.Errorf("%s: clean one-shot exit must not be reported (got %v)", n, got[n])
		}
	}
	if got["crashy"] != Stopped || got["gone"] != Stopped || got["cron"] != Running {
		t.Errorf("states wrong: %v", got)
	}
}
