package health

import "testing"

// Trimmed from a real OpenWrt 25.12 export (see testdata/README.md).
const realFW = `package firewall
config zone 'lan'
	option name 'lan'
	option input 'ACCEPT'
	option output 'ACCEPT'
	option forward 'ACCEPT'
	list network 'lan'
	list network 'direct'
	list network 'direct'
	list network 'direct'
config zone 'direct_zone'
	option name 'direct_zone'
	option input 'ACCEPT'
	list network 'direct'
config zone 'wan'
	option name 'wan'
	list network 'wan'
config forwarding
	option src 'direct'
	option dest 'wan'
config forwarding
	option src 'direct_zone'
	option dest 'wan'
package pbr
config policy 'vpn_handshake_bypass'
	option dest_addr '192.248.2.10'
	option interface 'wan'
config policy 'vpn_bypass'
	option dest_addr '192.248.2.10'
	option interface 'wan'
config policy 'other'
	option dest_addr '1.1.1.1'
	option interface 'wan'
`

func TestRealFirewallFindings(t *testing.T) {
	fs := Check(Input{Configs: parse(t, realFW)})
	if f := find(fs, "multi-zone:direct"); f == nil {
		t.Fatalf("direct in lan and direct_zone not flagged: %s", ids(fs))
	}
	if f := find(fs, "multi-zone:lan"); f != nil {
		t.Fatal("a repeated entry inside ONE zone must not count as several zones")
	}
	if find(fs, "fwd-dangling:@forwarding[0]:src") == nil || find(fs, "fwd-dangling:@forwarding[1]") != nil {
		t.Fatalf("dangling forwarding wrong: %s", ids(fs))
	}
	if find(fs, "pbr-dup:vpn_bypass") == nil || find(fs, "pbr-dup:other") != nil {
		t.Fatalf("duplicate policy wrong: %s", ids(fs))
	}
}
