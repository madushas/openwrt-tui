package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/health"
)

type healthScreen struct {
	e       *env
	list    []health.Finding
	cur     int
	loading bool
	err     error
	open    bool
}

func newHealth(e *env) *healthScreen { return &healthScreen{e: e, loading: true} }

func (h *healthScreen) Title() string   { return "Health" }
func (h *healthScreen) Capturing() bool { return false }
func (h *healthScreen) Keys() string {
	return "enter details  f stage the fix  g re-check"
}

type healthMsg struct {
	list []health.Finding
	err  error
}

func (h *healthScreen) Init() tea.Cmd { return h.load() }

func (h *healthScreen) load() tea.Cmd {
	return h.e.call(func(ctx context.Context) tea.Msg {
		cfgs, err := h.e.loadUCI(ctx)
		if err != nil {
			return healthMsg{err: err}
		}
		facts, err := h.e.S.HealthFacts(ctx)
		if err != nil {
			facts.SSHKeys = -1 // unknown: rules that depend on it stay quiet
		}
		return healthMsg{list: health.Check(health.Input{Configs: cfgs, Facts: facts})}
	})
}

func (h *healthScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case healthMsg:
		h.loading, h.err, h.list = false, m.err, m.list
		if m.err == nil {
			h.e.alertBad, h.e.alertWarn = 0, 0
			for _, f := range m.list {
				switch f.Sev {
				case health.Bad:
					h.e.alertBad++
				case health.Warn:
					h.e.alertWarn++
				}
			}
		}
		h.cur = clampInt(h.cur, 0, len(h.list)-1)
	case uciChangedMsg:
		return h, h.load()
	case tea.KeyPressMsg:
		return h.key(m.String())
	}
	return h, nil
}

func (h *healthScreen) key(k string) (screen, tea.Cmd) {
	switch k {
	case "up", "k":
		h.cur = clampInt(h.cur-1, 0, len(h.list)-1)
	case "down", "j":
		h.cur = clampInt(h.cur+1, 0, len(h.list)-1)
	case "enter":
		h.open = !h.open
	case "g":
		h.loading = true
		h.e.invalidate()
		return h, h.load()
	case "f":
		if len(h.list) == 0 {
			return h, nil
		}
		f := h.list[h.cur]
		if f.Fix == nil {
			return h, toast("no automatic fix for this one; see the details (enter)")
		}
		if h.e.S.ReadOnly {
			return h, toast("read-only mode: start with -write to stage fixes")
		}
		for _, c := range f.Fix {
			if err := h.e.Staged.Add(c); err != nil {
				return h, toastErr(err)
			}
		}
		return h, tea.Batch(toast(fmt.Sprintf("staged %d changes; review and apply", len(f.Fix))),
			func() tea.Msg { return gotoMsg{"Changes"} })
	}
	return h, nil
}

func (h *healthScreen) View(w, ht int) string {
	if h.loading && len(h.list) == 0 {
		return "\n  checking..."
	}
	if h.err != nil {
		return "\n  " + sBad.Render("error: "+h.err.Error())
	}
	if len(h.list) == 0 {
		return "\n  " + sGood.Render("no problems found") + sDim.Render("  (configuration and runtime checks)")
	}
	var b strings.Builder
	var nb, nw int
	for _, f := range h.list {
		switch f.Sev {
		case health.Bad:
			nb++
		case health.Warn:
			nw++
		}
	}
	b.WriteString(sDim.Render(fmt.Sprintf("  %d problem(s), %d warning(s), %d note(s)", nb, nw, len(h.list)-nb-nw)) + "\n")
	detail := 0
	if h.open {
		detail = 5
	}
	lo, hi := window(len(h.list), h.cur, ht-2-detail)
	for i := lo; i < hi; i++ {
		f := h.list[i]
		tag := map[health.Severity]string{health.Bad: sBad.Render("PROBLEM"), health.Warn: sWarn.Render("WARN   "), health.Info: sDim.Render("note   ")}[f.Sev]
		fix := " "
		if f.Fix != nil {
			fix = "f"
		}
		line := fmt.Sprintf("  %s %s  %s", tag, fix, f.Title)
		if i == h.cur {
			plain := map[health.Severity]string{health.Bad: "PROBLEM", health.Warn: "WARN   ", health.Info: "note   "}[f.Sev]
			line = sSel.Render(clip(fmt.Sprintf("  %s %s  %s", plain, fix, f.Title), w))
		} else {
			line = clip(line, w)
		}
		b.WriteString(line + "\n")
	}
	if h.open {
		f := h.list[h.cur]
		b.WriteString("\n")
		for _, l := range wrap(f.Detail, w-6) {
			b.WriteString("    " + l + "\n")
		}
		if f.FixNote != "" {
			b.WriteString("    " + sGood.Render("fix: "+f.FixNote) + "\n")
		}
	}
	return b.String()
}
