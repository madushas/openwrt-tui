package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/events"
	"github.com/madushakv/owrt-tui/internal/health"
)

func TestEventsScreenShowsTimelineAndFilters(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Events")
	v := view(a)
	for _, want := range []string{"pixel-8", "joined Wi-Fi", "interface wan is up", "failed SSH login", "cron ran"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	es := scr(a, "Events").(*eventsScreen)
	if es.all[0].Kind != events.WifiLeave { // newest first
		t.Fatalf("order: %v", es.all[0].Kind)
	}
	press(t, a, "f") // clients
	v = view(a)
	if strings.Contains(v, "cron ran") || !strings.Contains(v, "left Wi-Fi") {
		t.Fatalf("filter clients:\n%s", v)
	}
	press(t, a, "f", "f") // security
	v = view(a)
	if !strings.Contains(v, "failed SSH login") || strings.Contains(v, "joined Wi-Fi") {
		t.Fatalf("filter security:\n%s", v)
	}
	if es.Status() != "filter: security" {
		t.Fatal(es.Status())
	}
}

func TestClientsTrafficRatesAndSort(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Clients")
	if v := view(a); !strings.Contains(v, "TRAFFIC") {
		t.Fatalf("no traffic column:\n%s", v)
	}
	cs := scr(a, "Clients").(*clientsScreen)
	if cs.trafficCell("192.168.1.50") == "" || strings.Contains(cs.trafficCell("192.168.1.50"), "/s") {
		t.Fatalf("first sample should show open-connection bytes, got %q", cs.trafficCell("192.168.1.50"))
	}
	press(t, a, "g")
	if got := cs.trafficCell("192.168.1.50"); !strings.Contains(got, "/s") {
		t.Fatalf("second sample should show rates, got %q", got)
	}
	press(t, a, "t")
	if cs.list[0].IP != "192.168.1.50" || cs.list[len(cs.list)-1].IP == "192.168.1.50" {
		t.Fatalf("sort by traffic: %+v", cs.list)
	}
}

func TestHeaderAlertChip(t *testing.T) {
	a := newApp(t, true)
	a.env.alertBad, a.env.alertWarn = 1, 2
	a.w, a.h = 120, 30
	if h := a.header(120); !strings.Contains(h, "▲ 3 alert(s)") {
		t.Fatalf("no chip: %q", h)
	}
	a.env.alertBad, a.env.alertWarn = 0, 0
	if h := a.header(120); strings.Contains(h, "alert") {
		t.Fatalf("chip with no alerts: %q", h)
	}
	// the Health screen feeds the chip
	h := scr(a, "Health").(*healthScreen)
	h.Update(healthMsg{list: []health.Finding{{Sev: health.Bad}, {Sev: health.Warn}, {Sev: health.Info}}})
	if a.env.alertBad != 1 || a.env.alertWarn != 1 {
		t.Fatalf("%d %d", a.env.alertBad, a.env.alertWarn)
	}
}

func TestDemoHealthShowsWeakSignalNote(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Health")
	if v := view(a); !strings.Contains(v, "cellular signal is weak") {
		t.Fatalf("missing LTE note:\n%s", v)
	}
}

func TestDiagnosticsCheckRouterKey(t *testing.T) {
	a := writeApp(t)
	openTab(t, a, "Diagnostics")
	press(t, a, "d")
	d := scr(a, "Diagnostics").(*diagScreen)
	a.Update(d.e.call(func(ctx context.Context) tea.Msg {
		r, err := d.e.S.Doctor(ctx)
		return diagDoneMsg{title: "Router check: what each feature needs", text: r.Text(), err: err}
	})())
	if v := view(a); !strings.Contains(v, "MISSING") || !strings.Contains(v, "Router check") {
		t.Fatalf("check not shown:\n%s", v)
	}
}
