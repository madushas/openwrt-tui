package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/session"
)

// env is shared by every screen. Screens only ever talk to the router through
// env.S (the session), which owns the read-only switch.
type env struct {
	ctx     context.Context
	S       *session.Session
	Staged  *changes.Set
	Timeout time.Duration
}

// call runs f off the UI goroutine with the per-command timeout and turns its
// result into a message.
func (e *env) call(f func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(e.ctx, e.Timeout)
		defer cancel()
		return f(ctx)
	}
}

// Messages that cross screens.

// askMsg asks the app to show a yes/no prompt; yes runs when confirmed.
type askMsg struct {
	Prompt string
	Danger bool
	Yes    func() tea.Cmd
}

// toastMsg shows a one-line notice in the footer.
type toastMsg struct {
	Text  string
	Error bool
}

// uciChangedMsg tells the UCI screen to reload after an apply or restore.
type uciChangedMsg struct{}

func toast(text string) tea.Cmd { return func() tea.Msg { return toastMsg{Text: text} } }
func toastErr(err error) tea.Cmd {
	return func() tea.Msg { return toastMsg{Text: err.Error(), Error: true} }
}

// screen is one tab.
type screen interface {
	Title() string
	Init() tea.Cmd
	Update(tea.Msg) (screen, tea.Cmd)
	View(w, h int) string
	// Capturing is true while a text field has focus, so global keys such as
	// "q" and digits reach the field instead of switching tabs.
	Capturing() bool
	Keys() string
}

// callFor is call with an explicit timeout, for slow operations such as package
// installs and index updates.
func (e *env) callFor(d time.Duration, f func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(e.ctx, d)
		defer cancel()
		return f(ctx)
	}
}
