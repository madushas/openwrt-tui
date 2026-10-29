package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/discover"
	"github.com/madushakv/owrt-tui/internal/gitexport"
	"github.com/madushakv/owrt-tui/internal/history"
	"github.com/madushakv/owrt-tui/internal/model"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// ExportDir, when set, makes every apply also commit the masked configuration
// to a git repository there.
var ExportDir string

// historyOpenFn is replaced in tests so they never touch the real config dir.
var historyOpenFn = func(router string) (*history.Store, error) {
	return history.Open(discover.ConfigDir(), router)
}

// store opens this router's history once; nil when it cannot be opened.
func (e *env) store() *history.Store {
	e.histOnce.Do(func() {
		s, err := historyOpenFn(e.S.Host())
		if err == nil {
			e.hist = s
		}
	})
	return e.hist
}

// snapshot captures the pre-change text of every config the staged set
// touches. Best effort: no snapshot only means the change cannot be undone.
func (e *env) snapshot(ctx context.Context) *history.Entry {
	if e.store() == nil {
		return nil
	}
	cfgs, err := e.S.LoadUCI(ctx)
	if err != nil {
		return nil
	}
	ent := &history.Entry{
		ID:      "chg-" + time.Now().UTC().Format("20060102-150405"),
		Time:    time.Now().UTC(),
		Configs: map[string]string{},
	}
	for _, it := range e.Staged.Items() {
		ent.Summary = append(ent.Summary, it.Describe())
	}
	for _, name := range e.Staged.Configs() {
		for _, c := range cfgs {
			if c.Name == name {
				ent.Configs[name] = model.Render(c, false)
			}
		}
	}
	return ent
}

func (e *env) saveEntry(ent *history.Entry) {
	if ent != nil && e.store() != nil {
		_ = e.store().Save(*ent)
	}
}

// ---- state sync: baseline + git export ----

type syncDoneMsg struct{ err error }

// syncState records the router's current state as the known one and, if
// asked, commits the masked configs to the export repository.
func (e *env) syncState() tea.Cmd {
	return e.call(func(ctx context.Context) tea.Msg {
		cfgs, err := e.S.LoadUCI(ctx)
		if err != nil {
			return syncDoneMsg{nil} // nothing to record; not worth an error
		}
		if s := e.store(); s != nil {
			_ = s.SaveBaseline(model.Hashes(cfgs))
		}
		if ExportDir != "" {
			if _, err := gitexport.Export(ExportDir, e.S.Host(), maskedFiles(cfgs), "owrt-tui: configuration changed"); err != nil {
				return syncDoneMsg{fmt.Errorf("git export: %w", err)}
			}
		}
		return syncDoneMsg{}
	})
}

func maskedFiles(cfgs []uci.Config) map[string]string {
	out := map[string]string{}
	for _, c := range cfgs {
		out[c.Name] = model.Render(c, true)
	}
	return out
}

type driftMsg struct{ names []string }

// checkDrift compares the router with the last known state. The first run
// only records one.
func (e *env) checkDrift() tea.Cmd {
	return e.call(func(ctx context.Context) tea.Msg {
		s := e.store()
		if s == nil {
			return nil
		}
		cfgs, err := e.loadUCI(ctx)
		if err != nil {
			return nil
		}
		now := model.Hashes(cfgs)
		base, ok := s.LoadBaseline()
		if !ok {
			_ = s.SaveBaseline(now)
			return nil
		}
		return driftMsg{model.Changed(base.Hashes, now)}
	})
}

// ---- screen ----

type gotoMsg struct{ title string }

type histScreen struct {
	e       *env
	list    []history.Entry
	cur     int
	detail  bool
	err     error
	drift   []string
	loadErr string
}

func newHistory(e *env) *histScreen { return &histScreen{e: e} }

func (h *histScreen) Title() string   { return "History" }
func (h *histScreen) Capturing() bool { return false }
func (h *histScreen) Keys() string {
	k := "enter details  r restore this change"
	if len(h.drift) > 0 {
		k += "  a accept outside changes"
	}
	return k + "  g reload"
}

type histMsg struct {
	list []history.Entry
	err  error
}

func (h *histScreen) Init() tea.Cmd { return h.load() }

func (h *histScreen) load() tea.Cmd {
	return func() tea.Msg {
		s := h.e.store()
		if s == nil {
			return histMsg{err: errors.New("history is unavailable (cannot open the config directory)")}
		}
		l, err := s.List()
		return histMsg{l, err}
	}
}

type restoreMsg struct {
	n   int
	err error
}

func (h *histScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case histMsg:
		h.list, h.err = m.list, m.err
		h.cur = clampInt(h.cur, 0, len(h.list)-1)
	case driftMsg:
		h.drift = m.names
		if len(m.names) > 0 {
			return h, toast("changed outside owrt-tui: " + strings.Join(m.names, ", ") + " (see History)")
		}
	case syncDoneMsg:
		if m.err != nil {
			return h, toastErr(m.err)
		}
		h.drift = nil
		return h, h.load()
	case restoreMsg:
		if m.err != nil {
			return h, toastErr(m.err)
		}
		if m.n == 0 {
			return h, toast("nothing to restore: the router already matches that snapshot")
		}
		return h, tea.Batch(toast(fmt.Sprintf("staged %d changes to undo it; review and apply", m.n)),
			func() tea.Msg { return gotoMsg{"Changes"} })
	case tea.KeyPressMsg:
		return h.key(m.String())
	}
	return h, nil
}

func (h *histScreen) key(k string) (screen, tea.Cmd) {
	switch k {
	case "up", "k":
		h.cur = clampInt(h.cur-1, 0, len(h.list)-1)
	case "down", "j":
		h.cur = clampInt(h.cur+1, 0, len(h.list)-1)
	case "enter":
		h.detail = !h.detail
	case "g":
		return h, h.load()
	case "a":
		if len(h.drift) > 0 {
			return h, h.e.syncState()
		}
	case "r":
		if h.e.S.ReadOnly {
			return h, toast("read-only mode: start with -write to restore")
		}
		if len(h.list) == 0 {
			return h, nil
		}
		return h, h.restore(h.list[h.cur].ID)
	}
	return h, nil
}

// restore stages whatever it takes to bring the touched configs back to the
// snapshot. It stages rather than applies, so the usual preflight, rollback
// timer and smart confirm all protect the undo too.
func (h *histScreen) restore(id string) tea.Cmd {
	return h.e.call(func(ctx context.Context) tea.Msg {
		ent, err := h.e.store().Load(id)
		if err != nil {
			return restoreMsg{err: err}
		}
		cur, err := h.e.S.LoadUCI(ctx)
		if err != nil {
			return restoreMsg{err: err}
		}
		target := append([]uci.Config(nil), cur...)
		for name, text := range ent.Configs {
			old, err := uci.ParseExport(text)
			if err != nil || len(old) != 1 {
				return restoreMsg{err: fmt.Errorf("snapshot of %s is damaged", name)}
			}
			found := false
			for i := range target {
				if target[i].Name == name {
					target[i], found = old[0], true
				}
			}
			if !found {
				target = append(target, old[0])
			}
		}
		n := 0
		for _, c := range model.Diff(cur, target) {
			if err := h.e.Staged.Add(c); err != nil {
				return restoreMsg{err: err}
			}
			n++
		}
		return restoreMsg{n: n}
	})
}

func (h *histScreen) View(w, ht int) string {
	var b strings.Builder
	if len(h.drift) > 0 {
		b.WriteString("  " + sWarn.Render(clip("changed outside owrt-tui since last seen: "+strings.Join(h.drift, ", "), w-4)) + "\n")
		ht--
	}
	if h.err != nil {
		return b.String() + "\n  " + sBad.Render("error: "+h.err.Error())
	}
	if len(h.list) == 0 {
		return b.String() + "\n  " + sDim.Render("no applied changes yet. Every apply is recorded here and can be undone.")
	}
	lo, hi := window(len(h.list), h.cur, ht-1)
	for i := lo; i < hi; i++ {
		e := h.list[i]
		first := ""
		if len(e.Summary) > 0 {
			first = e.Summary[0]
		}
		line := fmt.Sprintf("  %s  %2d change(s)  %s", e.Time.Local().Format("2006-01-02 15:04"), len(e.Summary), first)
		if i == h.cur {
			line = sSel.Render(clip(line, w))
		} else {
			line = clip(line, w)
		}
		b.WriteString(line + "\n")
	}
	if h.detail {
		b.WriteString("\n")
		for _, s := range h.list[h.cur].Summary {
			b.WriteString("    " + clip(s, w-6) + "\n")
		}
	}
	return b.String()
}
