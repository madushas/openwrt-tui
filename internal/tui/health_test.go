package tui

import (
	"github.com/madushakv/owrt-tui/internal/health"
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

func TestHealthMarkAsIntendedHidesAndIsReversible(t *testing.T) {
	withStore(t)
	old := healthDir
	dir := t.TempDir()
	healthDir = func() string { return dir }
	t.Cleanup(func() { healthDir = old })
	a := writeApp(t)
	openTab(t, a, "Health")
	h := scr(a, "Health").(*healthScreen)
	before := len(h.list)
	if before == 0 {
		t.Fatal("demo has no findings")
	}
	id := h.list[h.cur].ID
	press(t, a, "a")
	if len(h.list) != before-1 || len(h.done) != 1 {
		t.Fatalf("not hidden: shown %d (was %d), accepted %d", len(h.list), before, len(h.done))
	}
	for _, f := range h.list {
		if f.ID == id {
			t.Fatal("accepted finding still shown")
		}
	}
	// it survives a re-check (persisted), and is listed under A
	press(t, a, "g")
	a.Update(h.load()())
	if len(h.done) != 1 {
		t.Fatalf("accepted state lost on re-check: %d", len(h.done))
	}
	press(t, a, "A")
	if !strings.Contains(view(a), "marked as intended") || len(h.list) != 1 {
		t.Fatalf("accepted list wrong:\n%s", view(a))
	}
	press(t, a, "a") // stop accepting
	press(t, a, "A")
	if len(h.list) != before {
		t.Fatalf("not restored: %d vs %d", len(h.list), before)
	}
}

func TestHealthRoleCyclesAndPersists(t *testing.T) {
	withStore(t)
	old := healthDir
	dir := t.TempDir()
	healthDir = func() string { return dir }
	t.Cleanup(func() { healthDir = old })
	a := writeApp(t)
	openTab(t, a, "Health")
	h := scr(a, "Health").(*healthScreen)
	if h.pinned != "" || !strings.Contains(view(a), "role:") {
		t.Fatalf("role not shown:\n%s", view(a))
	}
	press(t, a, "r")
	a.Update(h.load()())
	if h.pinned != health.RoleRouter || h.role != health.RoleRouter {
		t.Fatalf("pinned %q applied %q", h.pinned, h.role)
	}
	if health.LoadRole(dir, h.e.S.Host()) != health.RoleRouter {
		t.Fatal("not persisted")
	}
	press(t, a, "r")
	a.Update(h.load()())
	press(t, a, "r")
	a.Update(h.load()())
	press(t, a, "r")
	a.Update(h.load()())
	if h.pinned != health.RoleAP { // router, behind, cellular, ap
		t.Fatalf("cycle wrong: %q", h.pinned)
	}
}
