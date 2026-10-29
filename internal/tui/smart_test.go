package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/postcheck"
	"github.com/madushakv/owrt-tui/internal/transport"
)

var healthy = postcheck.Result{"default_route": "ok", "gateway": "ok", "dns": "ok", "wg_eduvpn": "30"}

// pending puts the Changes screen into "applied, waiting" with a baseline.
func pending(t *testing.T, base postcheck.Result) (*App, *changesScreen) {
	t.Helper()
	a := newApp(t, false)
	c := scr(a, "Changes").(*changesScreen)
	plan := changes.NewPlan(changes.WithRollback, 60)
	c.applied(applyDoneMsg{plan: plan, base: base})
	if c.phase != phasePending {
		t.Fatal("not pending")
	}
	return a, c
}

func confirmCalls(a *App) int {
	n := 0
	for _, call := range a.env.S.T.(*transport.Fake).Calls {
		if strings.Contains(call, ".confirm") {
			n++
		}
	}
	return n
}

func TestSmartConfirmKeepsTheChangeAfterTwoCleanChecks(t *testing.T) {
	a, c := pending(t, healthy)
	id := c.planID
	_, cmd := c.Update(smartResultMsg{id: id, res: healthy})
	if cmd == nil || c.streak != 1 || confirmCalls(a) != 0 {
		t.Fatalf("one clean check must not confirm (streak %d)", c.streak)
	}
	_, cmd = c.Update(smartResultMsg{id: id, res: healthy})
	msg := cmd()
	d, ok := msg.(confirmDoneMsg)
	if !ok || !d.auto || d.err != nil {
		t.Fatalf("second clean check did not confirm: %#v", msg)
	}
	if confirmCalls(a) != 1 {
		t.Fatalf("confirm marker not written: %v", a.env.S.T.(*transport.Fake).Calls)
	}
	c.Update(d)
	if c.phase != phaseIdle || !strings.Contains(c.note, "automatically") {
		t.Fatalf("phase %v note %q", c.phase, c.note)
	}
}

func TestSmartConfirmNeverKeepsABrokenChange(t *testing.T) {
	a, c := pending(t, healthy)
	broken := postcheck.Result{"default_route": "ok", "gateway": "fail", "dns": "ok", "wg_eduvpn": "never"}
	for i := 0; i < 5; i++ {
		c.Update(smartResultMsg{id: c.planID, res: broken})
	}
	if confirmCalls(a) != 0 || c.phase != phasePending {
		t.Fatal("a failing change was confirmed")
	}
	if !strings.Contains(c.note, "gateway reachable") || !strings.Contains(c.note, "WireGuard eduvpn") {
		t.Fatalf("failure not explained: %q", c.note)
	}
	// a flapping router: one clean check between failures is not enough
	c.Update(smartResultMsg{id: c.planID, res: healthy})
	c.Update(smartResultMsg{id: c.planID, res: broken})
	c.Update(smartResultMsg{id: c.planID, res: healthy})
	if confirmCalls(a) != 0 || c.streak != 1 {
		t.Fatalf("streak not reset by a failure (streak %d)", c.streak)
	}
}

func TestSmartConfirmWaitsOutUnreachableRouterAndIgnoresStaleResults(t *testing.T) {
	a, c := pending(t, healthy)
	c.Update(smartResultMsg{id: c.planID, res: healthy})
	c.Update(smartResultMsg{id: c.planID, err: errors.New("connection lost")})
	if c.streak != 0 || !strings.Contains(c.note, "waiting") {
		t.Fatalf("streak %d note %q", c.streak, c.note)
	}
	c.Update(smartResultMsg{id: "chg-other-plan", res: healthy})
	c.Update(smartResultMsg{id: "chg-other-plan", res: healthy})
	if confirmCalls(a) != 0 {
		t.Fatal("a result for another plan confirmed this one")
	}
}

func TestNoBaselineMeansNoAutomaticConfirm(t *testing.T) {
	a, c := pending(t, nil)
	if c.baseline != nil {
		t.Fatal("baseline kept without data")
	}
	c.Update(smartResultMsg{id: c.planID, res: healthy})
	c.Update(smartResultMsg{id: c.planID, res: healthy})
	// with no baseline the screen must not start confirming on its own
	if cmd := c.startSmart(postcheck.Result{"gateway": "fail"}); cmd != nil {
		t.Fatal("started watching with nothing passing before the change")
	}
	_ = a
}

func TestSmartConfirmCanBeTurnedOff(t *testing.T) {
	a := newApp(t, false)
	c := scr(a, "Changes").(*changesScreen)
	c.smart = false
	if cmd := c.startSmart(healthy); cmd != nil {
		t.Fatal("smart confirm ran while switched off")
	}
	c.smart = true
	c.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	if c.smart {
		t.Fatal("h did not toggle")
	}
}
