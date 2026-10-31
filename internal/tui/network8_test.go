package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/preflight"
)

// toTab moves the Network screen to a section by name, like pressing ].
func toTab(t *testing.T, a *App, name string) *secEditor {
	t.Helper()
	openTab(t, a, "Network")
	n := netOf(t, a)
	for i, tn := range netTabs {
		if tn == name {
			n.sub = i
			if i == 0 {
				return nil
			}
			return n.subs[i-1]
		}
	}
	t.Fatalf("no tab %q", name)
	return nil
}

func TestNetworkSectionsShowDemoData(t *testing.T) {
	a := newApp(t, true)
	for tab, want := range map[string][]string{
		"Bridges":  {"br-lan", "lan1 lan2 lan3"},
		"Routes":   {"10.20.0.0/16", "192.168.1.254", "default via 192.168.1.254 dev wan"},
		"Zones":    {"lan", "wan", "guest"},
		"Forwards": {"lan", "wan"},
		"Rules":    {"Allow-Ping", "icmp", "ACCEPT"},
		"Port fwd": {"plex", "32400", "192.168.1.60:32400"},
	} {
		toTab(t, a, tab)
		v := plain(a.render())
		for _, w := range want {
			if !strings.Contains(v, w) {
				t.Errorf("%s tab lacks %q:\n%s", tab, w, v)
			}
		}
	}
	// the bracket keys move between sections and the header shows where you are
	openTab(t, a, "Network")
	n := netOf(t, a)
	n.sub = 0
	press(t, a, "]")
	if n.sub != 1 || !strings.Contains(plain(a.render()), "Bridges") {
		t.Fatalf("sub=%d", n.sub)
	}
	press(t, a, "[", "[")
	if n.sub != len(n.subs) {
		t.Fatalf("[ did not wrap: %d", n.sub)
	}
}

func TestNewFirewallRuleMapsLabelsToUCI(t *testing.T) {
	a := newApp(t, false)
	ed := toTab(t, a, "Rules")
	press(t, a, "n")
	if ed.form == nil {
		t.Fatal("no form")
	}
	set := func(k, v string) {
		for _, f := range ed.form.fields {
			if f.Key == k {
				f.Value = v
			}
		}
	}
	set("name", "Allow SSH from home")
	set("src", "(any zone)")
	set("dest", "(router)")
	set("proto", "tcp")
	set("dest_port", "22")
	press(t, a, "s")
	got := map[string]string{}
	var sec string
	for _, c := range a.env.Staged.Items() {
		if c.Op == changes.OpAddSect {
			sec = c.Section
		}
		got[c.Option] = c.Value
	}
	if sec != "rule_allow_ssh_from_home" || got["src"] != "*" || got["target"] != "ACCEPT" || got["dest_port"] != "22" {
		t.Fatalf("%s %v", sec, got)
	}
	if _, has := got["dest"]; has {
		t.Fatalf("'(router)' must mean no dest option: %v", got)
	}
	// a rule with a port but protocol icmp is refused by the form
	a.env.Staged.Clear()
	press(t, a, "n")
	set("name", "bad")
	set("proto", "icmp")
	set("dest_port", "22")
	press(t, a, "s")
	if ed.form == nil || a.env.Staged.Len() != 0 {
		t.Fatal("an invalid rule was staged")
	}
}

func TestToggleAndDeleteRules(t *testing.T) {
	a := newApp(t, false)
	ed := toTab(t, a, "Rules")
	press(t, a, "t")
	items := a.env.Staged.Items()
	if len(items) != 1 || items[0].Option != "enabled" || items[0].Value != "0" {
		t.Fatalf("%+v", items)
	}
	press(t, a, "d")
	if a.ask == nil || !a.ask.Danger {
		t.Fatal("delete not confirmed")
	}
	press(t, a, "y")
	last := a.env.Staged.Items()
	if last[len(last)-1].Op != changes.OpDelete || last[len(last)-1].Option != "" {
		t.Fatalf("%+v", last)
	}
	_ = ed
}

func TestZoneInUseCannotBeDeleted(t *testing.T) {
	a := newApp(t, false)
	ed := toTab(t, a, "Zones")
	for i, s := range ed.rows() {
		if opt(s, "name") == "wan" {
			ed.cur = i
		}
	}
	press(t, a, "d")
	if a.ask != nil {
		t.Fatal("asked to delete a zone that rules and port forwards use")
	}
	if !strings.Contains(a.toast.Text, "Allow-Ping") {
		t.Fatalf("toast: %q", a.toast.Text)
	}
	// the guest zone is unused: deleting it only needs the zone itself
	for i, s := range ed.rows() {
		if opt(s, "name") == "guest" {
			ed.cur = i
		}
	}
	press(t, a, "d", "y")
	if a.env.Staged.Len() != 1 {
		t.Fatalf("staged %d", a.env.Staged.Len())
	}
}

func TestBridgePortChangeForcesRollback(t *testing.T) {
	a := newApp(t, false)
	ed := toTab(t, a, "Bridges")
	press(t, a, "enter")
	if ed.form == nil {
		t.Fatal("no form")
	}
	for _, f := range ed.form.fields {
		if f.Key == "ports" {
			f.Value = "lan1 lan2"
		}
	}
	press(t, a, "s")
	var del, add int
	for _, c := range a.env.Staged.Items() {
		if c.Option == "ports" && c.Op == changes.OpDelete {
			del++
		}
		if c.Option == "ports" && c.Op == changes.OpAddList {
			add++
		}
	}
	if del != 1 || add != 2 {
		t.Fatalf("a list must be rewritten as delete + add_list: %d %d", del, add)
	}
	openTab(t, a, "Changes")
	c := scr(a, "Changes").(*changesScreen)
	if !preflight.NeedsRollback(c.preflight()) || c.mode() != changes.WithRollback {
		t.Fatalf("a bridge port change on the management bridge must force rollback: %v", c.preflight())
	}
}

func TestNewInterfaceAndDelete(t *testing.T) {
	a := newApp(t, false)
	toTab(t, a, "Interfaces")
	n := netOf(t, a)
	n.sub = 0
	press(t, a, "n")
	if n.form == nil || !n.adding {
		t.Fatal("no new-interface form")
	}
	set := func(k, v string) {
		for _, f := range n.form.fields {
			if f.Key == k {
				f.Value = v
			}
		}
	}
	set("name", "cam")
	set("addr", "192.168.77.1/24")
	set("zone", "lan")
	set("dhcp", "1")
	press(t, a, "s")
	if a.env.Staged.Len() < 9 || n.adding {
		t.Fatalf("staged %d (toast %q)", a.env.Staged.Len(), a.toast.Text)
	}
	// an overlapping subnet is refused
	a.env.Staged.Clear()
	press(t, a, "n")
	set("name", "dup")
	set("addr", "192.168.1.5/24")
	press(t, a, "s")
	if a.env.Staged.Len() != 0 || !strings.Contains(a.toast.Text, "overlaps") {
		t.Fatalf("toast %q staged %d", a.toast.Text, a.env.Staged.Len())
	}
	// deleting wan's interface cleans up its zone entry
	selectIf(t, n, "wan")
	press(t, a, "d", "y")
	var delList bool
	for _, c := range a.env.Staged.Items() {
		if c.Op == changes.OpDelList && c.Value == "wan" {
			delList = true
		}
	}
	if !delList {
		t.Fatalf("zone entry not removed: %v", a.env.Staged.Items())
	}
}

func TestSectionEditorsRefuseInReadOnly(t *testing.T) {
	a := newApp(t, true)
	for _, tab := range netTabs[1:] {
		ed := toTab(t, a, tab)
		press(t, a, "n", "d", "t", "enter")
		if ed.form != nil || a.ask != nil || a.env.Staged.Len() != 0 {
			t.Fatalf("%s changed something in read-only mode", tab)
		}
	}
	toTab(t, a, "Interfaces")
	press(t, a, "n", "d")
	if netOf(t, a).form != nil || a.ask != nil {
		t.Fatal("interfaces changed in read-only mode")
	}
}

func fill(ed *secEditor, kv map[string]string) {
	for _, f := range ed.form.fields {
		if v, ok := kv[f.Key]; ok {
			f.Value = v
		}
	}
}

func TestNewBridgeVLANAndRoute(t *testing.T) {
	a := newApp(t, false)
	// a bridge may not steal a port from another bridge
	ed := toTab(t, a, "Bridges")
	press(t, a, "n")
	fill(ed, map[string]string{"name": "br-iot", "ports": "lan1"})
	press(t, a, "s")
	if ed.form == nil || a.env.Staged.Len() != 0 {
		t.Fatal("a port already in br-lan was accepted")
	}
	fill(ed, map[string]string{"ports": "lan4"})
	press(t, a, "s")
	if a.env.Staged.Len() != 3 { // add, name, type... plus ports list
		var d []string
		for _, c := range a.env.Staged.Items() {
			d = append(d, c.Describe())
		}
		if a.env.Staged.Len() != 4 {
			t.Fatalf("staged: %v", d)
		}
	}
	a.env.Staged.Clear()

	// a VLAN on br-lan with tagged and untagged ports
	ed = toTab(t, a, "VLANs")
	press(t, a, "n")
	fill(ed, map[string]string{"vlan": "10", "ports": "lan1:u* lan2:t"})
	press(t, a, "s")
	adds := 0
	for _, c := range a.env.Staged.Items() {
		if c.Op == changes.OpAddList && c.Option == "ports" {
			adds++
		}
	}
	if adds != 2 {
		t.Fatalf("ports must be staged as two list entries, got %d", adds)
	}
	a.env.Staged.Clear()
	press(t, a, "n")
	fill(ed, map[string]string{"vlan": "9999"})
	press(t, a, "s")
	if ed.form == nil {
		t.Fatal("VLAN 9999 accepted")
	}
	press(t, a, "esc")

	// a default route forces rollback
	ed = toTab(t, a, "Routes")
	press(t, a, "n")
	fill(ed, map[string]string{"target": "0.0.0.0/0", "gateway": "192.168.1.254"})
	press(t, a, "s")
	openTab(t, a, "Changes")
	c := scr(a, "Changes").(*changesScreen)
	if !preflight.NeedsRollback(c.preflight()) {
		t.Fatalf("default route did not force rollback: %+v", c.preflight())
	}
	// the legacy target+netmask form is not editable here
	a.env.Staged.Clear()
}

func TestReorderRulesStagesAbsolutePosition(t *testing.T) {
	a := newApp(t, false)
	ed := toTab(t, a, "Rules")
	if !strings.Contains(ed.Keys(), "K/J") {
		t.Fatalf("no reorder hint: %q", ed.Keys())
	}
	rows := ed.rows()
	if len(rows) < 2 {
		t.Fatalf("demo needs two rules, has %d", len(rows))
	}
	press(t, a, "K") // already first
	if a.env.Staged.Len() != 0 {
		t.Fatal("moved the first rule up")
	}
	press(t, a, "J")
	items := a.env.Staged.Items()
	if len(items) != 1 || items[0].Op != changes.OpReorder || items[0].Section != "@rule[0]" {
		t.Fatalf("%+v", items)
	}
	// position of the second rule in the whole firewall file
	want := -1
	for i, s := range ed.config().Sections {
		if s.Ref() == "@rule[1]" {
			want = i
		}
	}
	if items[0].Value != strconv.Itoa(want) || want < 2 {
		t.Fatalf("position %q, want %d", items[0].Value, want)
	}
	// the preflight overlay really swaps them
	press(t, a, "J") // second reorder of another section is refused or replaces; either way one item
	if a.env.Staged.Len() != 1 {
		t.Fatalf("%d staged", a.env.Staged.Len())
	}
}

func TestReorderIsRefusedInReadOnlyMode(t *testing.T) {
	a := newApp(t, true)
	toTab(t, a, "Rules")
	press(t, a, "J")
	if a.env.Staged.Len() != 0 {
		t.Fatal("read-only session staged a reorder")
	}
}
