package tui

import (
	"strings"
	"testing"
)

func TestGuestWizardStagesAndNeverShowsOrStoresThePassword(t *testing.T) {
	st := withStore(t)
	a := writeApp(t)
	openTab(t, a, "Tasks")
	press(t, a, "enter")
	ts := scr(a, "Tasks").(*tasksScreen)
	if ts.form == nil {
		t.Fatal("wizard did not open")
	}
	for _, f := range ts.form.fields {
		if f.Key == "key" {
			f.Value = "very-secret-pass"
		}
	}
	press(t, a, "s")
	if a.screens[a.active].Title() != "Changes" {
		t.Fatalf("on %s (form errors?)", a.screens[a.active].Title())
	}
	if a.env.Staged.Len() < 20 {
		t.Fatalf("staged %d", a.env.Staged.Len())
	}
	if v := view(a); strings.Contains(v, "very-secret-pass") {
		t.Fatalf("password on the changes screen:\n%s", v)
	}
	ent := a.env.snapshot(a.env.ctx)
	for _, l := range ent.Summary {
		if strings.Contains(l, "very-secret-pass") {
			t.Fatalf("password in history summary: %s", l)
		}
	}
	_ = st
}

func TestWizardsRefusedReadOnlyAndDNSWizard(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Tasks")
	press(t, a, "enter")
	if scr(a, "Tasks").Capturing() {
		t.Fatal("wizard opened read-only")
	}
	w := writeApp(t)
	openTab(t, w, "Tasks")
	press(t, w, "j", "j", "enter", "s")
	if w.env.Staged.Len() == 0 {
		t.Fatal("DNS wizard staged nothing")
	}
}
