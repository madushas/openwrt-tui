package netedit

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

const base = `package network

config interface 'lan'
	option device 'br-lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
	option netmask '255.255.255.0'

config interface 'iot'
	option proto 'static'
	option ipaddr '192.168.60.1/24'

config route
	option interface 'iot'
	option target '10.9.0.0/16'
	option gateway '192.168.60.254'

package dhcp

config dhcp 'lan'
	option interface 'lan'

config dhcp
	option interface 'iot'

package firewall

config zone
	option name 'lan'
	list network 'lan'

config zone
	option name 'iot'
	list network 'iot'

package wireless

config wifi-iface 'iot_ap'
	option network 'iot'
	option ssid 'IoT'
`

func cfgs(t *testing.T) []uci.Config {
	c, err := uci.ParseExport(base)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func valid(t *testing.T, ch []changes.Change) {
	t.Helper()
	seen := map[string]bool{}
	for _, c := range ch {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", c.Describe(), err)
		}
		k := c.Config + "." + c.Section
		if c.Op == changes.OpAddSect {
			seen[k] = true
		}
	}
	_ = seen
}

func TestNewStaticWithZoneAndDHCP(t *testing.T) {
	ch, err := NewInterface(Interface{Name: "cam", Proto: "static", Device: "br-cam", Address: "192.168.40.1/24", Zone: "iot", DHCP: true, DNS: []string{"1.1.1.1"}}, cfgs(t))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, ch)
	var zone, pool bool
	for _, c := range ch {
		if c.Config == "firewall" && c.Op == changes.OpAddList && c.Value == "cam" && c.Section == "@zone[1]" {
			zone = true
		}
		if c.Config == "dhcp" && c.Op == changes.OpAddSect && c.Section == "cam" {
			pool = true
		}
	}
	if !zone || !pool {
		t.Fatalf("zone=%v pool=%v\n%v", zone, pool, ch)
	}
}

func TestNewInterfaceRefusals(t *testing.T) {
	c := cfgs(t)
	good := Interface{Name: "x1", Proto: "static", Address: "192.168.50.1/24"}
	cases := map[string]func(*Interface){
		"name":     func(i *Interface) { i.Name = "Bad Name" },
		"loopback": func(i *Interface) { i.Name = "loopback" },
		"exists":   func(i *Interface) { i.Name = "lan" },
		"proto":    func(i *Interface) { i.Proto = "l2tp" },
		"overlap":  func(i *Interface) { i.Address = "192.168.1.5/24" },
		"overlap2": func(i *Interface) { i.Address = "192.168.60.9/25" },
		"cidr":     func(i *Interface) { i.Address = "192.168.50.1" },
		"tiny":     func(i *Interface) { i.Address = "192.168.50.1/31" },
		"gateway":  func(i *Interface) { i.Gateway = "10.0.0.1" },
		"dns":      func(i *Interface) { i.DNS = []string{"nope"} },
		"device":   func(i *Interface) { i.Device = "br lan;x" },
		"zone":     func(i *Interface) { i.Zone = "ghost" },
		"dhcp":     func(i *Interface) { i.Proto = "dhcp"; i.Address = ""; i.DHCP = true },
		"pppoe":    func(i *Interface) { i.Proto = "pppoe"; i.Address = "" },
		"pppoe-nl": func(i *Interface) { i.Proto = "pppoe"; i.Address = ""; i.Username = "u"; i.Password = "p\nq" },
	}
	for name, mod := range cases {
		i := good
		mod(&i)
		if _, err := NewInterface(i, c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := NewInterface(good, c); err != nil {
		t.Fatalf("the good one was refused: %v", err)
	}
	if ch, err := NewInterface(Interface{Name: "wan2", Proto: "pppoe", Device: "wan2", Username: "u", Password: "secret"}, c); err != nil || len(ch) != 5 {
		t.Fatalf("%v %v", err, ch)
	}
}

func TestDeleteInterfaceCleansUpReferences(t *testing.T) {
	ch, warn, err := DeleteInterface("iot", cfgs(t))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, ch)
	got := map[string]bool{}
	for _, c := range ch {
		got[c.Config+"/"+string(c.Op)+"/"+c.Section] = true
	}
	for _, want := range []string{"firewall/del_list/@zone[1]", "dhcp/delete/@dhcp[1]", "network/delete/@route[0]", "network/delete/iot"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if got["dhcp/delete/lan"] {
		t.Error("the other pool was deleted")
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "IoT") {
		t.Fatalf("expected a Wi-Fi warning: %v", warn)
	}
	// the interface section is deleted last so earlier references still resolve
	if last := ch[len(ch)-1]; last.Section != "iot" || last.Op != changes.OpDelete {
		t.Fatalf("%+v", last)
	}
	if _, _, err := DeleteInterface("loopback", cfgs(t)); err == nil {
		t.Fatal("loopback deleted")
	}
	if _, _, err := DeleteInterface("nope", cfgs(t)); err == nil {
		t.Fatal("missing interface deleted")
	}
}
