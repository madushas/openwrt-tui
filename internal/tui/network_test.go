package tui

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

func netOf(t *testing.T, a *App) *netScreen { return scr(a, "Network").(*netScreen) }

func selectIf(t *testing.T, n *netScreen, name string) {
	t.Helper()
	for i, r := range n.rows() {
		if r.Type == "interface" && r.Name == name {
			n.cur = i
			return
		}
	}
	t.Fatalf("no interface %q", name)
}

func TestNetworkListsInterfaces(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Network")
	v := plain(a.render())
	for _, want := range []string{"lan", "static", "192.168.1.1/24", "wg0", "wireguard"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
}

func TestNetworkReadOnlyAndUnsupportedProtocols(t *testing.T) {
	a := newApp(t, true)
	press(t, a, tab(a, "Network"), "enter")
	if netOf(t, a).form != nil {
		t.Fatal("read-only mode opened a form")
	}
	b := newApp(t, false)
	press(t, b, tab(b, "Network"))
	n := netOf(t, b)
	selectIf(t, n, "wg0")
	press(t, b, "enter")
	if n.form != nil {
		t.Fatal("a WireGuard interface opened the static form")
	}
}

func TestNetworkStaticEditStagesOnlyChanges(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "Network")
	n := netOf(t, a)
	selectIf(t, n, "lan")
	press(t, a, "enter")
	if n.form == nil {
		t.Fatal("form did not open")
	}
	n.form.fields[0].Value = "192.168.2.1"
	n.form.fields[1].Value = "255.255.255.128"
	n.form.fields[3].Value = "1.1.1.1 9.9.9.9"
	press(t, a, "s")
	var got []string
	for _, c := range a.env.Staged.Items() {
		got = append(got, string(c.Op)+":"+c.Option+"="+c.Value)
	}
	want := "set:ipaddr=192.168.2.1 set:netmask=255.255.255.128 add_list:dns=1.1.1.1 add_list:dns=9.9.9.9"
	if strings.Join(got, " ") != want {
		t.Fatalf("staged %v", got)
	}
}

func TestNetworkRejectsBadValues(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "Network")
	n := netOf(t, a)
	selectIf(t, n, "lan")
	press(t, a, "enter")
	n.form.fields[0].Value = "192.168.1.300"
	n.form.fields[1].Value = "255.0.255.0"
	n.form.fields[3].Value = "1.1.1.1 nope"
	press(t, a, "s")
	if n.form == nil || a.env.Staged.Len() != 0 {
		t.Fatal("bad values were staged")
	}
	for _, i := range []int{0, 1, 3} {
		if n.form.fields[i].err == "" {
			t.Errorf("field %d accepted a bad value", i)
		}
	}
}

// The user's real router: CIDR addresses, an existing DNS list and a DHCP uplink.
const realNetwork = `package network

config interface 'lan'
	option device 'br-lan'
	option proto 'static'
	option ipaddr '192.168.10.1/24'

config interface 'eduvpn'
	option proto 'static'
	option ipaddr '10.0.0.2'
	option netmask '255.255.255.0'
	list dns '8.8.8.8'
	list dns '8.8.4.4'

config interface 'wan'
	option device 'wan'
	option proto 'dhcp'
`

func TestNetworkRealShapes(t *testing.T) {
	cfgs, err := uci.ParseExport(realNetwork)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(t, false)
	openTab(t, a, "Network")
	n := netOf(t, a)
	n.cfg = cfgs[0]
	selectIf(t, n, "lan")
	press(t, a, "enter")
	if len(n.form.fields) != 4 || n.form.fields[0].Validate("192.168.10.1/24") != nil {
		t.Fatalf("CIDR form wrong: %d fields", len(n.form.fields))
	}
	n.form = nil
	selectIf(t, n, "eduvpn")
	press(t, a, "enter")
	n.form.fields[3].Value = "9.9.9.9" // replaces a two-entry list
	press(t, a, "s")
	items := a.env.Staged.Items()
	if len(items) != 2 || items[0].Op != changes.OpDelete || items[0].Option != "dns" || items[1].Op != changes.OpAddList || items[1].Value != "9.9.9.9" {
		t.Fatalf("list not rewritten: %+v", items)
	}
	selectIf(t, n, "wan")
	press(t, a, "enter")
	if n.form == nil || !strings.Contains(n.form.Title, "DHCP") {
		t.Fatal("DHCP form did not open")
	}
}

func TestMaskBits(t *testing.T) {
	for in, want := range map[string]string{"255.255.255.0": "24", "255.255.255.128": "25", "255.0.0.0": "8", "0.255.0.0": "0.255.0.0"} {
		if got := maskBits(in); got != want {
			t.Errorf("maskBits(%s) = %s, want %s", in, got, want)
		}
	}
}
