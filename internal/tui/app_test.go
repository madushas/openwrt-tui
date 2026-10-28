package tui

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/demo"
	"github.com/madushakv/owrt-tui/internal/session"
)

// pump runs cmd and feeds every resulting message back into the app. Commands
// that block (timers, streams) are abandoned after a short wait.
func pump(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				pump(t, a, c)
			}
			return
		}
		if msg == nil {
			return
		}
		_, next := a.Update(msg)
		pump(t, a, next)
	case <-time.After(200 * time.Millisecond):
	}
}

// idx finds a screen by its title so tests do not depend on tab order.
func idx(a *App, title string) int {
	for i, s := range a.screens {
		if s.Title() == title {
			return i
		}
	}
	panic("no screen " + title)
}

// tab is the key that opens the titled screen.
func tab(a *App, title string) string { return strconv.Itoa(idx(a, title) + 1) }

func scr(a *App, title string) screen { return a.screens[idx(a, title)] }

func newApp(t *testing.T, readOnly bool) *App {
	t.Helper()
	ctx := context.Background()
	s, err := session.Connect(ctx, demo.Router(), readOnly)
	if err != nil {
		t.Fatal(err)
	}
	a := New(ctx, s, 5*time.Second)
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pump(t, a, a.Init())
	return a
}

func press(t *testing.T, a *App, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var m tea.KeyPressMsg
		switch k {
		case "enter":
			m = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			m = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			m = tea.KeyPressMsg{Code: tea.KeyTab}
		case "down":
			m = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			r := []rune(k)
			m = tea.KeyPressMsg{Code: r[0], Text: k}
		}
		_, cmd := a.Update(m)
		pump(t, a, cmd)
	}
}

func view(a *App) string { return a.render() }

func TestScreensShowDemoData(t *testing.T) {
	a := newApp(t, true)
	if v := view(a); !strings.Contains(v, "gw-demo") {
		t.Fatalf("overview missing hostname:\n%s", v)
	}
	press(t, a, tab(a, "Config"))
	if v := view(a); !strings.Contains(v, "network") || !strings.Contains(v, "wireless") {
		t.Fatalf("config screen missing configs:\n%s", v)
	}
	press(t, a, tab(a, "Services"))
	if v := view(a); !strings.Contains(v, "dropbear") {
		t.Fatalf("services screen missing dropbear:\n%s", v)
	}
	press(t, a, tab(a, "Logs"))
	if v := view(a); !strings.Contains(v, "daemon") && !strings.Contains(v, "level") {
		t.Fatalf("logs screen empty:\n%s", v)
	}
	press(t, a, tab(a, "Packages"))
	if v := view(a); !strings.Contains(v, "installed") {
		t.Fatalf("packages screen empty:\n%s", v)
	}
}

func TestEveryScreenFitsTheTerminal(t *testing.T) {
	a := newApp(t, true)
	for _, size := range [][2]int{{100, 30}, {60, 12}, {41, 9}} {
		a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for i := range a.screens {
			a.active = i
			lines := strings.Split(view(a), "\n")
			if len(lines) != size[1] {
				t.Errorf("screen %q at %dx%d: %d lines, want %d", a.screens[i].Title(), size[0], size[1], len(lines), size[1])
			}
		}
	}
}

// openWireguardOptions navigates to network > the wireguard peer/interface that
// holds the demo private key.
func openSecretSection(t *testing.T, a *App) {
	t.Helper()
	press(t, a, tab(a, "Config"), "enter") // network
	u := scr(a, "Config").(*uciScreen)
	for i, s := range u.sections() {
		for _, o := range s.Options {
			if o.Key == "private_key" {
				u.cur[1] = i
				press(t, a, "enter")
				return
			}
		}
	}
	t.Fatal("no section with private_key in demo data")
}

func TestSecretsAreMaskedUntilRevealed(t *testing.T) {
	a := newApp(t, true)
	openSecretSection(t, a)
	v := view(a)
	if strings.Contains(v, "DEMOdemo") {
		t.Fatalf("secret visible by default:\n%s", v)
	}
	if !strings.Contains(v, "********") {
		t.Fatalf("no mask shown:\n%s", v)
	}
	press(t, a, "v")
	if !strings.Contains(view(a), "DEMOdemo") {
		t.Fatal("reveal did not show the value")
	}
}

func TestReadOnlyStagesNothing(t *testing.T) {
	a := newApp(t, true)
	openSecretSection(t, a)
	press(t, a, "e")
	if scr(a, "Config").Capturing() {
		t.Fatal("edit mode opened in read-only mode")
	}
	press(t, a, "d")
	if a.env.Staged.Len() != 0 {
		t.Fatal("read-only mode staged a change")
	}
}

func TestStagedSecretIsMaskedOnChangesScreen(t *testing.T) {
	a := newApp(t, false)
	openSecretSection(t, a)
	u := scr(a, "Config").(*uciScreen)
	for i, o := range u.options() {
		if o.Key == "private_key" {
			u.cur[2] = i
		}
	}
	press(t, a, "e")
	if !scr(a, "Config").Capturing() {
		t.Fatal("edit did not start")
	}
	for _, r := range "NEWsecretVALUE" {
		press(t, a, string(r))
	}
	press(t, a, "enter")
	if a.env.Staged.Len() != 1 {
		t.Fatalf("staged %d changes, want 1", a.env.Staged.Len())
	}
	press(t, a, tab(a, "Changes"))
	v := view(a)
	if strings.Contains(v, "NEWsecret") || strings.Contains(v, "DEMOdemo") {
		t.Fatalf("secret leaked on the changes screen:\n%s", v)
	}
	if !strings.Contains(v, "private_key") {
		t.Fatalf("staged change not listed:\n%s", v)
	}
}

func TestApplyAsksBeforeSending(t *testing.T) {
	a := newApp(t, false)
	openSecretSection(t, a)
	press(t, a, "e", "x", "enter") // stage something
	press(t, a, tab(a, "Changes"), "a")
	if a.ask == nil {
		t.Fatal("apply did not ask for confirmation")
	}
	// Declining must send nothing.
	f := a.env.S.T.(interface{ CallsMatching(string) []string })
	before := len(f.CallsMatching("uci"))
	press(t, a, "n")
	if a.ask != nil || len(f.CallsMatching("uci")) != before {
		t.Fatal("declined apply still did something")
	}
}

func TestQuitKey(t *testing.T) {
	a := newApp(t, true)
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q produced no QuitMsg")
	}
}
