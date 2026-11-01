package clients

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/uci"
)

const sample = `##leases
1790000000 aa:bb:cc:00:00:01 192.168.1.50 phone 01:aa
1790000000 AA:BB:CC:00:00:02 192.168.1.51 * *
1790000000 aa:bb:cc:00:00:03 192.168.1.52 tv *
##neigh
192.168.1.50 dev br-lan lladdr aa:bb:cc:00:00:01 REACHABLE
192.168.1.51 dev br-lan lladdr aa:bb:cc:00:00:02 STALE
192.168.1.52 dev br-lan  FAILED
192.168.1.60 dev br-lan lladdr aa:bb:cc:00:00:04 REACHABLE
##sta phy0-ap0
Station aa:bb:cc:00:00:01 (on phy0-ap0)
	inactive time:	40 ms
	rx bytes:	123456
	tx bytes:	654321
	signal:  	-52 [-52] dBm
	connected time:	3600 seconds
`

func cfgs(t *testing.T, s string) []uci.Config {
	c, err := uci.ParseExport(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func byMAC(l []Client, mac string) Client {
	for _, c := range l {
		if c.MAC == mac {
			return c
		}
	}
	return Client{}
}

func TestMerge(t *testing.T) {
	raw := ParseRaw(sample)
	if len(raw.Leases) != 3 || len(raw.Neigh) != 3 || len(raw.Stations) != 1 {
		t.Fatalf("%+v", raw)
	}
	conf := cfgs(t, `package dhcp

config host
	option name 'tv-livingroom'
	option mac 'AA:BB:CC:00:00:03'
	option ip '192.168.1.52'

package firewall

config rule 'owrt_block_aabbcc000002'
	option src '*'
	option dest '*'
	option src_mac 'aa:bb:cc:00:00:02'
	option target 'REJECT'

package pbr

config policy
	option src_addr '192.168.1.50'
	option interface 'vpn'
`)
	l := Merge(raw, conf)
	p := byMAC(l, "aa:bb:cc:00:00:01")
	if !p.Online || p.WifiIface != "phy0-ap0" || p.Signal != -52 || p.RxBytes != 123456 || p.Connected != 3600 || p.Route != "vpn" || p.Name != "phone" {
		t.Fatalf("%+v", p)
	}
	u := byMAC(l, "aa:bb:cc:00:00:02")
	if u.Name != "" || !u.Idle || u.Online || !u.Blocked {
		t.Fatalf("%+v", u)
	}
	tv := byMAC(l, "aa:bb:cc:00:00:03")
	if !tv.Static || tv.Name != "tv-livingroom" || tv.Online || tv.HostSec == "" {
		t.Fatalf("%+v", tv)
	}
	if byMAC(l, "aa:bb:cc:00:00:04").IP != "192.168.1.60" {
		t.Fatal("neighbour-only client missing")
	}
	if l[0].MAC != "aa:bb:cc:00:00:01" {
		t.Fatalf("online clients must sort first: %v", l[0].MAC)
	}
}

func TestOnlyOurBlockRulesCount(t *testing.T) {
	conf := cfgs(t, "package firewall\n\nconfig rule\n\toption src_mac 'aa:bb:cc:00:00:01'\n\toption target 'REJECT'\n")
	if byMAC(Merge(ParseRaw(sample), conf), "aa:bb:cc:00:00:01").Blocked {
		t.Fatal("a foreign rule was treated as ours")
	}
}

func TestActions(t *testing.T) {
	c := Client{MAC: "aa:bb:cc:00:00:09", IP: "192.168.1.9", Name: "cam"}
	ch, err := Rename(c, "garage-cam")
	if err != nil || len(ch) != 3 || ch[0].Section != "lease_aabbcc000009" {
		t.Fatalf("%v %+v", err, ch)
	}
	for _, bad := range []string{"a b", "x;reboot", "", "-x", strings.Repeat("a", 70)} {
		if _, err := Rename(c, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	c.HostSec, c.Static = "@host[0]", false
	if ch, _ := MakeStatic(c); len(ch) != 1 || ch[0].Option != "ip" {
		t.Fatalf("%+v", ch)
	}
	if _, err := MakeStatic(Client{MAC: "aa:bb:cc:00:00:09"}); err == nil {
		t.Fatal("static without address")
	}
	b := SetBlocked(c, true)
	if len(b) != 6 || b[0].Section != "owrt_block_aabbcc000009" || b[0].Type != "rule" {
		t.Fatalf("%+v", b)
	}
	c.BlockSec = "owrt_block_aabbcc000009"
	if u := SetBlocked(c, false); len(u) != 1 || u[0].Section != c.BlockSec {
		t.Fatalf("%+v", u)
	}
	r, err := SetRoute(c, "vpn")
	if err != nil || len(r) != 5 || r[2].Value != "192.168.1.9" {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := SetRoute(c, "x;y"); err == nil {
		t.Fatal("bad interface accepted")
	}
	for _, x := range append(append(ch, b...), r...) {
		if err := x.Validate(); err != nil {
			t.Errorf("%s: %v", x.Describe(), err)
		}
	}
}

func TestRouteChoices(t *testing.T) {
	c := cfgs(t, "package network\n\nconfig interface 'lan'\n\toption proto 'static'\n\nconfig interface 'wan'\n\toption proto 'dhcp'\n\nconfig interface 'vpn'\n\toption proto 'wireguard'\n")
	got := strings.Join(RouteChoices(c), ",")
	if got != "vpn,wan" {
		t.Fatal(got)
	}
}
