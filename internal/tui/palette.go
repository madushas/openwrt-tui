package tui

import (
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// paletteItem is one entry in the command palette.
type paletteItem struct {
	Title string
	Hint  string // shown dimmed on the right: a key or the screen it belongs to
	Run   func() tea.Cmd
}

// commander is implemented by screens that add their own palette entries.
type commander interface{ Commands() []paletteItem }

// fuzzy reports whether every rune of query appears in text in order, and
// scores the match: consecutive runs and word starts score higher, so "rsvc"
// finds "Restart service" before "reboot services".
func fuzzy(query, text string) (int, bool) {
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(q) == 0 {
		return 0, true
	}
	t := []rune(strings.ToLower(text))
	score, qi, run := 0, 0, 0
	for ti, r := range t {
		if qi < len(q) && r == q[qi] {
			bonus := 1
			if run > 0 {
				bonus += 3 * run // consecutive characters
			}
			if ti == 0 || !unicode.IsLetter(t[ti-1]) && !unicode.IsDigit(t[ti-1]) {
				bonus += 5 // start of a word
			}
			score += bonus
			qi++
			run++
		} else {
			run = 0
		}
	}
	if qi < len(q) {
		return 0, false
	}
	return score - len(t)/8, true // slightly prefer shorter titles
}

type palette struct {
	input textinput.Model
	all   []paletteItem
	shown []paletteItem
	cur   int
}

func newPalette(items []paletteItem) *palette {
	in := textinput.New()
	in.Prompt = ": "
	in.Placeholder = "type to search: go to logs, restart wifi, theme..."
	p := &palette{input: in, all: items}
	p.filter()
	return p
}

func (p *palette) filter() {
	type scored struct {
		it    paletteItem
		score int
		idx   int
	}
	var m []scored
	for i, it := range p.all {
		if sc, ok := fuzzy(p.input.Value(), it.Title+" "+it.Hint); ok {
			m = append(m, scored{it, sc, i})
		}
	}
	sort.SliceStable(m, func(i, j int) bool { return m[i].score > m[j].score })
	p.shown = p.shown[:0]
	for _, s := range m {
		p.shown = append(p.shown, s.it)
	}
	p.cur = clampInt(p.cur, 0, len(p.shown)-1)
}

// key handles a key press. It returns the chosen item when the user pressed
// enter on one, and done=true when the palette should close.
func (p *palette) key(m tea.KeyPressMsg) (chosen *paletteItem, done bool, cmd tea.Cmd) {
	switch m.String() {
	case "esc", "ctrl+c":
		return nil, true, nil
	case "enter":
		if len(p.shown) > 0 {
			it := p.shown[p.cur]
			return &it, true, nil
		}
		return nil, false, nil
	case "up", "ctrl+p":
		p.cur = clampInt(p.cur-1, 0, len(p.shown)-1)
		return nil, false, nil
	case "down", "ctrl+n":
		p.cur = clampInt(p.cur+1, 0, len(p.shown)-1)
		return nil, false, nil
	}
	before := p.input.Value()
	p.input, cmd = p.input.Update(m)
	if p.input.Value() != before {
		p.cur = 0
		p.filter()
	}
	return nil, false, cmd
}

func (p *palette) view(w, h int) string {
	var b strings.Builder
	b.WriteString(" " + p.input.View() + "\n")
	b.WriteString(sDim.Render(" "+strings.Repeat("─", max0(w-4))) + "\n")
	rows := max0(h - 3)
	lo, hi := window(len(p.shown), p.cur, rows)
	for i := lo; i < hi; i++ {
		it := p.shown[i]
		gap := max0(w - 4 - len([]rune(it.Title)) - len([]rune(it.Hint)))
		line := " " + it.Title + strings.Repeat(" ", gap) + it.Hint + " "
		if i == p.cur {
			line = sSel.Render(clip(line, w-2))
		} else {
			line = clip(" "+it.Title, w-2)
			if it.Hint != "" {
				line = padVisible(line, w-4-len([]rune(it.Hint))) + sDim.Render(it.Hint)
			}
		}
		b.WriteString(line + "\n")
	}
	if len(p.shown) == 0 {
		b.WriteString(sDim.Render(" nothing matches") + "\n")
	}
	return b.String()
}

// keyMsg builds the key press for a single-character key, so palette entries
// can reuse a screen's own key handling instead of duplicating it.
func keyMsg(k string) tea.KeyPressMsg {
	r := []rune(k)
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

// sendKey makes a screen handle key k as if the user had typed it.
func sendKey(s screen, k string) tea.Cmd {
	_, cmd := s.Update(keyMsg(k))
	return cmd
}

func item(s screen, title, hint, key string) paletteItem {
	return paletteItem{Title: title, Hint: hint, Run: func() tea.Cmd { return sendKey(s, key) }}
}

func (d *dashboard) Commands() []paletteItem {
	return []paletteItem{
		item(d, "Dashboard: pause or resume", "p", "p"),
		item(d, "Dashboard: switch bits / bytes", "u", "u"),
		item(d, "Dashboard: refresh faster", "+", "+"),
		item(d, "Dashboard: refresh slower", "-", "-"),
		item(d, "Dashboard: graph next interface", "i", "i"),
	}
}

func (l *logsScreen) Commands() []paletteItem {
	setLevel := func(i int) paletteItem {
		name := map[int]string{0: "everything", 1: "warnings and errors", 2: "errors only"}[i]
		return paletteItem{Title: "Logs: show " + name, Hint: "l", Run: func() tea.Cmd {
			l.level = i
			l.filter.MinSeverity = levels[i]
			l.refresh()
			return nil
		}}
	}
	return []paletteItem{
		setLevel(0), setLevel(1), setLevel(2),
		item(l, "Logs: follow live / stop following", "f", "f"),
		item(l, "Logs: reload", "g", "g"),
		{Title: "Logs: clear text filter", Hint: "/", Run: func() tea.Cmd {
			l.filter.Contains = ""
			l.input.SetValue("")
			l.refresh()
			return nil
		}},
	}
}

func (p *packagesScreen) Commands() []paletteItem {
	return []paletteItem{
		item(p, "Packages: show installed", "i", "i"),
		item(p, "Packages: show upgradable", "u", "u"),
		item(p, "Packages: search...", "/", "/"),
		item(p, "Packages: update package index", "U", "U"),
	}
}

func (c *changesScreen) Commands() []paletteItem {
	return []paletteItem{
		item(c, "Changes: apply staged changes", "a", "a"),
		item(c, "Changes: discard all staged changes", "x", "x"),
		item(c, "Changes: toggle rollback protection", "m", "m"),
	}
}

func (d *diagScreen) Commands() []paletteItem {
	return []paletteItem{
		item(d, "Diagnostics: ping", "p", "p"),
		item(d, "Diagnostics: traceroute", "t", "t"),
		item(d, "Diagnostics: nslookup", "n", "n"),
		item(d, "Diagnostics: change host", "h", "h"),
	}
}
