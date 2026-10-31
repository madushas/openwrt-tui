package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var layoutSizes = [][2]int{{40, 8}, {50, 12}, {60, 16}, {80, 24}, {100, 30}, {120, 40}, {160, 50}, {240, 60}}

// checkFrame reports layout faults in one rendered frame.
func checkFrame(a *App, w, h int) []string {
	a.w, a.h = w, h
	out := plain(a.render())
	lines := strings.Split(out, "\n")
	var bad []string
	if len(lines) > h {
		bad = append(bad, fmt.Sprintf("%d lines for height %d", len(lines), h))
	}
	for i, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			bad = append(bad, fmt.Sprintf("line %d is %d wide (max %d): %q", i, n, w, l))
			break
		}
	}
	return bad
}

// Every screen, at every size from the smallest supported to a huge terminal,
// must fit in the terminal: no line wider than it, no more lines than it has.
func TestEveryScreenFitsEverySize(t *testing.T) {
	a := newApp(t, false)
	pump(t, a, a.Init())
	for _, s := range a.screens {
		openTab(t, a, s.Title())
		for _, sz := range layoutSizes {
			if bad := checkFrame(a, sz[0], sz[1]); len(bad) > 0 {
				t.Errorf("%s at %dx%d: %s", s.Title(), sz[0], sz[1], strings.Join(bad, "; "))
			}
		}
	}
}

// Same for each sub-tab of the Network screen and each wizard, including the
// form each one opens.
func TestWizardsAndSubTabsFit(t *testing.T) {
	a := newApp(t, false)
	for i, name := range netTabs {
		toTab(t, a, name)
		_ = i
		for _, sz := range layoutSizes {
			if bad := checkFrame(a, sz[0], sz[1]); len(bad) > 0 {
				t.Errorf("Network/%s at %dx%d: %s", name, sz[0], sz[1], strings.Join(bad, "; "))
			}
		}
	}
	for _, it := range taskList {
		b := newApp(t, false)
		withStore(t)
		startTask(t, b, it.key)
		for _, sz := range layoutSizes {
			if bad := checkFrame(b, sz[0], sz[1]); len(bad) > 0 {
				t.Errorf("task %q at %dx%d: %s", it.key, sz[0], sz[1], strings.Join(bad, "; "))
			}
		}
	}
}

// The help overlay, palette and a staged-changes header must fit too.
func TestOverlaysFit(t *testing.T) {
	a := newApp(t, false)
	a.env.alertBad, a.env.alertWarn = 3, 4
	a.env.Staged.Add(chgSet("dhcp", "lan", "leasetime", "6h", "12h"))
	for _, ov := range []string{"?", ":"} {
		press(t, a, ov)
		for _, sz := range layoutSizes {
			if bad := checkFrame(a, sz[0], sz[1]); len(bad) > 0 {
				t.Errorf("overlay %q at %dx%d: %s", ov, sz[0], sz[1], strings.Join(bad, "; "))
			}
		}
		press(t, a, "esc")
	}
}

// A form taller than the terminal scrolls so the selected field stays visible,
// and a long hint wraps instead of being cut off.
func TestFormScrollsToSelectedField(t *testing.T) {
	var fs []*field
	for i := 0; i < 30; i++ {
		fs = append(fs, &field{Label: fmt.Sprintf("Field %02d", i), Kind: kText, Hint: strings.Repeat("a long hint ", 8)})
	}
	f := newForm("Tall form", fs...)
	for _, cur := range []int{0, 5, 17, 29} {
		f.cur = cur
		out := plain(f.View(60, 14))
		if !strings.Contains(out, fmt.Sprintf("Field %02d", cur)) {
			t.Fatalf("selected field %d not visible:\n%s", cur, out)
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) > 14 {
			t.Fatalf("%d lines in 14 rows:\n%s", len(lines), out)
		}
		if !strings.Contains(out, "enter edit/toggle") {
			t.Fatalf("footer cut off at field %d:\n%s", cur, out)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > 60 {
				t.Fatalf("line too wide: %q", l)
			}
		}
	}
	f.cur = 3
	if out := plain(f.View(60, 14)); strings.Count(out, "a long hint") < 4 {
		t.Fatalf("hint not wrapped over several lines:\n%s", out)
	}
}

// Footer hints are cut at hint boundaries and "? help" always survives.
func TestFooterKeepsHelpAndNeverCutsMidHint(t *testing.T) {
	keys := "n name  s make static  b block/unblock  v route via VPN/WAN  k kick  w wake  t sort by traffic  g reload"
	for w := 24; w <= 140; w += 4 {
		out := plain(footerHints(keys, w))
		if ansi.StringWidth(out) > w {
			t.Fatalf("width %d: %d wide: %q", w, ansi.StringWidth(out), out)
		}
		if !strings.Contains(out, "? help") {
			t.Fatalf("width %d lost help: %q", w, out)
		}
		for _, bad := range []string{"unblo ", "sort by traff ", "make stat "} {
			if strings.Contains(out, bad) {
				t.Fatalf("width %d cut mid-hint: %q", w, out)
			}
		}
	}
	if out := plain(footerHints(keys, 70)); !strings.Contains(out, "n name") || !strings.Contains(out, "? help") {
		t.Fatalf("at 70 columns the first hints should still show: %q", out)
	}
	if out := plain(footerHints(keys, 200)); !strings.Contains(out, "g reload") || strings.Contains(out, "…") {
		t.Fatalf("wide footer should show everything: %q", out)
	}
}

// An apply prompt must always show how to answer it, however long it is.
func TestAskKeepsAnswerKeysVisible(t *testing.T) {
	a := newApp(t, false)
	a.ask = &askMsg{Prompt: strings.Repeat("Delete the interface and everything related to it? ", 4)}
	for _, w := range []int{40, 80, 120} {
		if f := plain(a.footer(w)); !strings.Contains(f, "y yes") || ansi.StringWidth(f) > w {
			t.Fatalf("width %d: %q", w, f)
		}
	}
}

// Client columns appear whole or not at all; none is ever cut in half.
func TestClientColumnsAreNeverCut(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "Clients")
	press(t, a, "g") // a second traffic sample, so rates show
	cs := scr(a, "Clients").(*clientsScreen)
	for w := 50; w <= 160; w += 2 {
		lines := strings.Split(plain(cs.View(w, 12)), "\n")
		head := lines[0]
		for _, col := range []string{"TRAFFIC", "FLAGS", "MAC", "LINK", "ADDRESS"} {
			if i := strings.Index(head, col[:2]); i >= 0 && !strings.Contains(head, col) {
				// "MA" can start "MAC" only; "LI" "AD" "FL" "TR" likewise
				t.Fatalf("width %d: column %s cut in half: %q", w, col, head)
			}
		}
		if w >= 80 && !strings.Contains(head, "TRAFFIC") {
			t.Fatalf("width %d: traffic column missing: %q", w, head)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Fatalf("width %d: row too wide: %q", w, l)
			}
		}
	}
}
