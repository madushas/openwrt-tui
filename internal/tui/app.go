// Package tui is the Bubble Tea front end. It only calls internal/session.
package tui

import (
	"context"
	"fmt"
	"strconv"
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

// Overridable so tests never touch the real config directory.
var (
	loadThemeFn = loadTheme
	saveThemeFn = saveTheme
)

const (
	sidebarW = 22
	detailW  = 40
)

// activator is implemented by screens that do background work only while shown.
type activator interface{ SetActive(on bool) tea.Cmd }

// statuser shows a short status in the panel border.
type statuser interface{ Status() string }

// borderless screens draw their own panels and get the whole area.
type borderless interface{ Borderless() bool }

// detailer is implemented by screens with a side pane for the selected item.
type detailer interface{ Detail(w, h int) string }

var icons = map[string]string{
	"Dashboard": "◈", "Config": "≡", "Changes": "±", "Services": "▣",
	"Logs": "▤", "Packages": "▥", "Diagnostics": "◎",
}

// App is the root model.
type App struct {
	env     *env
	screens []screen
	active  int
	w, h    int

	ask     *askMsg
	toast   toastMsg
	toastID int
	help    bool
	pal     *palette
	theme   string
}

// New builds the root model.
func New(ctx context.Context, s *session.Session, timeout time.Duration) *App {
	e := &env{ctx: ctx, S: s, Staged: &changes.Set{}, Timeout: timeout}
	a := &App{env: e, w: 80, h: 24}
	a.theme = applyTheme(loadThemeFn()).Name
	a.screens = []screen{
		newDashboard(e),
		newWifi(e),
		newDHCP(e),
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
	if ac, ok := a.screens[a.active].(activator); ok {
		cmds = append(cmds, ac.SetActive(true))
	}
	return tea.Batch(cmds...)
}

type toastExpireMsg int

func (a *App) goTo(i int) tea.Cmd {
	if i < 0 || i >= len(a.screens) || i == a.active {
		return nil
	}
	var cmds []tea.Cmd
	if ac, ok := a.screens[a.active].(activator); ok {
		cmds = append(cmds, ac.SetActive(false))
	}
	a.active = i
	if ac, ok := a.screens[i].(activator); ok {
		cmds = append(cmds, ac.SetActive(true))
	}
	return tea.Batch(cmds...)
}

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
	case tea.MouseClickMsg:
		return a.click(m.Mouse())
	case tea.MouseWheelMsg:
		code := tea.KeyUp
		if m.Mouse().Button == tea.MouseWheelDown {
			code = tea.KeyDown
		}
		if a.pal != nil || a.help || a.ask != nil {
			return a, nil
		}
		ns, cmd := a.screens[a.active].Update(tea.KeyPressMsg{Code: code})
		a.screens[a.active] = ns
		return a, cmd
	}
	// Results of background work go to every screen; each ignores types it
	// does not own, so a reply is not lost when the user switched tabs.
	var cmds []tea.Cmd
	for i, s := range a.screens {
		ns, cmd := s.Update(msg)
		a.screens[i] = ns
		cmds = append(cmds, cmd)
	}
	return a, tea.Batch(cmds...)
}

// full is the layout with the Menu sidebar; narrower terminals get a tab bar so
// the screen itself keeps the width.
func (a *App) full() bool { return a.w >= 100 && a.h >= 14 }

func (a *App) click(m tea.Mouse) (tea.Model, tea.Cmd) {
	if m.Button != tea.MouseLeft || a.ask != nil || a.pal != nil {
		return a, nil
	}
	if a.full() {
		if m.X < sidebarW {
			if i := m.Y - 2; i >= 0 && i < len(a.screens) {
				return a, a.goTo(i)
			}
		}
		return a, nil
	}
	if m.Y == 1 { // compact tab bar
		x := 0
		for i, l := range a.tabLabels() {
			wd := lipgloss.Width(sTab.Render(l))
			if m.X >= x && m.X < x+wd {
				return a, a.goTo(i)
			}
			x += wd
		}
	}
	return a, nil
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
	if a.pal != nil {
		chosen, done, cmd := a.pal.key(k)
		if done {
			a.pal = nil
		}
		if chosen != nil && chosen.Run != nil {
			return a, tea.Batch(cmd, chosen.Run())
		}
		return a, cmd
	}
	if a.help {
		if s == "?" || s == "esc" || s == "q" {
			a.help = false
		}
		return a, nil
	}
	cur := a.screens[a.active]
	if !cur.Capturing() {
		switch s {
		case "q":
			return a, tea.Quit
		case "?":
			a.help = true
			return a, nil
		case ":", "ctrl+p":
			a.pal = newPalette(a.paletteItems())
			return a, a.pal.input.Focus()
		case "tab":
			return a, a.goTo((a.active + 1) % len(a.screens))
		case "shift+tab":
			return a, a.goTo((a.active + len(a.screens) - 1) % len(a.screens))
		}
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			if i := int(s[0] - '1'); i < len(a.screens) {
				return a, a.goTo(i)
			}
		}
	}
	ns, cmd := cur.Update(k)
	a.screens[a.active] = ns
	return a, cmd
}

// paletteItems builds the command list: fixed commands plus whatever the
// screens offer for what they currently show (services, config files, ...).
func (a *App) paletteItems() []paletteItem {
	var items []paletteItem
	for i, s := range a.screens {
		i := i
		items = append(items, paletteItem{Title: "Go to " + s.Title(), Hint: strconv.Itoa(i + 1), Run: func() tea.Cmd { return a.goTo(i) }})
	}
	for i, s := range a.screens {
		c, ok := s.(commander)
		if !ok {
			continue
		}
		i := i
		for _, it := range c.Commands() {
			it := it
			run := it.Run
			it.Run = func() tea.Cmd { return tea.Batch(a.goTo(i), run()) }
			items = append(items, it)
		}
	}
	items = append(items, paletteItem{Title: "Reboot router", Hint: "dashboard R", Run: func() tea.Cmd {
		if a.env.S.ReadOnly {
			return toast("read-only mode: start with -write to reboot")
		}
		return func() tea.Msg {
			return askMsg{Prompt: "Reboot " + a.env.S.T.Describe() + " now?", Danger: true, Yes: func() tea.Cmd {
				return a.env.call(func(ctx context.Context) tea.Msg { return rebootDoneMsg{a.env.S.Reboot(ctx)} })
			}}
		}
	}})
	for _, t := range themes {
		t := t
		hint := ""
		if t.Name == a.theme {
			hint = "current"
		}
		items = append(items, paletteItem{Title: "Theme: " + t.Name, Hint: hint, Run: func() tea.Cmd {
			a.theme = applyTheme(t.Name).Name
			saveThemeFn(a.theme)
			return nil
		}})
	}
	items = append(items,
		paletteItem{Title: "Show help", Hint: "?", Run: func() tea.Cmd { a.help = true; return nil }},
		paletteItem{Title: "Quit", Hint: "q", Run: func() tea.Cmd { return tea.Quit }},
	)
	return items
}

// ---- view ----

func (a *App) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "owrt-tui " + a.env.S.T.Describe()
	return v
}

func (a *App) tabLabels() []string {
	var out []string
	for i, s := range a.screens {
		t := fmt.Sprintf("%d %s", i+1, s.Title())
		if _, ok := s.(*changesScreen); ok && a.env.Staged.Len() > 0 {
			t += fmt.Sprintf(" (%d)", a.env.Staged.Len())
		}
		out = append(out, t)
	}
	return out
}

func (a *App) modeChip() string {
	if a.env.S.ReadOnly {
		return chipGood.Render("READ-ONLY")
	}
	return chipWarn.Render("WRITE")
}

func (a *App) header(w int) string {
	left := sAccent.Render(" owrt-tui ") + sDim.Render(a.env.S.T.Describe())
	right := a.modeChip()
	if st, ok := a.screens[a.active].(statuser); ok && a.pal == nil && !a.help {
		if t := st.Status(); t != "" {
			right = sDim.Render(t+"  ") + right
		}
	}
	if n := a.env.Staged.Len(); n > 0 {
		right = chipWarn.Render(fmt.Sprintf("± %d staged", n)) + " " + right
	}
	gap := max0(w - lipgloss.Width(left) - lipgloss.Width(right) - 1)
	return clip(left+strings.Repeat(" ", gap)+right+" ", w)
}

func (a *App) footer(w int) string {
	switch {
	case a.ask != nil:
		st := sWarn
		if a.ask.Danger {
			st = sBad
		}
		return clip(" "+st.Render(a.ask.Prompt)+sDim.Render("   y yes · n no"), w)
	case a.toast.Text != "":
		if a.toast.Error {
			return clip(" "+sBad.Render("✗ "+a.toast.Text), w)
		}
		return clip(" "+sGood.Render("✓ "+a.toast.Text), w)
	case a.pal != nil:
		return hints("enter run  up/down choose  esc close", w)
	case a.help:
		return hints("esc close", w)
	}
	left := hints(a.screens[a.active].Keys(), w)
	right := hints(": palette  ? help  q quit", w)
	if lipgloss.Width(left)+lipgloss.Width(right)+3 <= w {
		return " " + padVisible(left, w-lipgloss.Width(right)-3) + right + " "
	}
	return " " + clip(left, w-1)
}

func (a *App) mainBody(w, h int) (title, right, body string) {
	cur := a.screens[a.active]
	switch {
	case a.pal != nil:
		return "Command palette", "", a.pal.view(w, h)
	case a.help:
		return "Help", "", a.helpText()
	}
	if st, ok := cur.(statuser); ok {
		right = st.Status()
	}
	return cur.Title(), right, cur.View(w, h)
}

func (a *App) render() string {
	if a.w < 40 || a.h < 8 {
		return "terminal too small (need at least 40x8)"
	}
	if !a.full() {
		return a.renderCompact()
	}
	bodyH := a.h - 2
	mainW := a.w - sidebarW
	var detail string
	cur := a.screens[a.active]
	if d, ok := cur.(detailer); ok && a.w >= 120 && a.pal == nil && !a.help {
		if t := d.Detail(detailW-2, bodyH-2); t != "" {
			detail = panel("Details", "", t, detailW, bodyH, false)
			mainW -= detailW
		}
	}
	var main string
	if bl, ok := cur.(borderless); ok && bl.Borderless() && a.pal == nil && !a.help {
		_, _, body := a.mainBody(mainW, bodyH)
		main = fitLines(body, mainW, bodyH)
	} else {
		title, right, body := a.mainBody(mainW-2, bodyH-2)
		main = panel(title, right, body, mainW, bodyH, true)
	}
	row := join(a.sidebar(sidebarW, bodyH), main)
	if detail != "" {
		row = join(a.sidebar(sidebarW, bodyH), main, detail)
	}
	return strings.Join([]string{a.header(a.w), row, a.footer(a.w)}, "\n")
}

func (a *App) renderCompact() string {
	bodyH := a.h - 4 // header, tabs, rule, footer
	var tabs []string
	for i, l := range a.tabLabels() {
		if i == a.active {
			tabs = append(tabs, sTabOn.Render(l))
		} else {
			tabs = append(tabs, sTab.Render(l))
		}
	}
	_, _, body := a.mainBody(a.w, bodyH)
	return strings.Join([]string{
		a.header(a.w),
		clip(strings.Join(tabs, ""), a.w),
		sBorder.Render(strings.Repeat("─", a.w)),
		fitLines(body, a.w, bodyH),
		a.footer(a.w),
	}, "\n")
}

func (a *App) sidebar(w, h int) string {
	var lines []string
	for i, s := range a.screens {
		label := fmt.Sprintf(" %s %d %s", icons[s.Title()], i+1, s.Title())
		if _, ok := s.(*changesScreen); ok && a.env.Staged.Len() > 0 {
			label += fmt.Sprintf(" (%d)", a.env.Staged.Len())
		}
		if i == a.active {
			lines = append(lines, sSel.Render(padVisible(clip(label, w-2), w-2)))
		} else {
			lines = append(lines, sText.Render(label))
		}
	}
	// Router identity at the bottom of the sidebar.
	inner := h - 2
	for len(lines) < inner-3 {
		lines = append(lines, "")
	}
	info := a.env.S.Info
	lines = append(lines,
		sDim.Render(" "+strings.Repeat("─", w-4)),
		" "+sBold.Render(clip(info.Hostname, w-4)),
		" "+sDim.Render(clip(strings.TrimSpace(info.Distro+" "+info.Version), w-4)),
	)
	return panel("Menu", "", strings.Join(lines, "\n"), w, h, false)
}

func (a *App) helpText() string {
	cur := a.screens[a.active]
	var b strings.Builder
	line := func(k, d string) {
		b.WriteString(" " + sKey.Render(fmt.Sprintf("%-14s", k)) + sKeyDesc.Render(d) + "\n")
	}
	b.WriteString("\n " + sAccent.Render("Everywhere") + "\n")
	line("tab, shift+tab", "next / previous screen")
	line("1-7", "jump to a screen (or click it in the menu)")
	line(": or ctrl+p", "command palette: type to find anything")
	line("?", "this help")
	line("q, ctrl+c", "quit")
	b.WriteString("\n " + sAccent.Render(cur.Title()) + "\n")
	for _, it := range strings.Split(cur.Keys(), "  ") {
		k, d, _ := strings.Cut(strings.TrimSpace(it), " ")
		if k != "" {
			line(k, d)
		}
	}
	b.WriteString("\n " + sAccent.Render("Safety") + "\n")
	for _, l := range []string{
		"Read-only is the default. Start with -write to change things.",
		"Edits are staged and shown as a diff before anything is sent.",
		"Risky changes default to apply-with-rollback: the router restores",
		"the old config unless you confirm in time.",
		"Secrets are masked; v on the Config screen reveals them.",
	} {
		b.WriteString(" " + sDim.Render(l) + "\n")
	}
	return b.String()
}
