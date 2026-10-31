package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/clients"
	"github.com/madushakv/owrt-tui/internal/events"
	"github.com/madushakv/owrt-tui/internal/logs"
)

const eventBacklog = 2000

type eventsScreen struct {
	e       *env
	all     []events.Event
	group   int // 0 = all, then events.Groups
	cur     int
	loading bool
	err     error
}

func newEvents(e *env) *eventsScreen { return &eventsScreen{e: e, loading: true} }

func (s *eventsScreen) Title() string   { return "Events" }
func (s *eventsScreen) Capturing() bool { return false }
func (s *eventsScreen) Keys() string {
	return "f filter (all/clients/network/security/system)  g reload  up/down move"
}

func (s *eventsScreen) Status() string {
	if s.group == 0 {
		return ""
	}
	return "filter: " + string(events.Groups[s.group-1])
}

type eventsMsg struct {
	list []events.Event
	err  error
}

func (s *eventsScreen) Init() tea.Cmd { return s.load() }

func (s *eventsScreen) load() tea.Cmd {
	return s.e.call(func(ctx context.Context) tea.Msg {
		en, err := logs.Fetch(ctx, s.e.S.T, eventBacklog)
		if err != nil {
			return eventsMsg{err: err}
		}
		// Friendly names for MACs we know from the configuration; best effort.
		names := map[string]string{}
		if cfgs, err := s.e.loadUCI(ctx); err == nil {
			if raw, err := s.e.S.Clients(ctx); err == nil {
				for _, c := range clients.Merge(raw, cfgs) {
					if c.Named {
						names[strings.ToLower(c.MAC)] = c.Name
					}
				}
			}
		}
		return eventsMsg{list: events.Build(en, names)}
	})
}

func (s *eventsScreen) shown() []events.Event {
	if s.group == 0 {
		return s.all
	}
	return events.Filter(s.all, events.Groups[s.group-1])
}

func (s *eventsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case eventsMsg:
		s.loading, s.err = false, m.err
		if m.err == nil {
			s.all = m.list
			s.cur = clampInt(s.cur, 0, len(s.shown())-1)
		}
	case tea.KeyPressMsg:
		switch m.String() {
		case "up", "k":
			s.cur = clampInt(s.cur-1, 0, len(s.shown())-1)
		case "down", "j":
			s.cur = clampInt(s.cur+1, 0, len(s.shown())-1)
		case "f":
			s.group = (s.group + 1) % (len(events.Groups) + 1)
			s.cur = 0
		case "g":
			s.loading = true
			return s, s.load()
		}
	}
	return s, nil
}

func (s *eventsScreen) View(w, h int) string {
	if s.loading && len(s.all) == 0 {
		return "\n  reading the log..."
	}
	if s.err != nil {
		return "\n  " + sBad.Render("error: "+s.err.Error())
	}
	list := s.shown()
	var b strings.Builder
	head := fmt.Sprintf("  %d event(s) from the last %d log lines", len(list), eventBacklog)
	if bf := events.BruteForce(s.all, 5); len(bf) > 0 {
		var ips []string
		for ip, n := range bf {
			ips = append(ips, fmt.Sprintf("%s (%d)", ip, n))
		}
		b.WriteString(sBad.Render("  repeated failed SSH logins from "+strings.Join(ips, ", ")) + "\n")
		h--
	}
	b.WriteString(sDim.Render(head) + "\n")
	if len(list) == 0 {
		return b.String() + "\n  nothing to show for this filter"
	}
	lo, hi := window(len(list), s.cur, h-1)
	for i := lo; i < hi; i++ {
		ev := list[i]
		line := fmt.Sprintf("  %-15s %-9s %s", shortTime(ev.Time), ev.Group, ev.Label())
		switch {
		case i == s.cur:
			line = sSel.Render(clip(line, w))
		case ev.Sev == events.Bad:
			line = sBad.Render(clip(line, w))
		case ev.Sev == events.Warn:
			line = sWarn.Render(clip(line, w))
		default:
			line = clip(line, w)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
