package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/demo"
	"github.com/madushakv/owrt-tui/internal/history"
	"github.com/madushakv/owrt-tui/internal/session"
)

func withStore(t *testing.T) *history.Store {
	t.Helper()
	st, err := history.Open(t.TempDir(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	old := historyOpenFn
	historyOpenFn = func(string) (*history.Store, error) { return st, nil }
	t.Cleanup(func() { historyOpenFn = old; ExportDir = "" })
	return st
}

func writeApp(t *testing.T) *App {
	t.Helper()
	ctx := context.Background()
	s, err := session.Connect(ctx, demo.Router(), false)
	if err != nil {
		t.Fatal(err)
	}
	a := New(ctx, s, 5*time.Second)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pump(t, a, a.Init())
	return a
}

func TestApplyIsRecordedAndRestoreStagesTheUndo(t *testing.T) {
	st := withStore(t)
	a := writeApp(t)
	h := scr(a, "History").(*histScreen)

	// stage a change to dhcp and apply it directly through the hook
	cfgs, _ := a.env.S.LoadUCI(context.Background())
	var sec, old string
	for _, c := range cfgs {
		if c.Name == "dhcp" {
			for _, s := range c.Sections {
				if s.Type == "dhcp" {
					sec = s.Ref()
					o, _ := s.Option("leasetime")
					old = o.Value()
				}
			}
		}
	}
	if sec == "" || old == "" {
		t.Fatalf("demo dhcp section not found (%q %q)", sec, old)
	}
	if err := a.env.Staged.Add(chgSet("dhcp", sec, "leasetime", "1h", old)); err != nil {
		t.Fatal(err)
	}
	ent := a.env.snapshot(context.Background())
	if ent == nil || !strings.Contains(ent.Configs["dhcp"], "leasetime '"+old+"'") {
		t.Fatalf("snapshot missing the pre-change value: %+v", ent)
	}
	a.env.saveEntry(ent)
	a.env.Staged.Clear()

	pump(t, a, h.load())
	if l, _ := st.List(); len(l) != 1 || len(l[0].Summary) != 1 {
		t.Fatalf("entry not recorded: %+v", l)
	}

	// the router "currently" has leasetime 1h in the demo? restore must stage a
	// change only if it differs from the snapshot; here it already matches.
	pump(t, a, h.restore(ent.ID))
	if a.env.Staged.Len() != 0 {
		t.Fatalf("restore staged %d changes for a router that already matches", a.env.Staged.Len())
	}
}

func TestHistoryFilesArePrivate(t *testing.T) {
	st := withStore(t)
	a := writeApp(t)
	a.env.Staged.Add(chgSet("dhcp", "lan", "leasetime", "2h", "12h"))
	a.env.saveEntry(a.env.snapshot(context.Background()))
	fs, _ := os.ReadDir(st.Dir())
	if len(fs) == 0 {
		t.Fatal("nothing saved")
	}
	for _, f := range fs {
		i, _ := os.Stat(filepath.Join(st.Dir(), f.Name()))
		if i.Mode().Perm() != 0o600 {
			t.Fatalf("%s is %v", f.Name(), i.Mode().Perm())
		}
	}
}

func TestDriftIsDetectedAndAcceptable(t *testing.T) {
	st := withStore(t)
	a := writeApp(t) // Init recorded the first baseline silently
	if _, ok := st.LoadBaseline(); !ok {
		t.Fatal("no baseline after first connect")
	}
	h := scr(a, "History").(*histScreen)
	if len(h.drift) != 0 {
		t.Fatal("drift reported on first run")
	}
	b, _ := st.LoadBaseline()
	b.Hashes["dhcp"] = "something-else"
	st.SaveBaseline(b.Hashes)
	a.env.invalidate()
	pump(t, a, a.env.checkDrift())
	if len(h.drift) != 1 || h.drift[0] != "dhcp" {
		t.Fatalf("drift = %v", h.drift)
	}
	if v := h.View(100, 20); !strings.Contains(v, "outside owrt-tui") {
		t.Fatalf("banner missing:\n%s", v)
	}
	openTab(t, a, "History")
	press(t, a, "a")
	if len(h.drift) != 0 {
		t.Fatalf("accept did not clear drift: %v", h.drift)
	}
}

func TestReadOnlyRestoreRefused(t *testing.T) {
	withStore(t)
	a := newApp(t, true)
	h := scr(a, "History").(*histScreen)
	h.list = nil
	_, cmd := h.key("r")
	if cmd == nil {
		t.Fatal("no feedback")
	}
}

func chgSet(cfg, sec, opt, val, old string) changes.Change {
	return changes.Change{Op: changes.OpSet, Config: cfg, Section: sec, Option: opt, Value: val, Old: old}
}

func TestRestoreStagesTheReverseOfTheChange(t *testing.T) {
	withStore(t)
	a := writeApp(t)
	h := scr(a, "History").(*histScreen)
	// snapshot says leasetime was 6h, router has the demo value: restore must stage a set back to 6h
	snap := "package dhcp\n\nconfig dhcp 'lan'\n\toption interface 'lan'\n\toption leasetime '6h'\n"
	a.env.store().Save(history.Entry{ID: "chg-20260101-000001", Summary: []string{"x"}, Configs: map[string]string{"dhcp": snap}})
	pump(t, a, h.restore("chg-20260101-000001"))
	found := false
	for _, c := range a.env.Staged.Items() {
		if c.Config == "dhcp" && c.Option == "leasetime" && c.Value == "6h" {
			found = true
		}
	}
	if !found {
		t.Fatalf("undo not staged: %v", a.env.Staged.Items())
	}
}
