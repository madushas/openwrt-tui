package wg

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

package firewall

config zone
	option name 'lan'
	list network 'lan'

config zone
	option name 'wan'
	list network 'wan'
`

// withNet adds sections to the network config (they must precede "package firewall").
func withNet(extra string) string {
	return strings.Replace(base, "package firewall", extra+"\npackage firewall", 1)
}

func cfgs(t *testing.T, s string) []uci.Config {
	c, err := uci.ParseExport(s)
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
		} else if !seen[k] {
			t.Errorf("%s used before created", k)
		}
	}
}

func TestKeysAreWireGuardCompatible(t *testing.T) {
	priv, pub, err := GenKey()
	if err != nil || CheckKey(priv) != nil || CheckKey(pub) != nil {
		t.Fatal(err)
	}
	if p, _ := PubFromPriv(priv); p != pub {
		t.Fatal("derived public key differs")
	}
	// RFC 7748 test vector: the public key of Alice's private key
	const alicePriv = "dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo=" // 77076d0a...
	const alicePub = "hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo="  // 8520f009...
	if p, err := PubFromPriv(alicePriv); err != nil || p != alicePub {
		t.Fatalf("%v %s", err, p)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("A", 44)} {
		if CheckKey(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestServerThenPeer(t *testing.T) {
	c := cfgs(t, base)
	ch, srvPub, err := NewServer(Server{Name: "wgserver", Port: 51820, Address: "10.8.0.1/24"}, c)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, ch)
	// the zone only lets the VPN reach lan and wan, and opens exactly one port
	var forwards, ports []string
	var privKey string
	for _, x := range ch {
		if x.Section == "wgserver_lan" || x.Section == "wgserver_wan" {
			if x.Option == "dest" {
				forwards = append(forwards, x.Value)
			}
		}
		if x.Option == "dest_port" {
			ports = append(ports, x.Value)
		}
		if x.Option == "private_key" {
			privKey = x.Value
		}
	}
	if len(forwards) != 2 || len(ports) != 1 || ports[0] != "51820" {
		t.Fatalf("%v %v", forwards, ports)
	}
	if p, _ := PubFromPriv(privKey); p != srvPub {
		t.Fatal("returned server public key does not match the private key staged")
	}

	// apply the changes to the config text, then add two peers
	conf := withNet("\nconfig interface 'wgserver'\n\toption proto 'wireguard'\n\toption private_key '" + privKey + "'\n\toption listen_port '51820'\n\tlist addresses '10.8.0.1/24'\n")
	c2 := cfgs(t, conf)
	pc, text, ip, err := AddPeer(Peer{Iface: "wgserver", Name: "phone", Endpoint: "vpn.example.org:51820", FullTun: true, DNS: "10.8.0.1"}, c2)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, pc)
	if ip != "10.8.0.2" {
		t.Fatalf("first peer got %s", ip)
	}
	for _, want := range []string{"[Interface]", "Address = 10.8.0.2/32", "DNS = 10.8.0.1", "PublicKey = " + srvPub, "Endpoint = vpn.example.org:51820", "AllowedIPs = 0.0.0.0/0, ::/0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("client config lacks %q:\n%s", want, text)
		}
	}
	// the client's private key must not be among the staged changes
	var clientPriv string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "PrivateKey = ") {
			clientPriv = strings.TrimPrefix(l, "PrivateKey = ")
		}
	}
	for _, x := range pc {
		if x.Value == clientPriv {
			t.Fatal("the client's private key was staged on the router")
		}
	}
	// the peer's public key matches the private key in the config text
	cp, _ := PubFromPriv(clientPriv)
	found := false
	for _, x := range pc {
		if x.Option == "public_key" && x.Value == cp {
			found = true
		}
	}
	if !found {
		t.Fatal("staged public key does not belong to the client's private key")
	}
	// a second peer gets the next address; a repeated name is refused
	taken := strings.Replace(conf, "package firewall", "\nconfig wireguard_wgserver 'peer_wgserver_phone'\n\toption description 'phone'\n\toption public_key 'x'\n\tlist allowed_ips '10.8.0.2/32'\n\npackage firewall", 1)
	_, _, ip2, err := AddPeer(Peer{Iface: "wgserver", Name: "laptop", Endpoint: "1.2.3.4:51820"}, cfgs(t, taken))
	if err != nil || ip2 != "10.8.0.3" {
		t.Fatalf("%v %s", err, ip2)
	}
	if _, _, _, err := AddPeer(Peer{Iface: "wgserver", Name: "phone", Endpoint: "1.2.3.4:51820"}, cfgs(t, taken)); err == nil {
		t.Fatal("duplicate name accepted")
	}
	// split tunnel lists the LAN
	_, split, _, _ := AddPeer(Peer{Iface: "wgserver", Name: "tab", Endpoint: "1.2.3.4:51820"}, c2)
	if !strings.Contains(split, "AllowedIPs = 10.8.0.0/24, 192.168.1.0/24") {
		t.Fatalf("%s", split)
	}
}

func TestServerRefusals(t *testing.T) {
	c := cfgs(t, base)
	for name, s := range map[string]Server{
		"overlap": {Name: "wgs", Port: 51820, Address: "192.168.1.1/24"},
		"badname": {Name: "WG Server", Port: 51820, Address: "10.8.0.1/24"},
		"port":    {Name: "wgs", Port: 0, Address: "10.8.0.1/24"},
		"addr":    {Name: "wgs", Port: 51820, Address: "10.8.0.1"},
		"exists":  {Name: "lan", Port: 51820, Address: "10.8.0.1/24"},
	} {
		if _, _, err := NewServer(s, c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	used := base + "\nconfig rule\n\toption src 'wan'\n\toption dest_port '51820'\n\toption target 'ACCEPT'\n"
	if _, _, err := NewServer(Server{Name: "wgs", Port: 51820, Address: "10.8.0.1/24"}, cfgs(t, used)); err == nil {
		t.Fatal("port already open accepted")
	}
	// a client connection is not a server
	cl := withNet("\nconfig interface 'vpn'\n\toption proto 'wireguard'\n\toption private_key 'x'\n\tlist addresses '10.2.0.2/32'\n")
	if _, _, _, err := AddPeer(Peer{Iface: "vpn", Name: "p", Endpoint: "a.b:1"}, cfgs(t, cl)); err == nil {
		t.Fatal("peer added to a client interface")
	}
	for _, ep := range []string{"nohost", "host:0", ":51820", "bad host:51820", "a;b:1"} {
		if _, _, _, err := AddPeer(Peer{Iface: "x", Name: "p", Endpoint: ep}, c); err == nil {
			t.Errorf("endpoint %q accepted", ep)
		}
	}
}

func TestClient(t *testing.T) {
	c := cfgs(t, withNet("\nconfig interface 'vpn'\n\toption proto 'wireguard'\n\toption private_key 'x'\n\tlist addresses '10.2.0.2/32'\n"))
	_, peerPub, _ := GenKey()
	ch, pub, warn, err := NewClient(Client{Name: "provider", Address: "10.5.0.2/32", PeerPub: peerPub, Endpoint: "vpn.example.net:51820", FullTun: true}, c)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, ch)
	if CheckKey(pub) != nil {
		t.Fatal("no public key returned for the provider")
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "vpn") {
		t.Fatalf("no warning about the competing default route: %v", warn)
	}
	var masq, port string
	for _, x := range ch {
		if x.Option == "masq" {
			masq = x.Value
		}
		if x.Option == "endpoint_port" {
			port = x.Value
		}
	}
	if masq != "1" || port != "51820" {
		t.Fatal("zone must masquerade; endpoint split wrong")
	}
	for name, cl := range map[string]Client{
		"peer key": {Name: "p1", Address: "10.5.0.2/32", PeerPub: "nope", Endpoint: "a.b:1"},
		"address":  {Name: "p1", Address: "10.5.0.2", PeerPub: peerPub, Endpoint: "a.b:1"},
		"exists":   {Name: "vpn", Address: "10.5.0.2/32", PeerPub: peerPub, Endpoint: "a.b:1"},
		"priv":     {Name: "p1", Address: "10.5.0.2/32", PeerPub: peerPub, Endpoint: "a.b:1", PrivateKey: "bad"},
	} {
		if _, _, _, err := NewClient(cl, c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
