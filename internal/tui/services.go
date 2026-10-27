package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/services"
)

type servicesScreen struct {
	e       *env
	list    []services.Service
	cur     int
	loading bool
	err     error
}

func newServices(e *env) *servicesScreen { return &servicesScreen{e: e, loading: true} }

func (s *servicesScreen) Title() string   { return "Services" }
func (s *servicesScreen) Capturing() bool { return false }
func (s *servicesScreen) Keys() string {
	return "s start  x stop  r restart  e enable  d disable  g reload list"
}

type servicesMsg struct {
	list []services.Service
	err  error
}
type serviceDoneMsg struct {
	name   string
	action services.Action
	out    string
	err    error
}

func (s *servicesScreen) Init() tea.Cmd { return s.load() }

func (s *servicesScreen) load() tea.Cmd {
	return s.e.call(func(ctx context.Context) tea.Msg {
		l, err := s.e.S.Services(ctx)
		return servicesMsg{l, err}
	})
}

func (s *servicesScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case servicesMsg:
		s.loading, s.err = false, m.err
		if m.err == nil {
			sort.Slice(m.list, func(i, j int) bool { return m.list[i].Name < m.list[j].Name })
			s.list = m.list
			s.cur = clampInt(s.cur, 0, len(s.list)-1)
		}
	case serviceDoneMsg:
		if m.err != nil {
			return s, toastErr(fmt.Errorf("%s %s: %w", m.action, m.name, m.err))
		}
		return s, tea.Batch(toast(fmt.Sprintf("%s %s: done", m.action, m.name)), s.load())
	case tea.KeyPressMsg:
		return s.key(m.String())
	}
	return s, nil
}

var svcKeys = map[string]services.Action{
	"s": services.Start, "x": services.Stop, "r": services.Restart,
	"e": services.Enable, "d": services.Disable,
}

func (s *servicesScreen) key(k string) (screen, tea.Cmd) {
	switch k {
	case "up", "k":
		s.cur = clampInt(s.cur-1, 0, len(s.list)-1)
	case "down", "j":
		s.cur = clampInt(s.cur+1, 0, len(s.list)-1)
	case "g":
		s.loading = true
		return s, s.load()
	default:
		act, ok := svcKeys[k]
		if !ok || len(s.list) == 0 {
			return s, nil
		}
		return s, s.request(s.list[s.cur].Name, act)
	}
	return s, nil
}

// request asks for confirmation, then runs the action. In read-only mode it
// only explains why nothing happens.
func (s *servicesScreen) request(name string, act services.Action) tea.Cmd {
	if s.e.S.ReadOnly {
		return toast("read-only mode: start with -write to change services")
	}
	prompt := fmt.Sprintf("%s %s?", capitalize(string(act)), name)
	warn := services.Warning(name, act)
	if warn != "" {
		prompt += " Warning: " + warn + "."
	}
	return func() tea.Msg {
		return askMsg{Prompt: prompt, Danger: warn != "", Yes: func() tea.Cmd {
			return s.e.call(func(ctx context.Context) tea.Msg {
				out, err := s.e.S.ServiceDo(ctx, name, act)
				return serviceDoneMsg{name, act, out, err}
			})
		}}
	}
}

// Commands adds one palette entry per service and action.
func (s *servicesScreen) Commands() []paletteItem {
	var out []paletteItem
	for _, sv := range s.list {
		sv := sv
		for _, act := range []services.Action{services.Restart, services.Start, services.Stop, services.Enable, services.Disable} {
			act := act
			out = append(out, paletteItem{Title: capitalize(string(act)) + " " + sv.Name, Hint: "service", Run: func() tea.Cmd { return s.request(sv.Name, act) }})
		}
	}
	return out
}

// Detail describes the selected service.
func (s *servicesScreen) Detail(w, h int) string {
	if s.cur < 0 || s.cur >= len(s.list) {
		return ""
	}
	sv := s.list[s.cur]
	var b strings.Builder
	b.WriteString(" " + sBold.Render(sv.Name) + "\n\n")
	switch sv.State {
	case services.Running:
		b.WriteString(" " + chipGood.Render("RUNNING") + "\n")
	case services.Stopped:
		b.WriteString(" " + chipWarn.Render("STOPPED") + "\n")
	default:
		b.WriteString(" " + chipDim.Render("NO INSTANCE") + "\n")
	}
	boot := "disabled: will not start at boot"
	if sv.Enabled {
		boot = "enabled: starts at boot"
	}
	b.WriteString("\n " + sDim.Render("Boot   ") + boot + "\n")
	if sv.Start > 0 {
		b.WriteString(" " + sDim.Render("Order  ") + fmt.Sprintf("START=%d (lower starts first)", sv.Start) + "\n")
	}
	b.WriteString(" " + sDim.Render("Script ") + "/etc/init.d/" + sv.Name + "\n\n")
	b.WriteString(" " + sDim.Render("Same as: ") + "service " + sv.Name + " restart\n")
	if w := services.Warning(sv.Name, services.Restart); w != "" {
		b.WriteString("\n")
		for _, l := range wrap(w, 34) {
			b.WriteString(" " + sWarn.Render("! "+l) + "\n")
		}
	}
	return b.String()
}

func (s *servicesScreen) View(w, h int) string {
	if s.loading && len(s.list) == 0 {
		return "\n  loading init scripts..."
	}
	if s.err != nil {
		return "\n  " + sBad.Render("error: "+s.err.Error())
	}
	var b strings.Builder
	b.WriteString(sDim.Render(fmt.Sprintf("  %-24s %-9s %-8s %s", "NAME", "STATE", "BOOT", "START")) + "\n")
	lo, hi := window(len(s.list), s.cur, h-1)
	for i := lo; i < hi; i++ {
		sv := s.list[i]
		boot := sDim.Render("off")
		if sv.Enabled {
			boot = sGood.Render("on")
		}
		state := sv.State.String()
		switch sv.State {
		case services.Running:
			state = sGood.Render(state)
		case services.Stopped:
			state = sWarn.Render(state)
		default:
			state = sDim.Render(state)
		}
		line := fmt.Sprintf("  %-24s %-9s %-8s %d", sv.Name, padVisible(state, 9), padVisible(boot, 8), sv.Start)
		if i == s.cur {
			line = sSel.Render(clip(fmt.Sprintf("  %-24s %-9s %-8s %d", sv.Name, sv.State, onOff(sv.Enabled), sv.Start), w))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
