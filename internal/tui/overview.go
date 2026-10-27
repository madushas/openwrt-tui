package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/device"
)

type overview struct{ e *env }

func newOverview(e *env) *overview { return &overview{e: e} }

func (o *overview) Title() string   { return "Overview" }
func (o *overview) Init() tea.Cmd   { return nil }
func (o *overview) Capturing() bool { return false }
func (o *overview) Keys() string    { return "R reboot" }

func (o *overview) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case rebootDoneMsg:
		if m.err != nil {
			return o, toastErr(m.err)
		}
		return o, toast("reboot sent; the connection will drop and the router is back in about a minute")
	case tea.KeyPressMsg:
		if m.String() == "R" {
			if o.e.S.ReadOnly {
				return o, toast("read-only mode: start with -write to reboot")
			}
			return o, func() tea.Msg {
				return askMsg{Prompt: "Reboot " + o.e.S.T.Describe() + " now?", Danger: true, Yes: func() tea.Cmd {
					return o.e.call(func(ctx context.Context) tea.Msg { return rebootDoneMsg{o.e.S.Reboot(ctx)} })
				}}
			}
		}
	}
	return o, nil
}

func space(label string, s device.Space) string {
	if s.Total == 0 {
		return ""
	}
	pct := int(100 * s.Used / s.Total)
	line := fmt.Sprintf("%s used %d%%  (%s free of %s)", label, pct, kib(s.Avail), kib(s.Total))
	if s.Avail < 4096 {
		return sWarn.Render(line + "  low")
	}
	return line
}

func (o *overview) View(w, h int) string {
	i := o.e.S.Info
	row := func(k, v string) string {
		if v == "" {
			return ""
		}
		return fmt.Sprintf(" %s %s\n", sDim.Render(fmt.Sprintf("%-10s", k)), v)
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(row("Host", sBold.Render(i.Hostname)))
	b.WriteString(row("Model", i.Model))
	b.WriteString(row("Release", strings.TrimSpace(i.Distro+" "+i.Version+"  "+i.Revision)))
	b.WriteString(row("Target", i.Target))
	b.WriteString(row("Kernel", i.Kernel))
	b.WriteString(row("Packages", i.PkgMgr))
	b.WriteString(row("Firewall", i.Firewall))
	if i.UptimeSecs > 0 {
		b.WriteString(row("Uptime", uptime(i.UptimeSecs)))
	}
	b.WriteString(row("Overlay", space("", i.OverlayKB)))
	b.WriteString(row("RAM disk", space("", i.TmpKB)))
	if i.Swconfig {
		b.WriteString(row("Switch", "legacy swconfig present"))
	}
	var tools []string
	for t, ok := range i.Tools {
		if ok {
			tools = append(tools, t)
		}
	}
	sort.Strings(tools)
	b.WriteString(row("Tools", sDim.Render(strings.Join(tools, " "))))
	if adv := i.Advisories(); len(adv) > 0 {
		b.WriteString("\n")
		for _, a := range adv {
			st := sWarn
			if a.Level == "info" {
				st = sDim
			}
			b.WriteString(" " + st.Render("! "+a.Text) + "\n")
		}
	}
	return b.String()
}
