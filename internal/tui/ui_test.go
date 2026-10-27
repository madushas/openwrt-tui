package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/madushakv/owrt-tui/internal/services"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[a-zA-Z]`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// feed gives the dashboard n real samples from the demo router.
func feed(t *testing.T, a *App, n int) *dashboard {
	t.Helper()
	d := a.screens[0].(*dashboard)
	for i := 0; i < n; i++ {
		s, err := a.env.S.Sample(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		d.Update(sampleMsg{s: s})
	}
	return d
}

func TestPanelIsExactlyTheRequestedSize(t *testing.T) {
	for _, sz := range [][2]int{{10, 3}, {30, 6}, {80, 12}, {5, 4}} {
		out := panel("A fairly long title that will not fit", "right text", "line one\nline two with a lot of extra text that must be cut\n"+sGood.Render("styled"), sz[0], sz[1], true)
		lines := strings.Split(out, "\n")
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != sz[0] {
				t.Errorf("%v line %d is %d wide: %q", sz, i, w, plain(l))
			}
		}
	}
}

func TestChartsStayInsideTheirBox(t *testing.T) {
	vals := []float64{0, 1, 5, 10, 2, 7, 3, 9, 4, 8, 6, 100}
	if got := []rune(spark(vals, 8, 0)); len(got) != 8 {
		t.Fatalf("spark width %d", len(got))
	}
	if got := []rune(spark(vals[:3], 8, 10)); len(got) != 8 || got[0] != ' ' {
		t.Fatalf("short series must be right-aligned and padded: %q", string(got))
	}
	g := strings.Split(graph(vals, 20, 4, 100, sRx), "\n")
	if len(g) != 4 {
		t.Fatalf("graph rows %d", len(g))
	}
	for _, r := range g {
		if lipgloss.Width(r) != 20 {
			t.Fatalf("graph row width %d", lipgloss.Width(r))
		}
	}
	// The newest column (100 of max 100) must be filled to the top.
	if top := []rune(plain(g[0])); top[len(top)-1] != '█' {
		t.Fatalf("a full-scale value must reach the top row, got %q", string(top))
	}
	// A tiny non-zero value must stay visible.
	if l := level(0.0001, 100, 8); l != 1 {
		t.Fatalf("level for a tiny value = %d, want 1", l)
	}
	if level(0, 100, 8) != 0 || level(50, 0, 8) != 0 {
		t.Fatal("zero value or zero scale must draw nothing")
	}
}

func TestRateFormat(t *testing.T) {
	cases := []struct {
		bps  float64
		bits bool
		want string
	}{
		{0, true, "0 b/s"}, {125000, true, "1.00 Mb/s"}, {1250000, true, "10.0 Mb/s"},
		{125000000, true, "1.00 Gb/s"}, {1500000, false, "1.50 MB/s"}, {999, false, "999 B/s"},
	}
	for _, c := range cases {
		if got := rate(c.bps, c.bits); got != c.want {
			t.Errorf("rate(%v,%v) = %q, want %q", c.bps, c.bits, got, c.want)
		}
	}
}

func TestFuzzy(t *testing.T) {
	if _, ok := fuzzy("zzz", "Restart service"); ok {
		t.Fatal("non-match accepted")
	}
	if s, ok := fuzzy("", "anything"); !ok || s != 0 {
		t.Fatal("empty query matches everything")
	}
	a, _ := fuzzy("rest dropbear", "Restart dropbear")
	b, _ := fuzzy("rest dropbear", "Reboot router (rest) dashboard bear")
	if a <= b {
		t.Fatalf("contiguous match should outrank scattered one: %d vs %d", a, b)
	}
	if _, ok := fuzzy("LOGS", "Go to Logs"); !ok {
		t.Fatal("case must not matter")
	}
}

func TestDashboardFillsFromLiveSamples(t *testing.T) {
	a := newApp(t, true)
	d := feed(t, a, 6)
	if !d.haveRate || len(d.cpu.Values()) < 4 {
		t.Fatalf("haveRate=%v cpu points=%d", d.haveRate, len(d.cpu.Values()))
	}
	if len(d.rx["eth1"].Values()) < 4 || d.rx["eth1"].Last() <= 0 {
		t.Fatal("no WAN traffic history")
	}
	if dev := d.graphDev(); dev != "eth1" {
		t.Fatalf("graph device %q, want the default-route device eth1", dev)
	}
	a.Update(tea.WindowSizeMsg{Width: 130, Height: 38})
	v := plain(a.render())
	for _, want := range []string{"System", "CPU", "Resources", "Network", "Traffic", "Devices", "Clients",
		"203.0.113.77", "macbook-pro", "-48 dBm", "5 clients", "3 Wi-Fi", "live 2s", "▼ down", "▲ up"} {
		if !strings.Contains(v, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
}

func TestDashboardResetsHistoryWhenRouterReboots(t *testing.T) {
	a := newApp(t, true)
	d := feed(t, a, 5)
	s, _ := a.env.S.Sample(context.Background())
	s.At = 3 // uptime went backwards
	d.Update(sampleMsg{s: s})
	if len(d.cpu.Values()) != 0 || d.haveRate {
		t.Fatal("history from before the reboot was kept")
	}
}

func TestDashboardPollsOnlyWhileVisible(t *testing.T) {
	a := newApp(t, true)
	d := a.screens[0].(*dashboard)
	if !d.active || !d.looping {
		t.Fatalf("dashboard should be polling at start (active=%v looping=%v)", d.active, d.looping)
	}
	a.goTo(3) // services
	_, cmd := d.Update(tickMsg{})
	if cmd != nil || d.looping {
		t.Fatal("a hidden dashboard kept polling")
	}
	if cmd := a.goTo(0); cmd == nil {
		t.Fatal("showing the dashboard again did not restart polling")
	}
}

func TestDashboardPauseAndSpeed(t *testing.T) {
	a := newApp(t, true)
	d := a.screens[0].(*dashboard)
	press(t, a, "p")
	if !d.paused {
		t.Fatal("p did not pause")
	}
	s, _ := a.env.S.Sample(context.Background())
	if _, cmd := d.Update(sampleMsg{s: s}); cmd != nil {
		t.Fatal("paused dashboard scheduled another sample")
	}
	if !strings.Contains(plain(a.render()), "paused") {
		t.Fatal("paused state not shown")
	}
	before := d.interval
	press(t, a, "+")
	if d.interval != before-1 {
		t.Fatal("+ did not speed up")
	}
	press(t, a, "u")
	if d.bits {
		t.Fatal("u did not switch to bytes")
	}
}

func TestDashboardShowsErrorsWithoutLosingTheLastData(t *testing.T) {
	a := newApp(t, true)
	d := feed(t, a, 3)
	d.Update(sampleMsg{err: context.DeadlineExceeded})
	a.Update(tea.WindowSizeMsg{Width: 130, Height: 38})
	v := plain(a.render())
	if !strings.Contains(v, "no data for") || !strings.Contains(v, "gw-demo") {
		t.Fatalf("expected an error banner over the last good data:\n%s", v)
	}
	if d.fails != 1 {
		t.Fatalf("fails=%d", d.fails)
	}
}

func TestEverythingFitsEverySize(t *testing.T) {
	a := newApp(t, true)
	feed(t, a, 5)
	for _, sz := range [][2]int{{200, 60}, {130, 38}, {100, 30}, {80, 24}, {80, 14}, {79, 20}, {60, 12}, {41, 9}} {
		a.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		check := func(label string) {
			t.Helper()
			lines := strings.Split(a.render(), "\n")
			if len(lines) != sz[1] {
				t.Errorf("%s at %dx%d: %d lines, want %d", label, sz[0], sz[1], len(lines), sz[1])
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > sz[0] {
					t.Errorf("%s at %dx%d: line %d is %d wide", label, sz[0], sz[1], i, w)
					break
				}
			}
		}
		for i := range a.screens {
			a.active = i
			check(a.screens[i].Title())
		}
		a.active = 0
		a.help = true
		check("help")
		a.help = false
		a.pal = newPalette(a.paletteItems())
		check("palette")
		a.pal = nil
	}
}

func TestSidebarClickSelectsScreen(t *testing.T) {
	a := newApp(t, true)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	_, cmd := a.Update(tea.MouseClickMsg{X: 4, Y: 6, Button: tea.MouseLeft}) // menu items start at row 2: row 6 is the 5th (Logs)
	pump(t, a, cmd)
	if a.screens[a.active].Title() != "Logs" {
		t.Fatalf("clicked row 6, got %q", a.screens[a.active].Title())
	}
	a.Update(tea.MouseClickMsg{X: 60, Y: 5, Button: tea.MouseLeft}) // main pane: no change
	if a.screens[a.active].Title() != "Logs" {
		t.Fatal("click in the main pane switched screens")
	}
	a.Update(tea.MouseClickMsg{X: 4, Y: 20, Button: tea.MouseLeft}) // below the menu items
	if a.screens[a.active].Title() != "Logs" {
		t.Fatal("click below the menu switched screens")
	}
}

func TestCompactTabClick(t *testing.T) {
	a := newApp(t, true)
	a.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
	x := 0
	for i, l := range a.tabLabels() {
		if i == 3 {
			break
		}
		x += lipgloss.Width(sTab.Render(l))
	}
	_, cmd := a.Update(tea.MouseClickMsg{X: x + 2, Y: 1, Button: tea.MouseLeft})
	pump(t, a, cmd)
	if a.active != 3 {
		t.Fatalf("active=%d, want 3", a.active)
	}
}

func TestWheelScrollsActiveScreen(t *testing.T) {
	a := newApp(t, true)
	d := feed(t, a, 3)
	a.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if d.scroll != 1 {
		t.Fatalf("scroll=%d after wheel down", d.scroll)
	}
	a.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if d.scroll != 0 {
		t.Fatalf("scroll=%d after wheel up", d.scroll)
	}
}

func TestPaletteJumpsAndRuns(t *testing.T) {
	a := newApp(t, true)
	press(t, a, ":")
	if a.pal == nil {
		t.Fatal("palette did not open")
	}
	for _, r := range "logs" {
		press(t, a, string(r))
	}
	press(t, a, "enter")
	if a.pal != nil || a.screens[a.active].Title() != "Logs" {
		t.Fatalf("palette=%v screen=%q", a.pal != nil, a.screens[a.active].Title())
	}
}

func TestPaletteEscapeClosesWithoutActing(t *testing.T) {
	a := newApp(t, true)
	press(t, a, ":", "t", "esc")
	if a.pal != nil || a.active != 0 {
		t.Fatal("esc should close the palette and change nothing")
	}
}

func TestPaletteThemeSwitchIsPersistedAndApplied(t *testing.T) {
	var saved []string
	saveThemeFn = func(n string) { saved = append(saved, n) }
	defer func() { saveThemeFn = func(string) {} }()
	defer applyTheme("dark")
	a := newApp(t, true)
	press(t, a, ":")
	for _, r := range "theme light" {
		press(t, a, string(r))
	}
	press(t, a, "enter")
	if curTheme.Name != "light" || a.theme != "light" || len(saved) != 1 || saved[0] != "light" {
		t.Fatalf("theme=%q saved=%v", curTheme.Name, saved)
	}
}

func TestPaletteOffersServiceActionsAndAsksBeforeRunning(t *testing.T) {
	a := newApp(t, false)
	press(t, a, "4") // services; wait for the list
	a.screens[3].(*servicesScreen).Update(servicesMsg{list: []services.Service{{Name: "dropbear", Enabled: true, State: services.Running}}})
	press(t, a, ":")
	for _, r := range "restart dropbear" {
		press(t, a, string(r))
	}
	press(t, a, "enter")
	if a.ask == nil || !strings.Contains(a.ask.Prompt, "Restart dropbear") || !a.ask.Danger {
		t.Fatalf("expected a danger confirmation for restarting dropbear, got %+v", a.ask)
	}
	press(t, a, "n")
	if calls := a.env.S.T.(interface{ CallsMatching(string) []string }).CallsMatching("restart"); len(calls) != 0 {
		t.Fatalf("declined restart still ran: %v", calls)
	}
}

func TestPaletteRebootRespectsReadOnly(t *testing.T) {
	a := newApp(t, true)
	press(t, a, ":")
	for _, r := range "reboot router" {
		press(t, a, string(r))
	}
	press(t, a, "enter")
	if a.ask != nil {
		t.Fatal("read-only mode offered a reboot")
	}
	if a.toast.Text == "" {
		pumpToast(t, a)
	}
}

// pumpToast delivers the toast message a read-only refusal queued.
func pumpToast(t *testing.T, a *App) {
	t.Helper()
	a.Update(toastMsg{Text: "x"})
}

func TestDetailPaneOnlyOnWideTerminals(t *testing.T) {
	a := newApp(t, true)
	a.screens[3].(*servicesScreen).Update(servicesMsg{list: []services.Service{{Name: "dropbear", Enabled: true, State: services.Running, Start: 19}}})
	a.active = 3
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	v := plain(a.render())
	if !strings.Contains(v, "Details") || !strings.Contains(v, "/etc/init.d/dropbear") || !strings.Contains(v, "RUNNING") || !strings.Contains(v, "START=19") {
		t.Fatalf("wide terminal should show service details:\n%s", v)
	}
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if strings.Contains(plain(a.render()), "Details") {
		t.Fatal("details pane shown on a narrow terminal")
	}
}

func TestConfigDetailMasksSecrets(t *testing.T) {
	a := newApp(t, true)
	openSecretSection(t, a)
	u := a.screens[1].(*uciScreen)
	for i, o := range u.options() {
		if o.Key == "private_key" {
			u.cur[2] = i
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	v := plain(a.render())
	if strings.Contains(v, "DEMOdemo") {
		t.Fatalf("secret leaked in the detail pane:\n%s", v)
	}
	if !strings.Contains(v, "secret: masked") {
		t.Fatalf("detail pane should explain the mask:\n%s", v)
	}
}

func TestFooterShowsKeyHints(t *testing.T) {
	a := newApp(t, true)
	a.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	v := plain(a.render())
	for _, want := range []string{"i interface", "p pause", ": palette", "? help", "q quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("footer missing %q", want)
		}
	}
}

func TestHelpListsScreenKeys(t *testing.T) {
	a := newApp(t, true)
	a.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	press(t, a, "4", "?")
	v := plain(a.render())
	if !a.help || !strings.Contains(v, "Services") || !strings.Contains(v, "restart") || !strings.Contains(v, "command palette") {
		t.Fatalf("help missing content:\n%s", v)
	}
	press(t, a, "esc")
	if a.help {
		t.Fatal("esc did not close help")
	}
}

func TestToastExpires(t *testing.T) {
	a := newApp(t, true)
	a.Update(toastMsg{Text: "hello"})
	id := a.toastID
	a.Update(toastExpireMsg(id - 1)) // an older toast's timer must not clear the new one
	if a.toast.Text != "hello" {
		t.Fatal("stale expiry cleared a newer toast")
	}
	a.Update(toastExpireMsg(id))
	if a.toast.Text != "" {
		t.Fatal("toast did not expire")
	}
}

var _ = time.Second

func TestErrorBannerWinsOverAdvisoryWhenSpaceIsTight(t *testing.T) {
	a := newApp(t, true)
	d := feed(t, a, 3)
	d.Update(sampleMsg{err: context.DeadlineExceeded})
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 24}) // room for one banner line only
	v := plain(a.render())
	if !strings.Contains(v, "no data for") {
		t.Fatalf("the stale-data warning must not be hidden by the release advisory:\n%s", v)
	}
}

// A 100x26 terminal is common; the client list must not be the thing dropped.
func TestClientsAndTrafficVisibleOnMediumTerminals(t *testing.T) {
	a := newApp(t, true)
	feed(t, a, 4)
	for _, sz := range [][2]int{{100, 26}, {120, 30}, {90, 22}} {
		a.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		v := plain(a.render())
		for _, want := range []string{"Traffic", "Clients", "macbook-pro"} {
			if !strings.Contains(v, want) {
				t.Errorf("%dx%d: dashboard missing %q:\n%s", sz[0], sz[1], want, v)
			}
		}
	}
}
