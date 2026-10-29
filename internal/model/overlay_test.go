package model

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

const sample = `package dhcp

config dhcp 'lan'
	option start '100'
	list ra_flags 'a'

config host
	option name 'one'
	option ip '10.0.0.1'

config host
	option name 'two'
	option ip '10.0.0.2'

config host
	option name 'three'
	option ip '10.0.0.3'
`

func load(t *testing.T) []uci.Config {
	c, err := uci.ParseExport(sample)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func name(c uci.Config, ref string) string {
	s, ok := c.Section(ref)
	if !ok {
		return "<none>"
	}
	o, _ := s.Option("name")
	return o.Value()
}

func TestOverlayEditsAndLeavesInputAlone(t *testing.T) {
	in := load(t)
	var set changes.Set
	set.Add(changes.Change{Op: changes.OpSet, Config: "dhcp", Section: "lan", Option: "start", Value: "50", Old: "100"})
	set.Add(changes.Change{Op: changes.OpAddList, Config: "dhcp", Section: "lan", Option: "ra_flags", Value: "b"})
	set.Add(changes.Change{Op: changes.OpDelete, Config: "dhcp", Section: "@host[0]", Option: "ip"})
	out := Overlay(in, &set)
	lan, _ := out[0].Section("lan")
	if o, _ := lan.Option("start"); o.Value() != "50" {
		t.Fatalf("start %q", o.Value())
	}
	if o, _ := lan.Option("ra_flags"); len(o.Values) != 2 {
		t.Fatalf("list %v", o.Values)
	}
	h, _ := out[0].Section("@host[0]")
	if _, ok := h.Option("ip"); ok {
		t.Fatal("option not deleted")
	}
	// the input must be untouched
	l0, _ := in[0].Section("lan")
	if o, _ := l0.Option("start"); o.Value() != "100" {
		t.Fatal("Overlay modified its input")
	}
}

func TestOverlayAnonymousDeletesMatchTheScriptOrder(t *testing.T) {
	var set changes.Set
	for _, ref := range []string{"@host[0]", "@host[2]"} {
		set.Add(changes.Change{Op: changes.OpDelete, Config: "dhcp", Section: ref})
	}
	out := Overlay(load(t), &set)
	if n := name(out[0], "@host[0]"); n != "two" {
		t.Fatalf("first remaining host is %q, want two", n)
	}
	if n := name(out[0], "@host[1]"); n != "<none>" {
		t.Fatalf("a second host survived: %q", n)
	}
}

func TestOverlayAddsNamedSection(t *testing.T) {
	var set changes.Set
	set.Add(changes.Change{Op: changes.OpAddSect, Config: "dhcp", Section: "lease_x", Type: "host"})
	set.Add(changes.Change{Op: changes.OpSet, Config: "dhcp", Section: "lease_x", Option: "ip", Value: "10.0.0.9"})
	out := Overlay(load(t), &set)
	s, ok := out[0].Section("lease_x")
	if !ok || s.Type != "host" {
		t.Fatal("section missing")
	}
	if o, _ := s.Option("ip"); o.Value() != "10.0.0.9" {
		t.Fatal("option missing")
	}
	// a change to a missing section is ignored rather than invented
	var bad changes.Set
	bad.Add(changes.Change{Op: changes.OpSet, Config: "dhcp", Section: "nope", Option: "a", Value: "b"})
	if got := Overlay(load(t), &bad); len(got[0].Sections) != 4 {
		t.Fatal("invented a section")
	}
}

func TestDiffThenOverlayReproducesTheSnapshot(t *testing.T) {
	cur := load(t)
	want, err := uci.ParseExport(`package dhcp

config dhcp 'lan'
	option start '120'
	list ra_flags 'x'
	list ra_flags 'y'
	option leasetime '6h'

config host
	option name 'two'
	option ip '10.0.0.22'

config host
	option name 'three'
	option ip '10.0.0.3'

config host
	option name 'four'
	option ip '10.0.0.4'
`)
	if err != nil {
		t.Fatal(err)
	}
	var set changes.Set
	for _, c := range Diff(cur, want) {
		if err := set.Add(c); err != nil {
			t.Fatalf("%v: %+v", err, c)
		}
	}
	got := Overlay(cur, &set)
	if Render(got[0], false) != Render(want[0], false) {
		t.Fatalf("restore does not reproduce the snapshot:\n--- got\n%s\n--- want\n%s", Render(got[0], false), Render(want[0], false))
	}
}

func TestDiffOfIdenticalConfigsIsEmpty(t *testing.T) {
	if d := Diff(load(t), load(t)); len(d) != 0 {
		t.Fatalf("%+v", d)
	}
}

func TestRenderMasksSecretsAndQuotes(t *testing.T) {
	c, _ := uci.ParseExport("package wireless\n\nconfig wifi-iface 'a'\n\toption ssid 'it'\\''s'\n\toption key 'hunter2'\n")
	masked, plain := Render(c[0], true), Render(c[0], false)
	if strings.Contains(masked, "hunter2") || !strings.Contains(masked, "'********'") {
		t.Fatalf("masked render leaks:\n%s", masked)
	}
	if !strings.Contains(plain, "hunter2") || !strings.Contains(plain, `'it'\''s'`) {
		t.Fatalf("plain render wrong:\n%s", plain)
	}
	again, _ := uci.ParseExport(plain)
	if Render(again[0], false) != plain {
		t.Fatal("Render output does not round-trip through ParseExport")
	}
}

func TestChanged(t *testing.T) {
	a := Hashes(load(t))
	var set changes.Set
	set.Add(changes.Change{Op: changes.OpSet, Config: "dhcp", Section: "lan", Option: "start", Value: "9"})
	b := Hashes(Overlay(load(t), &set))
	if got := Changed(a, b); len(got) != 1 || got[0] != "dhcp" {
		t.Fatalf("%v", got)
	}
	if got := Changed(a, a); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := Changed(map[string]string{"gone": "x"}, a); len(got) != 2 {
		t.Fatalf("%v", got)
	}
}
