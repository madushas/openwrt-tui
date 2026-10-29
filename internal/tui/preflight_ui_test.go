package tui

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/preflight"
)

func TestLockoutChangeForcesRollbackAndIsExplained(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "Network")
	n := netOf(t, a)
	selectIf(t, n, "lan")
	press(t, a, "enter")
	n.form.fields[0].Value = "192.168.50.1"
	press(t, a, "s")
	openTab(t, a, "Changes")
	v := plain(a.render())
	c := scr(a, "Changes").(*changesScreen)
	if !strings.Contains(v, `! changes ipaddr of interface "lan"`) || !preflight.NeedsRollback(c.preflight()) {
		t.Fatalf("lock-out risk not explained:\n%s", v)
	}
	if c.mode() != changes.WithRollback {
		t.Fatal("rollback not forced")
	}
	nm := changes.CommitOnly
	c.userMode = &nm // even an explicit choice must not win
	if c.mode() != changes.WithRollback {
		t.Fatal("user override beat the lock-out protection")
	}
	press(t, a, "a")
	if a.ask == nil || !strings.Contains(a.ask.Prompt, "rollback") || !strings.Contains(a.ask.Prompt, "DANGER") || !a.ask.Danger {
		t.Fatalf("apply prompt: %+v", a.ask)
	}
}

func TestHarmlessChangeStaysQuiet(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "DHCP")
	d := dhcpOf(t, a)
	selectRow(t, d, rowPool, "lan")
	press(t, a, "enter")
	d.form.fields[2].Value = "6h"
	press(t, a, "s")
	openTab(t, a, "Changes")
	if v := plain(a.render()); strings.Contains(v, "!") {
		t.Fatalf("a lease-time change raised a warning:\n%s", v)
	}
}
