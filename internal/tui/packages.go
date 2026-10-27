package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/pkgmgr"
)

const slowOp = 5 * time.Minute

type packagesScreen struct {
	e       *env
	mode    string // installed, upgradable, search
	list    []pkgmgr.Package
	cur     int
	loading bool
	err     error
	detail  string
	input   textinput.Model
	typing  bool
	query   string
}

func newPackages(e *env) *packagesScreen {
	in := textinput.New()
	in.Prompt = "search: "
	return &packagesScreen{e: e, mode: "installed", loading: true, input: in}
}

func (p *packagesScreen) Title() string   { return "Packages" }
func (p *packagesScreen) Capturing() bool { return p.typing }
func (p *packagesScreen) Keys() string {
	return "i installed  u upgradable  / search  enter info  + install  - remove  U update index"
}

type pkgListMsg struct {
	mode string
	list []pkgmgr.Package
	err  error
}
type pkgTextMsg struct {
	text string
	err  error
}
type pkgDoneMsg struct {
	what string
	out  string
	err  error
}

func (p *packagesScreen) Init() tea.Cmd { return p.load() }

func (p *packagesScreen) load() tea.Cmd {
	mode, q := p.mode, p.query
	p.loading = true
	return p.e.callFor(slowOp, func(ctx context.Context) tea.Msg {
		var l []pkgmgr.Package
		var err error
		switch mode {
		case "upgradable":
			l, err = p.e.S.PkgUpgradable(ctx)
		case "search":
			l, err = p.e.S.PkgSearch(ctx, q)
		default:
			l, err = p.e.S.PkgInstalled(ctx)
		}
		return pkgListMsg{mode, l, err}
	})
}

func (p *packagesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case pkgListMsg:
		if m.mode != p.mode {
			return p, nil // a stale reply for a tab the user already left
		}
		p.loading, p.err = false, m.err
		if m.err == nil {
			p.list = m.list
			p.cur = clampInt(p.cur, 0, len(p.list)-1)
		}
	case pkgTextMsg:
		if m.err != nil {
			p.detail = sBad.Render("error: " + m.err.Error())
		} else {
			p.detail = m.text
		}
	case pkgPlanMsg:
		p.detail = m.text
		verb := "Install"
		if m.remove {
			verb = "Remove"
		}
		return p, func() tea.Msg {
			return askMsg{Prompt: verb + " " + m.name + " on the router?", Danger: m.remove, Yes: func() tea.Cmd {
				return p.e.callFor(slowOp, func(ctx context.Context) tea.Msg {
					var out string
					var err error
					if m.remove {
						out, err = p.e.S.PkgRemove(ctx, m.name)
					} else {
						out, err = p.e.S.PkgInstall(ctx, m.name)
					}
					return pkgDoneMsg{strings.ToLower(verb) + " " + m.name, out, err}
				})
			}}
		}
	case pkgDoneMsg:
		if m.err != nil {
			p.detail = m.out
			return p, toastErr(fmt.Errorf("%s failed: %w", m.what, m.err))
		}
		p.detail = m.out
		return p, tea.Batch(toast(m.what+": done"), p.load())
	case tea.KeyPressMsg:
		return p.key(m)
	}
	return p, nil
}

func (p *packagesScreen) cursorName() string {
	if p.cur < 0 || p.cur >= len(p.list) {
		return ""
	}
	return p.list[p.cur].Name
}

func (p *packagesScreen) key(m tea.KeyPressMsg) (screen, tea.Cmd) {
	if p.typing {
		switch m.String() {
		case "esc":
			p.typing = false
			p.input.Blur()
			return p, nil
		case "enter":
			p.typing = false
			p.input.Blur()
			if q := strings.TrimSpace(p.input.Value()); q != "" {
				p.mode, p.query, p.cur, p.detail = "search", q, 0, ""
				return p, p.load()
			}
			return p, nil
		}
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(m)
		return p, cmd
	}
	switch m.String() {
	case "up", "k":
		p.cur = clampInt(p.cur-1, 0, len(p.list)-1)
	case "down", "j":
		p.cur = clampInt(p.cur+1, 0, len(p.list)-1)
	case "i", "u":
		p.mode = map[string]string{"i": "installed", "u": "upgradable"}[m.String()]
		p.cur, p.detail = 0, ""
		return p, p.load()
	case "/":
		p.typing = true
		return p, p.input.Focus()
	case "enter":
		if n := p.cursorName(); n != "" {
			return p, p.e.call(func(ctx context.Context) tea.Msg {
				t, err := p.e.S.PkgInfo(ctx, n)
				return pkgTextMsg{t, err}
			})
		}
	case "+", "=", "-", "_":
		return p.plan(m.String() == "-" || m.String() == "_")
	case "U":
		if p.e.S.ReadOnly {
			return p, toast("read-only mode: start with -write to update the package index")
		}
		return p, func() tea.Msg {
			return askMsg{Prompt: "Update the package index on the router?", Yes: func() tea.Cmd {
				return p.e.callFor(slowOp, func(ctx context.Context) tea.Msg {
					out, err := p.e.S.PkgUpdate(ctx)
					return pkgDoneMsg{"update index", out, err}
				})
			}}
		}
	}
	return p, nil
}

// plan always dry-runs first; the result is shown before anything is changed.
func (p *packagesScreen) plan(remove bool) (screen, tea.Cmd) {
	name := p.cursorName()
	if name == "" {
		return p, nil
	}
	verb := "install"
	if remove {
		verb = "remove"
	}
	p.detail = "simulating " + verb + " " + name + " ..."
	return p, p.e.callFor(slowOp, func(ctx context.Context) tea.Msg {
		plan, err := p.e.S.PkgSimulate(ctx, name, remove)
		if err != nil {
			return pkgTextMsg{err: fmt.Errorf("%s\n%s", err, plan.Raw)}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Dry run: %s %s (nothing changed yet)\n", verb, name)
		for _, a := range plan.Actions {
			fmt.Fprintf(&b, "  %-12s %s %s\n", a.Verb, a.Name, a.Version)
		}
		if len(plan.Actions) == 0 {
			b.WriteString("  (no actions reported)\n")
		}
		if !remove && plan.FreeKB > 0 {
			line := fmt.Sprintf("Overlay free: %s", kib(plan.FreeKB))
			if plan.FreeKB < 2048 {
				line = sWarn.Render(line + "  very low: an install may fill the flash and fail")
			}
			b.WriteString(line + "\n")
		}
		if p.e.S.ReadOnly {
			b.WriteString(sDim.Render("read-only mode: start with -write to run it for real"))
			return pkgTextMsg{text: b.String()}
		}
		b.WriteString(sDim.Render("confirm below to run it for real"))
		return pkgPlanMsg{name: name, remove: remove, text: b.String()}
	})
}

type pkgPlanMsg struct {
	name   string
	remove bool
	text   string
}

func (p *packagesScreen) View(w, h int) string {
	if p.typing {
		return p.input.View() + "\n"
	}
	head := fmt.Sprintf(" %s", p.mode)
	if p.mode == "search" {
		head += fmt.Sprintf(" %q", p.query)
	}
	head += fmt.Sprintf("  (%d)  via %s", len(p.list), p.e.S.PkgManager())
	if p.loading {
		head += "  loading..."
	}
	detailH := 0
	if p.detail != "" {
		detailH = h / 2
	}
	listH := h - 1 - detailH
	var b strings.Builder
	b.WriteString(sDim.Render(head) + "\n")
	if p.err != nil {
		b.WriteString("  " + sBad.Render("error: "+p.err.Error()) + "\n")
	}
	lo, hi := window(len(p.list), p.cur, listH)
	for i := lo; i < hi; i++ {
		pk := p.list[i]
		line := fmt.Sprintf("  %-32s %-18s %s", pk.Name, pk.Version, pk.Desc)
		if i == p.cur {
			line = sSel.Render(clip(line, w))
		}
		b.WriteString(line + "\n")
	}
	if detailH > 0 {
		cur := strings.Count(b.String(), "\n")
		for ; cur < h-detailH; cur++ {
			b.WriteString("\n")
		}
		b.WriteString(sDim.Render(strings.Repeat("─", w)) + "\n" + p.detail)
	}
	return b.String()
}
