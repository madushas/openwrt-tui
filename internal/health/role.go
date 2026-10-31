package health

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/madushakv/owrt-tui/internal/uci"
)

// Role is what the device is for. It tunes which findings matter: an access
// point has no WAN to expose, and a firewall behind another router is not
// directly on the internet.
type Role string

const (
	RoleAuto     Role = ""
	RoleRouter   Role = "router"
	RoleBehind   Role = "behind"
	RoleCellular Role = "cellular"
	RoleAP       Role = "ap"
	RoleRepeater Role = "repeater"
)

// Roles lists the choices in the order the screen cycles through them.
var Roles = []Role{RoleAuto, RoleRouter, RoleBehind, RoleCellular, RoleAP, RoleRepeater}

// Label is the wording shown to the user.
func (r Role) Label() string {
	switch r {
	case RoleRouter:
		return "router (WAN faces the internet)"
	case RoleBehind:
		return "firewall/VPN behind another router"
	case RoleCellular:
		return "router on a SIM/cellular uplink"
	case RoleAP:
		return "access point only"
	case RoleRepeater:
		return "Wi-Fi repeater/client"
	}
	return "auto"
}

func isPrivate4(ip string) bool {
	p := strings.Split(ip, ".")
	if len(p) != 4 {
		return false
	}
	switch {
	case p[0] == "10", p[0] == "192" && p[1] == "168":
		return true
	case p[0] == "172":
		n := 0
		for _, c := range p[1] {
			n = n*10 + int(c-'0')
		}
		return n >= 16 && n <= 31
	}
	return false
}

// Detect guesses the role from the configuration and what the router reports:
// the device of its default route says whether the uplink is cellular, and a
// private WAN address means another router is upstream. It never returns RoleAuto.
func Detect(cfgs []uci.Config, f Facts) Role {
	wanIP := f.WanIP
	in := Input{Configs: cfgs}
	n, haveNet := cfg(in, "network")
	if !haveNet {
		return RoleRouter // nothing to judge by
	}
	wan := uplink(in, n)
	sta := false
	if w, ok := cfg(in, "wireless"); ok {
		for _, s := range w.Sections {
			sta = sta || (s.Type == "wifi-iface" && opt(s, "mode") == "sta")
		}
	}
	if wan == nil {
		if sta {
			return RoleRepeater
		}
		return RoleAP
	}
	if cell, known := cellularDev(f.UpDev); known {
		if cell {
			return RoleCellular
		}
	} else if isCellular(opt(*wan, "proto")) {
		return RoleCellular
	}
	if isPrivate4(wanIP) || isPrivate4(opt(*wan, "ipaddr")) || isPrivate4(opt(*wan, "gateway")) {
		return RoleBehind
	}
	return RoleRouter
}

// hidden lists finding-ID prefixes that make no sense for a role.
var hidden = map[Role][]string{
	RoleAP:       {"wan-forward", "wan-rule", "open-zone", "multi-zone", "fwd-dangling", "pbr-dup"},
	RoleRepeater: {"wan-forward", "wan-rule", "open-zone", "multi-zone", "fwd-dangling", "pbr-dup"},
}

// softened lists prefixes that are capped to a note for a role, with why.
var softened = map[Role]struct {
	prefixes []string
	why      string
}{
	RoleBehind:   {[]string{"wan-forward", "wan-rule"}, " (This device sits behind another router, so this is only reachable from that network unless it forwards the port too.)"},
	RoleCellular: {[]string{"wan-forward", "wan-rule"}, " (Mobile carriers usually block inbound connections, so this is probably not reachable from the internet unless your plan gives a public IP.)"},
}

func applyRole(list []Finding, r Role) []Finding {
	var out []Finding
next:
	for _, f := range list {
		for _, p := range hidden[r] {
			if strings.HasPrefix(f.ID, p) {
				continue next
			}
		}
		if sf, ok := softened[r]; ok {
			for _, p := range sf.prefixes {
				if strings.HasPrefix(f.ID, p) && f.Sev > Info {
					f.Sev = Info
					f.Detail += sf.why
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// LoadRole reads the router's chosen role (RoleAuto when unset).
func LoadRole(dir, router string) Role {
	p, err := acceptPath(dir, router)
	if err != nil {
		return RoleAuto
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(p)), "roles", filepath.Base(p)))
	if err != nil {
		return RoleAuto
	}
	var v struct{ Role Role }
	_ = json.Unmarshal(b, &v)
	for _, r := range Roles {
		if r == v.Role {
			return r
		}
	}
	return RoleAuto
}

// SaveRole stores the choice privately.
func SaveRole(dir, router string, r Role) error {
	p, err := acceptPath(dir, router)
	if err != nil {
		return err
	}
	d := filepath.Join(filepath.Dir(filepath.Dir(p)), "roles")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(struct{ Role Role }{r})
	return os.WriteFile(filepath.Join(d, filepath.Base(p)), b, 0o600)
}

var modemProtos = map[string]bool{"qmi": true, "mbim": true, "ncm": true, "modemmanager": true, "3g": true, "wwan": true}

func isCellular(proto string) bool { return modemProtos[proto] }

// uplink finds the interface that carries the internet connection: a member of
// the firewall's wan zone, else the interface called wan. SIM-only routers
// usually name it wwan or lte, so the name alone is not enough.
func uplink(in Input, n uci.Config) *uci.Section {
	byName := map[string]*uci.Section{}
	for i := range n.Sections {
		if n.Sections[i].Type == "interface" && n.Sections[i].Name != "" {
			byName[n.Sections[i].Name] = &n.Sections[i]
		}
	}
	if w := byName["wan"]; w != nil && !isCellular(opt(*w, "proto")) {
		if _, known := cellularDev(in.Facts.UpDev); !known {
			return w // a leftover modem interface must not override a real wan
		}
	}
	if fw, ok := cfg(in, "firewall"); ok {
		var first *uci.Section
		for _, z := range fw.Sections {
			if z.Type != "zone" || opt(z, "name") != "wan" {
				continue
			}
			for _, name := range vals(z, "network") {
				s := byName[name]
				if s == nil {
					continue
				}
				if isCellular(opt(*s, "proto")) {
					return s // a modem in the wan zone is the uplink
				}
				if first == nil && name != "wan6" {
					first = s
				}
			}
		}
		if first != nil {
			return first
		}
	}
	return byName["wan"]
}

var cellPrefixes = []string{"wwan", "wwp", "rmnet", "ccmni", "usb", "3g-", "qmi-", "mbim-", "ncm-", "modem"}

// cellularDev classifies the default route's device name. known is false when
// the router did not report one.
func cellularDev(dev string) (cell, known bool) {
	if dev == "" {
		return false, false
	}
	for _, p := range cellPrefixes {
		if strings.HasPrefix(dev, p) {
			return true, true
		}
	}
	return false, true
}

// IsCGNAT reports an address in the carrier-grade NAT range 100.64.0.0/10.
func IsCGNAT(ip string) bool {
	p := strings.Split(ip, ".")
	if len(p) != 4 || p[0] != "100" {
		return false
	}
	n := 0
	for _, c := range p[1] {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 64 && n <= 127
}
