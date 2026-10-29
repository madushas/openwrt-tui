package tui

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

func dhcpOf(t *testing.T, a *App) *dhcpScreen {
	t.Helper()
	for _, s := range a.screens {
		if d, ok := s.(*dhcpScreen); ok {
			return d
		}
	}
	t.Fatal("no DHCP screen")
	return nil
}

func typeText(t *testing.T, a *App, s string) {
	t.Helper()
	for _, r := range s {
		press(t, a, string(r))
	}
}

func TestDHCPListsPoolsAndStaticLeases(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "DHCP")
	v := plain(a.render())
	for _, want := range []string{"POOL", "lan", "STATIC", "printer", "aa:bb:cc:00:00:04", "ACTIVE", "macbook-pro"} {
		if !strings.Contains(v, want) {
			t.Fatalf("DHCP screen missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "aa:bb:cc:00:00:04  192.168.1.60  ACTIVE") {
		t.Fatal("a reserved device is also listed as an active lease")
	}
}

func TestDHCPReadOnlyStagesNothing(t *testing.T) {
	a := newApp(t, true)
	press(t, a, tab(a, "DHCP"), "n")
	if dhcpOf(t, a).form != nil || a.env.Staged.Len() != 0 {
		t.Fatal("read-only mode opened a form")
	}
}

func selectRow(t *testing.T, d *dhcpScreen, kind dhcpKind, contains string) {
	t.Helper()
	for i, r := range d.rows() {
		if r.kind == kind && (contains == "" || strings.Contains(r.get("name")+r.get("interface"), contains)) {
			d.cur = i
			return
		}
	}
	t.Fatalf("no row of kind %d containing %q", kind, contains)
}

func TestMakeActiveLeaseStatic(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "DHCP")
	d := dhcpOf(t, a)
	selectRow(t, d, rowActive, "pixel-8")
	press(t, a, "s")
	if d.form == nil {
		t.Fatal("form did not open")
	}
	// name, mac and ip come from the lease
	if d.form.fields[0].Value != "pixel-8" || d.form.fields[2].Value != "192.168.1.51" {
		t.Fatalf("form not prefilled: %+v %+v", d.form.fields[0], d.form.fields[2])
	}
	press(t, a, "s")
	if d.form != nil || a.env.Staged.Len() != 4 {
		t.Fatalf("form=%v staged=%d, want closed and 4 changes (section + 3 options)", d.form != nil, a.env.Staged.Len())
	}
	items := a.env.Staged.Items()
	if items[0].Op != changes.OpAddSect || items[0].Section != "lease_aabbcc000002" || items[0].Type != "host" {
		t.Fatalf("unexpected first change: %+v", items[0])
	}
	script, err := a.env.Staged.Script(changes.NewPlan(changes.CommitOnly, 0))
	if err != nil || !strings.Contains(script, "dhcp.lease_aabbcc000002.ip=192.168.1.51") {
		t.Fatalf("script wrong (%v):\n%s", err, script)
	}
	// it now shows as staged and the live row is gone
	v := plain(a.render())
	if !strings.Contains(v, "staged") {
		t.Fatalf("staged reservation not shown:\n%s", v)
	}
}

func TestStaticLeaseRejectsBadAndDuplicateInput(t *testing.T) {
	a := newApp(t, false)
	press(t, a, tab(a, "DHCP"), "n")
	d := dhcpOf(t, a)
	f := d.form
	set := func(i int, v string) { f.fields[i].Value = v }
	set(0, "nas")
	set(1, "not-a-mac")
	set(2, "192.168.1.999")
	press(t, a, "s")
	if d.form == nil || a.env.Staged.Len() != 0 {
		t.Fatal("invalid form was accepted")
	}
	if f.fields[1].err == "" || f.fields[2].err == "" {
		t.Fatalf("errors not shown: %q %q", f.fields[1].err, f.fields[2].err)
	}
	set(1, "AA:BB:CC:00:00:04") // the printer's reservation, different case
	set(2, "192.168.1.60")
	press(t, a, "s")
	if d.form == nil || !strings.Contains(f.fields[1].err, "already") || !strings.Contains(f.fields[2].err, "already") {
		t.Fatalf("duplicates accepted: %q %q", f.fields[1].err, f.fields[2].err)
	}
	set(1, "AA:BB:CC:00:00:77")
	set(2, "192.168.1.77")
	press(t, a, "s")
	if d.form != nil {
		t.Fatal("valid form was rejected")
	}
	if got := a.env.Staged.Items()[1].Value; got != "nas" && a.env.Staged.Items()[2].Value != "aa:bb:cc:00:00:77" {
		t.Fatalf("mac not lower-cased: %+v", a.env.Staged.Items())
	}
}

func TestEditPoolStagesOnlyChangedOptions(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "DHCP")
	d := dhcpOf(t, a)
	selectRow(t, d, rowPool, "lan")
	press(t, a, "enter")
	if d.form == nil || !d.Capturing() {
		t.Fatal("pool form did not open")
	}
	press(t, a, "down", "down", "enter") // lease time
	for i := 0; i < 3; i++ {
		press(t, a, "backspace")
	}
	typeText(t, a, "24h")
	press(t, a, "enter", "s")
	items := a.env.Staged.Items()
	if len(items) != 1 || items[0].Option != "leasetime" || items[0].Value != "24h" || items[0].Old != "12h" {
		t.Fatalf("staged %+v", items)
	}
}

func TestPoolRejectsBadLeaseTime(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "DHCP")
	d := dhcpOf(t, a)
	selectRow(t, d, rowPool, "lan")
	press(t, a, "enter", "down", "down", "enter")
	for i := 0; i < 3; i++ {
		press(t, a, "backspace")
	}
	typeText(t, a, "soon")
	press(t, a, "enter", "s")
	if d.form == nil || a.env.Staged.Len() != 0 {
		t.Fatal("bad lease time was staged")
	}
}

func TestDeleteStaticLease(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "DHCP")
	d := dhcpOf(t, a)
	selectRow(t, d, rowStatic, "printer")
	press(t, a, "d")
	items := a.env.Staged.Items()
	if len(items) != 1 || items[0].Op != changes.OpDelete || items[0].Section != "@host[0]" {
		t.Fatalf("staged %+v", items)
	}
	// deleting a pool or an active lease must not stage anything
	a.env.Staged.Clear()
	selectRow(t, d, rowPool, "lan")
	press(t, a, "d")
	if a.env.Staged.Len() != 0 {
		t.Fatal("deleted a pool")
	}
}

func TestHostileHostnameBecomesSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"phone-a":          "phone-a",
		"Giresha's A21s":   "Gireshas-A21s",
		"x'; reboot; '":    "x-reboot",
		"*":                "",
		"a.b_c d":          "a-b-c-d",
		"../../etc/passwd": "etcpasswd",
	} {
		if got := leaseName(in); got != want {
			t.Errorf("leaseName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The user's real router stores reservations as "list mac" and has hosts with
// no name; editing must keep the list a list and must not choke on either.
const realDHCP = `package dhcp

config dhcp 'lan'
	option interface 'lan'
	option start '100'
	option limit '150'
	option leasetime '12h'

config host
	option ip '192.168.10.212'
	list mac '64:82:14:47:41:0F'

config host
	option name 'Z2473'
	option ip '192.168.10.235'
	list mac '5E:18:91:07:E1:D2'
`

func TestRealRouterListMacReservations(t *testing.T) {
	cfgs, err := uci.ParseExport(realDHCP)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(t, false)
	openTab(t, a, "DHCP")
	d := dhcpOf(t, a)
	d.cfg, d.leases = cfgs[0], nil
	if v := plain(a.render()); !strings.Contains(v, "(no name)") || !strings.Contains(v, "64:82:14:47:41:0F") {
		t.Fatalf("nameless list-mac host not shown:\n%s", v)
	}
	selectRow(t, d, rowStatic, "Z2473")
	press(t, a, "enter")
	// moving to the second field and changing the MAC (case-only changes of the same device are fine)
	d.form.fields[1].Value = "5E:18:91:07:E1:D3 64:82:14:47:41:0f"
	press(t, a, "s")
	if d.form == nil || !strings.Contains(d.form.fields[1].err, "already") {
		t.Fatalf("a MAC owned by another host was accepted: %+v", d.form)
	}
	d.form.fields[1].Value = "5e:18:91:07:e1:d3 5e:18:91:07:e1:d4"
	press(t, a, "s")
	if d.form != nil {
		t.Fatalf("valid multi-MAC edit rejected")
	}
	items := a.env.Staged.Items()
	if len(items) != 3 || items[0].Op != changes.OpDelete || items[0].Option != "mac" ||
		items[1].Op != changes.OpAddList || items[2].Op != changes.OpAddList || items[2].Value != "5e:18:91:07:e1:d4" {
		t.Fatalf("list not preserved: %+v", items)
	}
	script, err := a.env.Staged.Script(changes.NewPlan(changes.CommitOnly, 0))
	if err != nil || !strings.Contains(script, "uci add_list 'dhcp.@host[1].mac=5e:18:91:07:e1:d3'") {
		t.Fatalf("script (%v):\n%s", err, script)
	}
}
