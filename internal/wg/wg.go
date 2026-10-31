// Package wg builds WireGuard setups (a server, a peer for it, or a client
// connection to a provider) as staged changes. Key pairs are generated here on
// the PC, so a client's private key is created on the PC and shown once; only
// its public key goes to the router. Nothing here talks to the router.
package wg

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

// GenKey returns a new private and public key pair (base64).
func GenKey() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// PubFromPriv derives the public key.
func PubFromPriv(priv string) (string, error) {
	if err := CheckKey(priv); err != nil {
		return "", err
	}
	b, _ := base64.StdEncoding.DecodeString(priv)
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// NewPSK returns a random pre-shared key.
func NewPSK() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// CheckKey validates a WireGuard key: 32 bytes of base64.
func CheckKey(s string) error {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("not a WireGuard key (44 characters of base64 ending in '=')")
	}
	return nil
}

type builder struct{ out []changes.Change }

func (b *builder) add(cfg, sec, typ string) {
	b.out = append(b.out, changes.Change{Op: changes.OpAddSect, Config: cfg, Section: sec, Type: typ})
}
func (b *builder) set(cfg, sec, k, v string) {
	b.out = append(b.out, changes.Change{Op: changes.OpSet, Config: cfg, Section: sec, Option: k, Value: v})
}
func (b *builder) list(cfg, sec, k, v string) {
	b.out = append(b.out, changes.Change{Op: changes.OpAddList, Config: cfg, Section: sec, Option: k, Value: v})
}

func find(cfgs []uci.Config, name string) (uci.Config, bool) {
	for _, c := range cfgs {
		if c.Name == name {
			return c, true
		}
	}
	return uci.Config{}, false
}

func opt(s uci.Section, k string) string { o, _ := s.Option(k); return o.Value() }

func has(cfgs []uci.Config, cfg, sec string) bool {
	c, ok := find(cfgs, cfg)
	if !ok {
		return false
	}
	_, ok = c.Section(sec)
	return ok
}

var ifaceName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,14}$`)

// usedPrefixes lists every address range the router already uses.
func usedPrefixes(cfgs []uci.Config) []netip.Prefix {
	var out []netip.Prefix
	n, ok := find(cfgs, "network")
	if !ok {
		return nil
	}
	for _, s := range n.Sections {
		if s.Type != "interface" {
			continue
		}
		if ip := opt(s, "ipaddr"); ip != "" {
			if strings.Contains(ip, "/") {
				if p, err := netip.ParsePrefix(ip); err == nil {
					out = append(out, p.Masked())
				}
			} else if a, err := netip.ParseAddr(ip); err == nil {
				bits := 24
				if m := opt(s, "netmask"); m != "" {
					bits = maskBits(m)
				}
				if p, err := a.Prefix(bits); err == nil {
					out = append(out, p)
				}
			}
		}
		if o, ok := s.Option("addresses"); ok {
			for _, a := range o.Values {
				if p, err := netip.ParsePrefix(a); err == nil {
					out = append(out, p.Masked())
				}
			}
		}
	}
	return out
}

func maskBits(m string) int {
	a, err := netip.ParseAddr(m)
	if err != nil {
		return 24
	}
	n := 0
	for _, b := range a.AsSlice() {
		for ; b&0x80 != 0; b <<= 1 {
			n++
		}
	}
	return n
}

func overlaps(p netip.Prefix, used []netip.Prefix) (netip.Prefix, bool) {
	for _, u := range used {
		if u.Overlaps(p) {
			return u, true
		}
	}
	return netip.Prefix{}, false
}

// ---- server ----

// Server is a WireGuard server for phones and laptops to connect to.
type Server struct {
	Name    string // interface and zone name, e.g. "wgserver"
	Port    int
	Address string // the router's address inside the VPN, e.g. "10.8.0.1/24"
}

// NewServer creates the interface, its firewall zone, the rule that opens the
// port on the internet side, and forwarding from VPN clients to the LAN and
// the internet. It returns the changes and the server's public key.
func NewServer(s Server, cfgs []uci.Config) ([]changes.Change, string, error) {
	if !ifaceName.MatchString(s.Name) {
		return nil, "", fmt.Errorf("name: lowercase letters, digits and '_', starting with a letter, at most 15")
	}
	if s.Port < 1 || s.Port > 65535 {
		return nil, "", fmt.Errorf("port out of range")
	}
	p, err := netip.ParsePrefix(s.Address)
	if err != nil || !p.Addr().Is4() || p.Bits() > 29 || p.Bits() < 16 {
		return nil, "", fmt.Errorf("address: use something like 10.8.0.1/24")
	}
	if u, bad := overlaps(p.Masked(), usedPrefixes(cfgs)); bad {
		return nil, "", fmt.Errorf("%s overlaps %s which is already in use", p.Masked(), u)
	}
	if has(cfgs, "network", s.Name) || has(cfgs, "firewall", s.Name) {
		return nil, "", fmt.Errorf("%s already exists; choose another name", s.Name)
	}
	if fw, ok := find(cfgs, "firewall"); ok {
		for _, sec := range fw.Sections {
			if (sec.Type == "rule" || sec.Type == "redirect") && opt(sec, "src") == "wan" && opt(sec, "enabled") != "0" {
				if opt(sec, "dest_port") == strconv.Itoa(s.Port) || opt(sec, "src_dport") == strconv.Itoa(s.Port) {
					return nil, "", fmt.Errorf("port %d is already used by firewall entry %s", s.Port, sec.Ref())
				}
			}
		}
	} else {
		return nil, "", fmt.Errorf("no firewall config")
	}
	if n, ok := find(cfgs, "network"); ok {
		for _, sec := range n.Sections {
			if sec.Type == "interface" && opt(sec, "proto") == "wireguard" && opt(sec, "listen_port") == strconv.Itoa(s.Port) {
				return nil, "", fmt.Errorf("interface %s already listens on port %d", sec.Ref(), s.Port)
			}
		}
	}
	priv, pub, err := GenKey()
	if err != nil {
		return nil, "", err
	}
	var b builder
	n := s.Name
	b.add("network", n, "interface")
	b.set("network", n, "proto", "wireguard")
	b.set("network", n, "private_key", priv)
	b.set("network", n, "listen_port", strconv.Itoa(s.Port))
	b.list("network", n, "addresses", s.Address)

	b.add("firewall", n, "zone")
	b.set("firewall", n, "name", n)
	b.list("firewall", n, "network", n)
	b.set("firewall", n, "input", "ACCEPT")
	b.set("firewall", n, "output", "ACCEPT")
	b.set("firewall", n, "forward", "REJECT")
	for _, dst := range []string{"lan", "wan"} {
		sec := n + "_" + dst
		b.add("firewall", sec, "forwarding")
		b.set("firewall", sec, "src", n)
		b.set("firewall", sec, "dest", dst)
	}
	b.add("firewall", n+"_in", "rule")
	b.set("firewall", n+"_in", "name", "Allow-WireGuard-"+n)
	b.set("firewall", n+"_in", "src", "wan")
	b.set("firewall", n+"_in", "proto", "udp")
	b.set("firewall", n+"_in", "dest_port", strconv.Itoa(s.Port))
	b.set("firewall", n+"_in", "target", "ACCEPT")
	return b.out, pub, nil
}

// ---- peer for a server ----

// Peer is a device that connects to the server.
type Peer struct {
	Iface    string // the server interface
	Name     string // a label, e.g. "phone"
	Endpoint string // host:port the device connects to (the router's public address)
	FullTun  bool   // send all traffic through the router; otherwise only the VPN and LAN
	DNS      string // optional DNS server pushed to the device
}

var peerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,30}$`)

// AddPeer generates keys for a new device, registers it on the server and
// returns the configuration to import on the device. The client's private key
// exists only in the returned text.
func AddPeer(p Peer, cfgs []uci.Config) (ch []changes.Change, clientConf string, clientIP string, err error) {
	if !peerName.MatchString(p.Name) {
		return nil, "", "", fmt.Errorf("name: letters, digits, '-' and '_'")
	}
	if err := checkEndpoint(p.Endpoint); err != nil {
		return nil, "", "", err
	}
	if p.DNS != "" {
		if err := validate.IP(p.DNS); err != nil {
			return nil, "", "", fmt.Errorf("DNS: %w", err)
		}
	}
	n, ok := find(cfgs, "network")
	if !ok {
		return nil, "", "", fmt.Errorf("no network config")
	}
	srv, ok := n.Section(p.Iface)
	if !ok || opt(srv, "proto") != "wireguard" {
		return nil, "", "", fmt.Errorf("%s is not a WireGuard interface", p.Iface)
	}
	if opt(srv, "listen_port") == "" {
		return nil, "", "", fmt.Errorf("%s has no listen_port, so it is a client connection, not a server", p.Iface)
	}
	srvPub, err := PubFromPriv(opt(srv, "private_key"))
	if err != nil {
		return nil, "", "", fmt.Errorf("cannot read the server's private key (is it unset?): %w", err)
	}
	ao, _ := srv.Option("addresses")
	var srvAddr netip.Prefix
	for _, a := range ao.Values {
		if pf, e := netip.ParsePrefix(a); e == nil && pf.Addr().Is4() {
			srvAddr = pf
			break
		}
	}
	if !srvAddr.IsValid() {
		return nil, "", "", fmt.Errorf("%s has no IPv4 address", p.Iface)
	}
	// every address already taken inside the VPN subnet
	taken := map[netip.Addr]bool{srvAddr.Addr(): true}
	for _, s := range n.Sections {
		if s.Type != "wireguard_"+p.Iface {
			continue
		}
		if d := opt(s, "description"); d == p.Name {
			return nil, "", "", fmt.Errorf("a peer named %q already exists on %s", p.Name, p.Iface)
		}
		al, _ := s.Option("allowed_ips")
		for _, a := range al.Values {
			if pf, e := netip.ParsePrefix(a); e == nil {
				taken[pf.Addr()] = true
			}
		}
	}
	net := srvAddr.Masked()
	var ip netip.Addr
	for a := net.Addr().Next(); net.Contains(a); a = a.Next() {
		last := a.As4()[3]
		if last == 255 || last == 0 {
			continue
		}
		if !taken[a] {
			ip = a
			break
		}
	}
	if !ip.IsValid() {
		return nil, "", "", fmt.Errorf("no free address left in %s", net)
	}
	priv, pub, err := GenKey()
	if err != nil {
		return nil, "", "", err
	}
	psk, err := NewPSK()
	if err != nil {
		return nil, "", "", err
	}
	sec := "peer_" + p.Iface + "_" + strings.ReplaceAll(p.Name, "-", "_")
	var b builder
	b.add("network", sec, "wireguard_"+p.Iface)
	b.set("network", sec, "description", p.Name)
	b.set("network", sec, "public_key", pub)
	b.set("network", sec, "preshared_key", psk)
	b.list("network", sec, "allowed_ips", ip.String()+"/32")
	b.set("network", sec, "route_allowed_ips", "1")

	allowed := "0.0.0.0/0, ::/0"
	if !p.FullTun {
		allowed = net.String()
		for _, u := range usedPrefixes(cfgs) {
			if u.Addr().Is4() && u.Addr().IsPrivate() && u != net {
				allowed += ", " + u.String()
			}
		}
	}
	var c strings.Builder
	c.WriteString("[Interface]\nPrivateKey = " + priv + "\nAddress = " + ip.String() + "/32\n")
	if p.DNS != "" {
		c.WriteString("DNS = " + p.DNS + "\n")
	}
	c.WriteString("\n[Peer]\nPublicKey = " + srvPub + "\nPresharedKey = " + psk + "\nEndpoint = " + p.Endpoint + "\nAllowedIPs = " + allowed + "\nPersistentKeepalive = 25\n")
	return b.out, c.String(), ip.String(), nil
}

func checkEndpoint(e string) error {
	i := strings.LastIndex(e, ":")
	if i <= 0 {
		return fmt.Errorf("endpoint: use host:port, e.g. myrouter.example.org:51820")
	}
	if err := validate.Port(e[i+1:]); err != nil {
		return fmt.Errorf("endpoint port: %w", err)
	}
	h := e[:i]
	if validate.IPv4(h) != nil && validate.Hostname(h) != nil {
		return fmt.Errorf("endpoint host is not an address or a hostname")
	}
	return nil
}

// ---- client connection to a provider ----

// Client connects the router to another WireGuard server (a VPN provider or
// your own), optionally sending all LAN traffic through it.
type Client struct {
	Name       string
	PrivateKey string // empty = generate one; the public key is returned for the provider
	Address    string // the router's address on the VPN, e.g. 10.2.0.2/32
	PeerPub    string
	PSK        string // optional
	Endpoint   string // host:port
	FullTun    bool
}

// NewClient builds the connection. The second result is the public key that
// the provider or the other server needs.
func NewClient(c Client, cfgs []uci.Config) ([]changes.Change, string, []string, error) {
	if !ifaceName.MatchString(c.Name) {
		return nil, "", nil, fmt.Errorf("name: lowercase letters, digits and '_', starting with a letter, at most 15")
	}
	if has(cfgs, "network", c.Name) || has(cfgs, "firewall", c.Name) {
		return nil, "", nil, fmt.Errorf("%s already exists; choose another name", c.Name)
	}
	pf, err := netip.ParsePrefix(c.Address)
	if err != nil || !pf.Addr().Is4() {
		return nil, "", nil, fmt.Errorf("address: use something like 10.2.0.2/32")
	}
	if err := CheckKey(c.PeerPub); err != nil {
		return nil, "", nil, fmt.Errorf("server public key: %w", err)
	}
	if c.PSK != "" {
		if err := CheckKey(c.PSK); err != nil {
			return nil, "", nil, fmt.Errorf("pre-shared key: %w", err)
		}
	}
	if err := checkEndpoint(c.Endpoint); err != nil {
		return nil, "", nil, err
	}
	priv, pub := c.PrivateKey, ""
	if priv == "" {
		if priv, pub, err = GenKey(); err != nil {
			return nil, "", nil, err
		}
	} else if pub, err = PubFromPriv(priv); err != nil {
		return nil, "", nil, fmt.Errorf("private key: %w", err)
	}
	i := strings.LastIndex(c.Endpoint, ":")
	host, port := c.Endpoint[:i], c.Endpoint[i+1:]
	var warn []string
	var b builder
	n := c.Name
	b.add("network", n, "interface")
	b.set("network", n, "proto", "wireguard")
	b.set("network", n, "private_key", priv)
	b.list("network", n, "addresses", c.Address)
	peer := "peer_" + n
	b.add("network", peer, "wireguard_"+n)
	b.set("network", peer, "description", n)
	b.set("network", peer, "public_key", c.PeerPub)
	if c.PSK != "" {
		b.set("network", peer, "preshared_key", c.PSK)
	}
	b.set("network", peer, "endpoint_host", host)
	b.set("network", peer, "endpoint_port", port)
	b.set("network", peer, "persistent_keepalive", "25")
	b.set("network", peer, "route_allowed_ips", "1")
	if c.FullTun {
		b.list("network", peer, "allowed_ips", "0.0.0.0/0")
		b.list("network", peer, "allowed_ips", "::/0")
		if nw, ok := find(cfgs, "network"); ok {
			for _, s := range nw.Sections {
				if s.Type != "interface" || opt(s, "proto") != "wireguard" {
					continue
				}
				if opt(s, "listen_port") == "" {
					warn = append(warn, fmt.Sprintf("interface %s is already a WireGuard client; two connections both claiming the default route will fight. Use policy-based routing (pbr) to split clients between them", s.Ref()))
				}
			}
		}
	} else {
		b.list("network", peer, "allowed_ips", pf.Masked().String())
	}
	b.add("firewall", n, "zone")
	b.set("firewall", n, "name", n)
	b.list("firewall", n, "network", n)
	b.set("firewall", n, "input", "REJECT")
	b.set("firewall", n, "output", "ACCEPT")
	b.set("firewall", n, "forward", "REJECT")
	b.set("firewall", n, "masq", "1")
	b.set("firewall", n, "mtu_fix", "1")
	b.add("firewall", n+"_lan", "forwarding")
	b.set("firewall", n+"_lan", "src", "lan")
	b.set("firewall", n+"_lan", "dest", n)
	return b.out, pub, warn, nil
}
