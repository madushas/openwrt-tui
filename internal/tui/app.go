// Package tui is the Bubble Tea front end. It only calls internal/session.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/session"
)

// Run starts the TUI and blocks until the user quits.
func Run(ctx context.Context, s *session.Session, timeout time.Duration) error {
	// Cancelling on exit stops a running "logread -f" or traceroute on the router.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := New(ctx, s, timeout)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

// App is the root model.
type App struct {
	env     *env
	screens []screen
	active  int
	w, h    int

	ask     *askMsg
	toast   toastMsg
	help    bool
	toastID int
}

// New builds the root model.
func New(ctx context.Context, s *session.Session, timeout time.Duration) *App {
	e := &env{ctx: ctx, S: s, Staged: &changes.Set{}, Timeout: timeout}
	a := &App{env: e, w: 80, h: 24}
	a.screens = []screen{
		newOverview(e),
		newUCI(e),
		newChanges(e),
		newServices(e),
		newLogs(e),
		newPackages(e),
		newDiag(e),
	}
	return a
}

func (a *App) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range a.screens {
		cmds = append(cmds, s.Init())
	}
	return tea.Batch(cmds...)
}

type toastExpireMsg int

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		return a, nil
	case askMsg:
		a.ask = &m
		return a, nil
	case toastMsg:
		a.toast = m
		a.toastID++
		id := a.toastID
		return a, tea.Tick(6*time.Second, func(time.Time) tea.Msg { return toastExpireMsg(id) })
	case toastExpireMsg:
		if int(m) == a.toastID {
			a.toast = toastMsg{}
		}
		return a, nil
	case tea.KeyPressMsg:
		return a.key(m)
	}
	// Results of background work go to every screen; each ignores types it
	// does not own, so a reply is not lost when the user switched tabs.
	return a, a.broadcast(msg)
}

func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for i, s := range a.screens {
		ns, cmd := s.Update(msg)
		a.screens[i] = ns
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (a *App) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return a, tea.Quit
	}
	if a.ask != nil {
		switch s {
		case "y", "Y":
			yes := a.ask.Yes
			a.ask = nil
			if yes != nil {
				return a, yes()
			}
		case "n", "N", "esc", "enter":
			a.ask = nil
		}
		return a, nil
	}
	cur := a.screens[a.active]
	if !cur.Capturing() {
		switch s {
		case "q":
			return a, tea.Quit
		case "?":
			a.help = !a.help
			return a, nil
		case "tab":
			a.active = (a.active + 1) % len(a.screens)
			return a, nil
		case "shift+tab":
			a.active = (a.active + len(a.screens) - 1) % len(a.screens)
			return a, nil
		}
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			if i := int(s[0] - '1'); i < len(a.screens) {
				a.active = i
				return a, nil
			}
		}
	}
	ns, cmd := cur.Update(k)
	a.screens[a.active] = ns
	return a, cmd
}

func (a *App) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	v.WindowTitle = "owrt-tui " + a.env.S.T.Describe()
	return v
}

func (a *App) tabBar() string {
	var parts []string
	for i, s := range a.screens {
		title := fmt.Sprintf("%d %s", i+1, s.Title())
		if _, ok := s.(*changesScreen); ok && a.env.Staged.Len() > 0 {
			title += fmt.Sprintf(" (%d)", a.env.Staged.Len())
		}
		if i == a.active {
			parts = append(parts, sTabOn.Render(title))
		} else {
			parts = append(parts, sTab.Render(title))
		}
	}
	return strings.Join(parts, "")
}

func (a *App) header() string {
	mode := sGood.Render("read-only")
	if !a.env.S.ReadOnly {
		mode = sWarn.Render("WRITE")
	}
	left := sAccent.Render("owrt-tui") + " " + sDim.Render(a.env.S.T.Describe())
	return left + "  " + mode
}

func (a *App) footer() string {
	switch {
	case a.ask != nil:
		st := sWarn
		if a.ask.Danger {
			st = sBad
		}
		return st.Render(a.ask.Prompt) + sDim.Render("  [y] yes  [n] no")
	case a.toast.Text != "":
		if a.toast.Error {
			return sBad.Render(a.toast.Text)
		}
		return sGood.Render(a.toast.Text)
	}
	return sDim.Render(a.screens[a.active].Keys() + "  tab next  ? help  q quit")
}

func (a *App) render() string {
	if a.w < 40 || a.h < 8 {
		return "terminal too small (need at least 40x8)"
	}
	bodyH := a.h - 4 // header, tabs, rule, footer
	var body string
	if a.help {
		body = helpText
	} else {
		body = a.screens[a.active].View(a.w, bodyH)
	}
	rule := sDim.Render(strings.Repeat("─", a.w))
	return lipgloss.JoinVertical(lipgloss.Left,
		clip(a.header(), a.w),
		clip(a.tabBar(), a.w),
		rule,
		fitLines(body, a.w, bodyH),
		clip(a.footer(), a.w),
	)
}

const helpText = `
 Navigation   tab / shift+tab or 1-7 switch screens    q quit    ? close help
 Lists        up/down or k/j move, enter open, esc back

 Safety
   Read-only is the default. Start with -write to stage and apply changes.
   Edits are staged first (screen 3) and shown as a diff before anything is sent.
   Changes that can cut your connection default to "apply with rollback":
   the router restores the old config unless you confirm in time.
   Secrets (keys, passwords) are masked; v on the UCI screen reveals them.

 Press ? again to go back.`
