package uci

import "testing"

const sample = `package network

config interface 'loopback'
	option device 'lo'
	option proto 'static'
	list ipaddr '127.0.0.1/8'

config interface 'wg0'
	option proto 'wireguard'
	option private_key 'abc'

config wireguard_wg0
	option description 'it'\''s a peer'
	list allowed_ips '10.9.0.2/32'
	list allowed_ips '10.9.1.0/24'

config wireguard_wg0
	option description 'second'

package firewall

config rule
	option name 'Allow-WG'
`

func TestParseExport(t *testing.T) {
	cfgs, err := ParseExport(sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfgs) != 2 || cfgs[0].Name != "network" || cfgs[1].Name != "firewall" {
		t.Fatalf("configs: %+v", cfgs)
	}
	n := cfgs[0]
	if len(n.Sections) != 4 {
		t.Fatalf("sections: %d", len(n.Sections))
	}
	lo, ok := n.Section("loopback")
	if !ok || lo.Type != "interface" {
		t.Fatalf("loopback: %+v", lo)
	}
	p0, _ := n.Section("@wireguard_wg0[0]")
	if !p0.Anonymous {
		t.Fatal("peer should be anonymous")
	}
	d, _ := p0.Option("description")
	if d.Value() != "it's a peer" {
		t.Fatalf("quote handling: %q", d.Value())
	}
	al, _ := p0.Option("allowed_ips")
	if !al.IsList || len(al.Values) != 2 {
		t.Fatalf("list: %+v", al)
	}
	if _, ok := n.Section("@wireguard_wg0[1]"); !ok {
		t.Fatal("second anonymous peer should be index 1")
	}
	r, ok := cfgs[1].Section("@rule[0]")
	if !ok || r.Ref() != "@rule[0]" {
		t.Fatal("firewall rule ref")
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"option x 'y'", "package a\noption x 'y'", "package a\nconfig t\noption x 'unterminated", "bogus line"} {
		if _, err := ParseExport(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
