package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/madushakv/owrt-tui/internal/transport"
)

// RunConnect starts the TUI on a Connect screen. It finds or asks for the
// router, helps fix connection problems, then switches to the main screens.
func RunConnect(ctx context.Context, o ConnectOpts) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &Root{ctx: ctx, opts: o, c: newConnect(ctx, o), w: 80, h: 24}
	m, err := tea.NewProgram(r, tea.WithContext(ctx)).Run()
	if rr, ok := m.(*Root); ok {
		rr.Close()
	} else {
		r.Close()
	}
	return err
}

// Root shows the connect flow until a session exists, then the main App.
type Root struct {
	ctx  context.Context
	opts ConnectOpts
	c    *connect
	app  *App
	t    transport.Transport
	w, h int
	note toastMsg
}

// Close releases the ssh connection.
func (r *Root) Close() {
	if r.t != nil {
		r.t.Close()
		r.t = nil
	}
	if r.c != nil {
		r.c.closePending()
	}
}

func (r *Root) Init() tea.Cmd { return r.c.Init() }

func (r *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if r.app != nil {
		_, cmd := r.app.Update(msg)
		return r, cmd
	}
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		r.w, r.h = m.Width, m.Height
		return r, nil
	case toastMsg:
		r.note = m
		return r, nil
	case connectedMsg:
		r.t = m.t
		r.app = New(r.ctx, m.s, r.opts.Timeout)
		var cmds []tea.Cmd
		cmds = append(cmds, r.app.Init())
		if r.note.Text != "" { // e.g. the config file could not be written
			n := r.note
			cmds = append(cmds, func() tea.Msg { return n })
		}
		_, cmd := r.app.Update(tea.WindowSizeMsg{Width: r.w, Height: r.h})
		return r, tea.Batch(append(cmds, cmd)...)
	case tea.KeyPressMsg:
		switch m.String() {
		case "ctrl+c":
			return r, tea.Quit
		case "q":
			if !r.c.Capturing() && r.c.st != stWizBusy {
				return r, tea.Quit
			}
		}
	}
	return r, r.c.Update(msg)
}

func (r *Root) View() tea.View {
	if r.app != nil {
		return r.app.View()
	}
	v := tea.NewView(r.render())
	v.AltScreen = true
	v.WindowTitle = "owrt-tui"
	return v
}

func (r *Root) render() string {
	if r.w < 40 || r.h < 8 {
		return "terminal too small (need at least 40x8)"
	}
	mode := sGood.Render("read-only")
	if r.opts.Write {
		mode = sWarn.Render("WRITE")
	}
	head := sAccent.Render("owrt-tui") + "  " + sDim.Render("connect") + "  " + mode
	foot := sDim.Render(r.c.Keys() + "  q quit")
	if r.note.Text != "" {
		st := sGood
		if r.note.Error {
			st = sBad
		}
		foot = st.Render(r.note.Text)
	}
	bodyH := r.h - 3
	return lipgloss.JoinVertical(lipgloss.Left,
		clip(head, r.w),
		sDim.Render(strings.Repeat("─", r.w)),
		fitLines(r.c.View(r.w, bodyH), r.w, bodyH),
		clip(foot, r.w),
	)
}
