package tui

import (
	"strings"
	"testing"
)

func TestClientsScreenAndActions(t *testing.T) {
	a := writeApp(t)
	withStore(t)
	openTab(t, a, "Clients")
	v := view(a)
	for _, want := range []string{"pixel-8", "-52 dBm", "printer"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	cs := scr(a, "Clients").(*clientsScreen)
	if cs.list[0].Name != "printer" && cs.list[0].Name != "pixel-8" {
		t.Fatalf("online clients first: %+v", cs.list[0])
	}
	for i, c := range cs.list {
		if c.Name == "laptop" {
			cs.cur = i
		}
	}
	press(t, a, "s") // make static
	got := a.env.Staged.Items()
	if len(got) != 4 || got[0].Section != "lease_aabbcc000002" {
		t.Fatalf("%+v", got)
	}
	// name via the form
	a.env.Staged.Clear()
	press(t, a, "n")
	if !cs.Capturing() {
		t.Fatal("rename form did not open")
	}
}

func TestClientsReadOnlyRefusesChanges(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Clients")
	press(t, a, "b", "s", "n", "w")
	if a.env.Staged.Len() != 0 || scr(a, "Clients").Capturing() {
		t.Fatal("read-only session changed something")
	}
}

func TestKickRunsOnlyForWifiAndValidates(t *testing.T) {
	a := writeApp(t)
	withStore(t)
	if err := a.env.S.Kick(a.env.ctx, "phy0-ap0;reboot", "aa:bb:cc:00:00:01"); err == nil {
		t.Fatal("injection accepted")
	}
	if err := a.env.S.Wake(a.env.ctx, "br-lan", "nope"); err == nil {
		t.Fatal("bad MAC accepted")
	}
	if err := a.env.S.Kick(a.env.ctx, "phy0-ap0", "aa:bb:cc:00:00:01"); err != nil {
		t.Fatal(err)
	}
}
