package tui

import (
	"strings"
	"testing"
)

func TestHealthScreenShowsDemoFindingsAndStagesFix(t *testing.T) {
	a := writeApp(t)
	withStore(t)
	openTab(t, a, "Health")
	v := view(a)
	for _, want := range []string{"repeats entries", "guest", "SSH accepts passwords"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	h := scr(a, "Health").(*healthScreen)
	for i, f := range h.list {
		if strings.HasPrefix(f.ID, "dup-list") {
			h.cur = i
		}
	}
	press(t, a, "f")
	if a.env.Staged.Len() != 3 { // delete + two add_list... wan x3 collapses to one value => delete + 1 add
		// zone 'wan' has the single distinct value 'wan': delete + add_list
		if a.env.Staged.Len() != 2 {
			t.Fatalf("staged %d", a.env.Staged.Len())
		}
	}
	if a.screens[a.active].Title() != "Changes" {
		t.Fatalf("did not jump to Changes, on %s", a.screens[a.active].Title())
	}
}

func TestHealthFixRefusedReadOnly(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Health")
	h := scr(a, "Health").(*healthScreen)
	for i, f := range h.list {
		if f.Fix != nil {
			h.cur = i
		}
	}
	press(t, a, "f")
	if a.env.Staged.Len() != 0 {
		t.Fatal("read-only session staged a fix")
	}
}
