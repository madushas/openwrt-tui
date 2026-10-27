package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/logs"
)

const logBacklog = 300
const logCap = 5000 // entries kept in memory while following

var levels = []string{"", "warn", "err"} // "" = everything

type logsScreen struct {
	e         *env
	entries   []logs.Entry
	vp        viewport.Model
	input     textinput.Model
	filter    logs.Filter
	level     int
	typing    bool
	following bool
	cancel    context.CancelFunc
	feed      chan logs.Entry
	err       error
	loaded    bool
	w, h      int
}

func newLogs(e *env) *logsScreen {
	in := textinput.New()
	in.Prompt = "/ "
	in.Placeholder = "text to match"
	return &logsScreen{e: e, vp: viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)), input: in}
}

func (l *logsScreen) Title() string   { return "Logs" }
func (l *logsScreen) Capturing() bool { return l.typing }
func (l *logsScreen) Keys() string {
	return "l level  / filter  f follow  g reload  up/down pgup/pgdn scroll"
}

type logsMsg struct {
	entries []logs.Entry
	err     error
}
type logLineMsg struct{ e logs.Entry }
type logEndMsg struct{ err error }

func (l *logsScreen) Init() tea.Cmd { return l.fetch() }

func (l *logsScreen) fetch() tea.Cmd {
	return l.e.call(func(ctx context.Context) tea.Msg {
		en, err := logs.Fetch(ctx, l.e.S.T, logBacklog)
		return logsMsg{en, err}
	})
}

// waitLine turns the next streamed entry into a message.
func (l *logsScreen) waitLine(ch chan logs.Entry) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return logEndMsg{}
		}
		return logLineMsg{e}
	}
}

func (l *logsScreen) startFollow() tea.Cmd {
	ctx, cancel := context.WithCancel(l.e.ctx)
	l.cancel = cancel
	ch := make(chan logs.Entry, 64)
	l.feed = ch
	l.following = true
	go func() {
		defer close(ch)
		err := logs.Follow(ctx, l.e.S.T, 0, func(e logs.Entry) {
			select {
			case ch <- e:
			case <-ctx.Done():
			}
		})
		_ = err // surfaced as the stream ending; Stop is the normal exit
	}()
	return l.waitLine(ch)
}

func (l *logsScreen) stopFollow() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.following = false
	l.feed = nil
}

func (l *logsScreen) refresh() {
	var b strings.Builder
	for _, e := range l.filter.Apply(l.entries) {
		b.WriteString(colorEntry(e) + "\n")
	}
	atBottom := l.vp.AtBottom()
	l.vp.SetContent(b.String())
	if atBottom || l.following {
		l.vp.GotoBottom()
	}
}

func colorEntry(e logs.Entry) string {
	if e.Time == "" {
		return e.Raw
	}
	line := fmt.Sprintf("%s %s %s: %s", sDim.Render(shortTime(e.Time)), e.Severity, e.Proc, e.Msg)
	switch e.Rank() {
	case 0, 1, 2, 3:
		return sBad.Render(fmt.Sprintf("%s %s %s: %s", shortTime(e.Time), e.Severity, e.Proc, e.Msg))
	case 4:
		return sDim.Render(shortTime(e.Time)) + " " + sWarn.Render(e.Severity+" "+e.Proc+": "+e.Msg)
	}
	return line
}

// shortTime drops the weekday and year from "Mon Oct 25 09:13:01 2026".
func shortTime(t string) string {
	f := strings.Fields(t)
	if len(f) == 5 {
		return f[1] + " " + f[2] + " " + f[3]
	}
	return t
}

func (l *logsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case logsMsg:
		l.loaded, l.err = true, m.err
		if m.err == nil {
			l.entries = m.entries
			l.refresh()
			l.vp.GotoBottom()
		}
	case logLineMsg:
		if !l.following {
			return l, nil
		}
		l.entries = append(l.entries, m.e)
		if len(l.entries) > logCap {
			l.entries = l.entries[len(l.entries)-logCap:]
		}
		l.refresh()
		return l, l.waitLine(l.feed)
	case logEndMsg:
		if l.following {
			l.following = false
			l.feed = nil
			return l, toast("log stream ended")
		}
	case tea.KeyPressMsg:
		return l.key(m)
	}
	return l, nil
}

func (l *logsScreen) key(m tea.KeyPressMsg) (screen, tea.Cmd) {
	if l.typing {
		switch m.String() {
		case "enter", "esc":
			l.typing = false
			l.input.Blur()
			l.filter.Contains = l.input.Value()
			l.refresh()
			return l, nil
		}
		var cmd tea.Cmd
		l.input, cmd = l.input.Update(m)
		return l, cmd
	}
	switch m.String() {
	case "/":
		l.typing = true
		return l, l.input.Focus()
	case "l":
		l.level = (l.level + 1) % len(levels)
		l.filter.MinSeverity = levels[l.level]
		l.refresh()
	case "f":
		if l.following {
			l.stopFollow()
			return l, toast("stopped following")
		}
		return l, l.startFollow()
	case "g":
		return l, l.fetch()
	default:
		var cmd tea.Cmd
		l.vp, cmd = l.vp.Update(m)
		return l, cmd
	}
	return l, nil
}

func (l *logsScreen) View(w, h int) string {
	if !l.loaded {
		return "\n  loading log..."
	}
	if l.err != nil {
		return "\n  " + sBad.Render("error: "+l.err.Error())
	}
	l.vp.SetWidth(w)
	l.vp.SetHeight(h - 1)
	lvl := levels[l.level]
	if lvl == "" {
		lvl = "all"
	}
	status := fmt.Sprintf(" level: %s  match: %q  lines: %d", lvl, l.filter.Contains, len(l.filter.Apply(l.entries)))
	if l.following {
		status += "  " + sGood.Render("FOLLOWING")
	}
	if l.typing {
		return l.input.View() + "\n" + l.vp.View()
	}
	return sDim.Render(status) + "\n" + l.vp.View()
}
