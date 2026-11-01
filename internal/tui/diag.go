package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/diag"
)

type diagScreen struct {
	e      *env
	input  textinput.Model
	vp     viewport.Model
	typing bool
	out    []string
	busy   string
	cancel context.CancelFunc
	feed   chan string
}

func newDiag(e *env) *diagScreen {
	in := textinput.New()
	in.Prompt = "host: "
	in.Placeholder = "1.1.1.1 or example.org"
	in.SetValue("1.1.1.1")
	return &diagScreen{e: e, input: in, vp: viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))}
}

func (d *diagScreen) Title() string   { return "Diagnostics" }
func (d *diagScreen) Init() tea.Cmd   { return nil }
func (d *diagScreen) Capturing() bool { return d.typing }
func (d *diagScreen) Keys() string {
	return "h edit host  p ping  t traceroute  n nslookup  d check router  c cancel"
}

type diagDoneMsg struct {
	title string
	text  string
	err   error
}
type traceLineMsg string
type traceEndMsg struct{ err error }

func (d *diagScreen) set(lines ...string) {
	d.out = lines
	d.vp.SetContent(strings.Join(lines, "\n"))
	d.vp.GotoTop()
}

func (d *diagScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case diagDoneMsg:
		d.busy = ""
		if m.err != nil {
			d.set(sBad.Render("error: " + m.err.Error()))
		} else {
			d.set(m.title, "", m.text)
		}
	case traceLineMsg:
		if d.busy == "" {
			return d, nil
		}
		d.out = append(d.out, string(m))
		d.vp.SetContent(strings.Join(d.out, "\n"))
		d.vp.GotoBottom()
		return d, d.wait(d.feed)
	case traceEndMsg:
		d.busy = ""
		d.feed = nil
		if m.err != nil {
			d.out = append(d.out, sBad.Render("error: "+m.err.Error()))
			d.vp.SetContent(strings.Join(d.out, "\n"))
		}
	case tea.KeyPressMsg:
		return d.key(m)
	}
	return d, nil
}

func (d *diagScreen) wait(ch chan string) tea.Cmd {
	return func() tea.Msg {
		l, ok := <-ch
		if !ok {
			return traceEndMsg{}
		}
		return traceLineMsg(l)
	}
}

func (d *diagScreen) key(m tea.KeyPressMsg) (screen, tea.Cmd) {
	if d.typing {
		switch m.String() {
		case "enter", "esc":
			d.typing = false
			d.input.Blur()
			return d, nil
		}
		var cmd tea.Cmd
		d.input, cmd = d.input.Update(m)
		return d, cmd
	}
	host := strings.TrimSpace(d.input.Value())
	switch m.String() {
	case "h", "/":
		d.typing = true
		return d, d.input.Focus()
	case "c":
		if d.cancel != nil {
			d.cancel()
			d.cancel = nil
		}
		d.busy = ""
	case "p":
		if d.busy != "" {
			return d, toast("busy: press c to cancel")
		}
		d.busy = "ping"
		d.set("pinging " + host + " ...")
		return d, d.e.call(func(ctx context.Context) tea.Msg {
			r, err := diag.Ping(ctx, d.e.S.T, host, 4)
			if err != nil {
				return diagDoneMsg{err: err}
			}
			txt := fmt.Sprintf("%d sent, %d received, %d%% loss", r.Sent, r.Received, r.LossPct)
			if r.Received > 0 {
				txt += fmt.Sprintf("\nrtt min/avg/max = %.1f/%.1f/%.1f ms", r.Min, r.Avg, r.Max)
			}
			return diagDoneMsg{title: "ping " + host, text: txt + "\n\n" + r.Raw}
		})
	case "d":
		if d.busy != "" {
			return d, toast("busy: press c to cancel")
		}
		d.busy = "doctor"
		d.set("checking what this router has ...")
		return d, d.e.call(func(ctx context.Context) tea.Msg {
			r, err := d.e.S.Doctor(ctx)
			return diagDoneMsg{title: "Router check: what each feature needs", text: r.Text(), err: err}
		})
	case "n":
		if d.busy != "" {
			return d, toast("busy: press c to cancel")
		}
		d.busy = "lookup"
		d.set("resolving " + host + " ...")
		return d, d.e.call(func(ctx context.Context) tea.Msg {
			out, err := diag.Nslookup(ctx, d.e.S.T, host, "")
			return diagDoneMsg{title: "nslookup " + host, text: out, err: err}
		})
	case "t":
		if d.busy != "" {
			return d, toast("busy: press c to cancel")
		}
		d.busy = "trace"
		d.set("traceroute to " + host)
		ctx, cancel := context.WithCancel(d.e.ctx)
		d.cancel = cancel
		ch := make(chan string, 32)
		d.feed = ch
		go func() {
			defer close(ch)
			if err := diag.Traceroute(ctx, d.e.S.T, host, func(l string) {
				select {
				case ch <- l:
				case <-ctx.Done():
				}
			}); err != nil && ctx.Err() == nil {
				select {
				case ch <- "error: " + err.Error():
				case <-ctx.Done():
				}
			}
		}()
		return d, d.wait(ch)
	default:
		var cmd tea.Cmd
		d.vp, cmd = d.vp.Update(m)
		return d, cmd
	}
	return d, nil
}

func (d *diagScreen) View(w, h int) string {
	d.vp.SetWidth(w)
	d.vp.SetHeight(h - 2)
	state := ""
	if d.busy != "" {
		state = "  " + sWarn.Render("running "+d.busy+" (c cancels)")
	}
	return " " + d.input.View() + state + "\n" + sDim.Render(strings.Repeat("─", w)) + "\n" + d.vp.View()
}
