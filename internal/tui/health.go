package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/discover"
	"github.com/madushakv/owrt-tui/internal/health"
)

// healthDir is where accepted findings are kept (replaced in tests).
var healthDir = discover.ConfigDir

type healthScreen struct {
	e       *env
	list    []health.Finding // what is shown: active findings, or the accepted ones
	active  []health.Finding
	done    []health.Finding // accepted as intended
	acc     health.Accepted
	showAcc bool
	role    health.Role // the role the checks used
	pinned  health.Role // the user's choice (auto = detected)
	cur     int
	loading bool
	err     error
	open    bool
}

func newHealth(e *env) *healthScreen { return &healthScreen{e: e, loading: true} }

func (h *healthScreen) Title() string   { return "Health" }
func (h *healthScreen) Capturing() bool { return false }
func (h *healthScreen) Keys() string {
	if h.showAcc {
		return "enter details  a stop accepting  A back to findings"
	}
	return "enter details  f fix  a mark as intended  A accepted  r device role  g re-check"
}

type healthMsg struct {
	list         []health.Finding
	err          error
	role, pinned health.Role
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
		pinned := health.LoadRole(healthDir(), h.e.S.Host())
		list, role := health.CheckRole(health.Input{Configs: cfgs, Facts: facts, Role: pinned})
		return healthMsg{list: list, role: role, pinned: pinned}
	})
}

func (h *healthScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case healthMsg:
		h.loading, h.err = false, m.err
		h.role, h.pinned = m.role, m.pinned
		h.acc = health.LoadAccepted(healthDir(), h.e.S.Host())
		h.active, h.done = health.Split(m.list, h.acc)
		h.pick()
		if m.err == nil {
			h.e.alertBad, h.e.alertWarn = 0, 0
			for _, f := range h.active {
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

func (h *healthScreen) roleText() string {
	if h.pinned == health.RoleAuto {
		return h.role.Label() + " (auto, r to change)"
	}
	return h.role.Label() + " (r to change)"
}

func (h *healthScreen) pick() {
	h.list = h.active
	if h.showAcc {
		h.list = h.done
	}
	h.cur = clampInt(h.cur, 0, len(h.list)-1)
}

func (h *healthScreen) key(k string) (screen, tea.Cmd) {
	switch k {
	case "up", "k":
		h.cur = clampInt(h.cur-1, 0, len(h.list)-1)
	case "down", "j":
		h.cur = clampInt(h.cur+1, 0, len(h.list)-1)
	case "enter":
		h.open = !h.open
	case "r":
		next := health.RoleAuto
		for i, r := range health.Roles {
			if r == h.pinned {
				next = health.Roles[(i+1)%len(health.Roles)]
			}
		}
		if err := health.SaveRole(healthDir(), h.e.S.Host(), next); err != nil {
			return h, toastErr(err)
		}
		h.loading = true
		return h, h.load()
	case "A":
		h.showAcc, h.cur, h.open = !h.showAcc, 0, false
		h.pick()
	case "a":
		if len(h.list) == 0 {
			return h, nil
		}
		f := h.list[h.cur]
		if h.showAcc {
			delete(h.acc, f.ID)
		} else {
			h.acc[f.ID] = health.Fingerprint(f)
		}
		if err := health.SaveAccepted(healthDir(), h.e.S.Host(), h.acc); err != nil {
			return h, toastErr(err)
		}
		all := append(append([]health.Finding{}, h.active...), h.done...)
		h.active, h.done = health.Split(all, h.acc)
		h.pick()
		h.e.alertBad, h.e.alertWarn = 0, 0
		for _, x := range h.active {
			switch x.Sev {
			case health.Bad:
				h.e.alertBad++
			case health.Warn:
				h.e.alertWarn++
			}
		}
		if h.showAcc {
			return h, toast("no longer accepted: it is back in the findings")
		}
		return h, toast("marked as intended; it returns if the finding changes (A shows the accepted list)")
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
		if h.showAcc {
			return "\n  " + sDim.Render("nothing has been marked as intended")
		}
		msg := "\n  " + sGood.Render("no problems found") + sDim.Render("  (role: "+h.roleText()+")")
		if len(h.done) > 0 {
			msg += "\n  " + sDim.Render(fmt.Sprintf("%d finding(s) marked as intended (A to review)", len(h.done)))
		}
		return msg
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
	head := fmt.Sprintf("  %d problem(s), %d warning(s), %d note(s)", nb, nw, len(h.list)-nb-nw)
	head += "  ·  role: " + h.roleText()
	if h.showAcc {
		head = fmt.Sprintf("  %d finding(s) you marked as intended", len(h.list))
	} else if len(h.done) > 0 {
		head += fmt.Sprintf("  ·  %d marked as intended (A)", len(h.done))
	}
	b.WriteString(sDim.Render(head) + "\n")
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
