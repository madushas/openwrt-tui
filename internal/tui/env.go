package tui

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/history"
	"github.com/madushakv/owrt-tui/internal/session"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// env is shared by every screen. Screens only ever talk to the router through
// env.S (the session), which owns the read-only switch.
type env struct {
	ctx     context.Context
	S       *session.Session
	Staged  *changes.Set
	Timeout time.Duration

	hist     *history.Store
	histOnce sync.Once

	cacheMu sync.Mutex
	cache   []uci.Config
	cached  bool
}

// loadUCI returns the router's configuration, reading it once and sharing it
// between screens until something invalidates it (an apply, a restore, or a
// screen's explicit reload).
func (e *env) loadUCI(ctx context.Context) ([]uci.Config, error) {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	if e.cached {
		return e.cache, nil
	}
	c, err := e.S.LoadUCI(ctx)
	if err != nil {
		return nil, err
	}
	e.cache, e.cached = c, true
	return c, nil
}

// peekUCI returns the shared copy if it has been loaded, without reading the router.
func (e *env) peekUCI() []uci.Config {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	if !e.cached {
		return nil
	}
	return e.cache
}

// invalidate drops the shared copy so the next loadUCI reads the router again.
func (e *env) invalidate() {
	e.cacheMu.Lock()
	e.cached, e.cache = false, nil
	e.cacheMu.Unlock()
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
