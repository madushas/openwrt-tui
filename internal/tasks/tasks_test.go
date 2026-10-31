package tasks

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

const base = `package network

config interface 'lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
	option netmask '255.255.255.0'

package wireless

config wifi-device 'radio0'
	option band '2g'

package dhcp

config dnsmasq
	list server '8.8.8.8'

package firewall

config zone
	option name 'wan'
	list network 'wan'
`

func cfgs(t *testing.T, s string) []uci.Config {
	c, err := uci.ParseExport(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func goodGuest() Guest {
	return Guest{Name: "guest", SSID: "Free WiFi", Key: "correct horse", Octet: 3, Radio: "radio0", Isolate: true}
}

func validateAll(t *testing.T, ch []changes.Change) {
	t.Helper()
	for _, c := range ch {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", c.Describe(), err)
		}
	}
}

func TestGuestNetwork(t *testing.T) {
	ch, err := GuestNetwork(goodGuest(), cfgs(t, base))
	if err != nil {
		t.Fatal(err)
	}
	validateAll(t, ch)
	var zoneInput, fwdDest, isolate, key string
	for _, c := range ch {
		switch {
		case c.Config == "firewall" && c.Section == "guest" && c.Option == "input":
			zoneInput = c.Value
		case c.Section == "guest_wan" && c.Option == "dest":
			fwdDest = c.Value
		case c.Option == "isolate":
			isolate = c.Value
		case c.Option == "key":
			key = c.Value
		case c.Config == "firewall" && c.Option == "dest" && c.Value == "lan":
			t.Fatal("guest zone may not forward to lan")
		}
	}
	if zoneInput != "REJECT" || fwdDest != "wan" || isolate != "1" || key != "correct horse" {
		t.Fatalf("%q %q %q %q", zoneInput, fwdDest, isolate, key)
	}
	// the sections must be created before options are set on them
	seen := map[string]bool{}
	for _, c := range ch {
		k := c.Config + "." + c.Section
		if c.Op == changes.OpAddSect {
			seen[k] = true
		} else if !seen[k] {
			t.Fatalf("%s used before it is created", k)
		}
	}
}

func TestGuestNetworkRefusals(t *testing.T) {
	c := cfgs(t, base)
	cases := map[string]func(*Guest){
		"name":   func(g *Guest) { g.Name = "Guest Net" },
		"ssid":   func(g *Guest) { g.SSID = "" },
		"pass":   func(g *Guest) { g.Key = "short" },
		"octet":  func(g *Guest) { g.Octet = 1 }, // taken by lan
		"radio":  func(g *Guest) { g.Radio = "radio9" },
		"octet0": func(g *Guest) { g.Octet = 0 },
		"exists": func(g *Guest) { g.Name = "lan" },
	}
	for name, mod := range cases {
		g := goodGuest()
		mod(&g)
		if _, err := GuestNetwork(g, c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := GuestNetwork(goodGuest(), cfgs(t, "package wireless\n\nconfig wifi-device 'radio0'\n")); err == nil || !strings.Contains(err.Error(), "wan") {
		t.Fatalf("no wan zone: %v", err)
	}
}

func TestPortForward(t *testing.T) {
	c := cfgs(t, base)
	ch, warn, err := PortForward(Forward{Name: "web-nas", Proto: "tcp", ExtPort: "8443", IP: "192.168.1.5"}, c)
	if err != nil || len(warn) != 0 {
		t.Fatalf("%v %v", err, warn)
	}
	validateAll(t, ch)
	if ch[0].Section != "fwd_web_nas" {
		t.Fatalf("%+v", ch[0])
	}
	var ip, ip2 string
	for _, x := range ch {
		if x.Option == "dest_port" {
			ip = x.Value
		}
		if x.Option == "dest_ip" {
			ip2 = x.Value
		}
	}
	if ip != "8443" || ip2 != "192.168.1.5" {
		t.Fatal("internal port should default to the external one")
	}
	_, warn, _ = PortForward(Forward{Name: "ssh", Proto: "tcp", ExtPort: "22", IP: "192.168.1.5"}, c)
	if len(warn) != 1 {
		t.Fatal("no warning for SSH")
	}
	for _, f := range []Forward{
		{Name: "a b", Proto: "tcp", ExtPort: "80", IP: "192.168.1.5"},
		{Name: "x", Proto: "icmp", ExtPort: "80", IP: "192.168.1.5"},
		{Name: "x", Proto: "tcp", ExtPort: "0", IP: "192.168.1.5"},
		{Name: "x", Proto: "tcp", ExtPort: "80", IP: "999.1.1.1"},
		{Name: "x", Proto: "tcp", ExtPort: "80", IP: "192.168.1.5", IntPort: "70000"},
	} {
		if _, _, err := PortForward(f, c); err == nil {
			t.Errorf("accepted %+v", f)
		}
	}
	taken := cfgs(t, base+"\nconfig redirect 'old'\n\toption src 'wan'\n\toption src_dport '8443'\n\toption proto 'tcp'\n")
	if _, _, err := PortForward(Forward{Name: "n", Proto: "tcp", ExtPort: "8443", IP: "192.168.1.5"}, taken); err == nil {
		t.Fatal("duplicate external port accepted")
	}
	if _, _, err := PortForward(Forward{Name: "n", Proto: "udp", ExtPort: "8443", IP: "192.168.1.5"}, taken); err != nil {
		t.Fatalf("same port, other protocol must be fine: %v", err)
	}
}

func TestUpstreamDNS(t *testing.T) {
	c := cfgs(t, base)
	ch, _, err := UpstreamDNS(DNSPresets[0].Servers, c)
	if err != nil {
		t.Fatal(err)
	}
	validateAll(t, ch)
	if ch[0].Op != changes.OpDelete || ch[0].Option != "server" || len(ch) != 4 {
		t.Fatalf("%+v", ch)
	}
	if _, _, err := UpstreamDNS([]string{"not-an-ip"}, c); err == nil {
		t.Fatal("bad server accepted")
	}
	if _, _, err := UpstreamDNS(nil, c); err == nil {
		t.Fatal("empty accepted")
	}
	done := cfgs(t, "package dhcp\n\nconfig dnsmasq\n\toption noresolv '1'\n\tlist server '1.1.1.1'\n\tlist server '1.0.0.1'\n")
	if _, _, err := UpstreamDNS(DNSPresets[0].Servers, done); err == nil {
		t.Fatal("no-op accepted")
	}
	_, warn, _ := UpstreamDNS(DNSPresets[0].Servers, cfgs(t, base+"\npackage adblock\n\nconfig adblock 'global'\n"))
	if len(warn) != 1 {
		t.Fatalf("%v", warn)
	}
}
