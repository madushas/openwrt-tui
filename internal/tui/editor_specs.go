package tui

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

// ---- helpers shared by the specs ----

func joinOpt(s uci.Section, k string) string {
	o, _ := s.Option(k)
	return strings.Join(o.Values, " ")
}

func pick(list []string, prefer string) string {
	for _, x := range list {
		if x == prefer {
			return x
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return ""
}

// choice builds a choice field; a stored value that is not among the choices is kept.
func choice(lbl, key string, choices []string, raw, emptyLabel, star string) *field {
	v := label(raw, emptyLabel, star)
	found := false
	for _, c := range choices {
		if c == v {
			found = true
		}
	}
	if !found && v != "" {
		choices = append(append([]string(nil), choices...), v)
	}
	return &field{Label: lbl, Key: key, Kind: kChoice, Choices: choices, Value: v}
}

func boolf(lbl, key string, s *uci.Section, hint string) *field {
	v := ""
	if s != nil && (opt(*s, key) == "1" || opt(*s, key) == "on" || opt(*s, key) == "true") {
		v = "1"
	}
	return &field{Label: lbl, Key: key, Kind: kBool, Value: v, Hint: hint}
}

func textf(lbl, key string, s *uci.Section, hint string, v validate.Func) *field {
	val := ""
	if s != nil {
		val = joinOpt(*s, key)
	}
	return &field{Label: lbl, Key: key, Value: val, Hint: hint, Validate: v}
}

var portRe = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?$`)

func validPorts(v string) error {
	for _, p := range strings.Fields(v) {
		if !portRe.MatchString(p) {
			return fmt.Errorf("%q: use ports like 80, 8000-8100 or a list separated by spaces", p)
		}
		for _, n := range strings.Split(p, "-") {
			if x, _ := strconv.Atoi(n); x < 1 || x > 65535 {
				return fmt.Errorf("%q: ports are 1 to 65535", p)
			}
		}
	}
	return nil
}

func validIPorCIDR(v string) error {
	if strings.Contains(v, "/") {
		return validate.CIDR4(v)
	}
	return validate.IPv4(v)
}

func req(f *field) *field { f.Required = true; return f }

func fieldBy(f *form, key string) *field {
	for _, fl := range f.fields {
		if fl.Key == key {
			return fl
		}
	}
	return &field{}
}

var devTok = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)

func validDevList(v string) error {
	for _, p := range strings.Fields(v) {
		if !devTok.MatchString(p) {
			return fmt.Errorf("%q is not a device name (e.g. lan1 eth0.3)", p)
		}
	}
	return nil
}

var vlanPort = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}(:[tu*]{1,3})?$`)

func validVlanPorts(v string) error {
	for _, p := range strings.Fields(v) {
		if !vlanPort.MatchString(p) {
			return fmt.Errorf("%q: use port or port:flags, e.g. lan1:u* (untagged, PVID) or lan2:t (tagged)", p)
		}
	}
	return nil
}

// ---- firewall ----

var policies = []string{"ACCEPT", "REJECT", "DROP"}

func zoneSpec() secSpec {
	return secSpec{
		ID: "zones", Title: "Zones", Cfg: "firewall", Types: []string{"zone"}, NewType: "zone",
		Cols: []col{
			{"NAME", 10, func(s uci.Section) string { return opt(s, "name") }},
			{"NETWORKS", 22, func(s uci.Section) string { return joinOpt(s, "network") }},
			{"INPUT", 7, func(s uci.Section) string { return orDash(opt(s, "input")) }},
			{"OUTPUT", 7, func(s uci.Section) string { return orDash(opt(s, "output")) }},
			{"FORWARD", 7, func(s uci.Section) string { return orDash(opt(s, "forward")) }},
			{"NAT", 4, func(s uci.Section) string {
				if opt(s, "masq") == "1" {
					return "masq"
				}
				return ""
			}},
		},
		ListKeys: []string{"network"},
		Fields: func(ed *secEditor, s *uci.Section) []*field {
			var f []*field
			if s == nil {
				f = append(f, req(&field{Label: "Name", Key: "name", Hint: "letters, digits, '_' (at most 11)", Validate: func(v string) error {
					if !regexp.MustCompile(`^[A-Za-z0-9_]{1,11}$`).MatchString(v) {
						return fmt.Errorf("letters, digits and '_', at most 11")
					}
					for _, z := range ed.zones() {
						if z == v {
							return fmt.Errorf("a zone named %q already exists", v)
						}
					}
					return nil
				}}))
			}
			pol := func(lbl, key, def string) *field {
				raw := def
				if s != nil {
					raw = opt(*s, key)
				}
				return choice(lbl, key, policies, raw, "", "")
			}
			f = append(f,
				textf("Networks", "network", s, "interfaces in this zone, separated by spaces", func(v string) error {
					known := map[string]bool{}
					for _, i := range ed.ifaces() {
						known[i] = true
					}
					for _, n := range strings.Fields(v) {
						if !known[n] {
							return fmt.Errorf("no interface named %q", n)
						}
					}
					return nil
				}),
				pol("Input (to the router)", "input", "REJECT"),
				pol("Output (from the router)", "output", "ACCEPT"),
				pol("Forward (between zones)", "forward", "REJECT"),
				boolf("Masquerade (NAT)", "masq", s, "on for an internet-facing zone"),
				boolf("MSS clamping", "mtu_fix", s, "on together with masquerade"),
			)
			return f
		},
		NewName: func(ed *secEditor, f *form) string { return ed.uniqueName("zone", fieldBy(f, "name").Value) },
		Delete: func(ed *secEditor, s uci.Section) ([]changes.Change, []string, error) {
			zn := opt(s, "name")
			fw := ed.config()
			var out []changes.Change
			var users []string
			for _, x := range fw.Sections {
				switch x.Type {
				case "forwarding":
					if opt(x, "src") == zn || opt(x, "dest") == zn {
						out = append(out, changes.Change{Op: changes.OpDelete, Config: "firewall", Section: x.Ref(), Old: "forwarding " + opt(x, "src") + " -> " + opt(x, "dest")})
					}
				case "rule", "redirect":
					if opt(x, "src") == zn || opt(x, "dest") == zn {
						users = append(users, fmt.Sprintf("%s %q", x.Type, opt(x, "name")))
					}
				}
			}
			if len(users) > 0 {
				return nil, nil, fmt.Errorf("zone %s is used by %s; delete or change those first", zn, strings.Join(users, ", "))
			}
			return append(out, changes.Change{Op: changes.OpDelete, Config: "firewall", Section: s.Ref(), Old: "zone " + zn}), nil, nil
		},
	}
}

func forwardingSpec() secSpec {
	return secSpec{
		ID: "forwards", Title: "Forwardings", Cfg: "firewall", Types: []string{"forwarding"}, NewType: "forwarding",
		Empty: "No traffic is allowed between zones.",
		Cols: []col{
			{"FROM ZONE", 12, func(s uci.Section) string { return opt(s, "src") }},
			{"TO ZONE", 12, func(s uci.Section) string { return opt(s, "dest") }},
		},
		Pre: func(ed *secEditor) string {
			if len(ed.zones()) < 2 {
				return "you need at least two zones first"
			}
			return ""
		},
		Fields: func(ed *secEditor, s *uci.Section) []*field {
			src, dst := "", ""
			if s != nil {
				src, dst = opt(*s, "src"), opt(*s, "dest")
			} else {
				src = pick(ed.zones(), "lan")
				dst = pick(ed.zones(), "wan")
			}
			return []*field{choice("From zone", "src", ed.zones(), src, "", ""), choice("To zone", "dest", ed.zones(), dst, "", "")}
		},
		Cross: func(ed *secEditor, f *form) {
			if fieldBy(f, "src").Value == fieldBy(f, "dest").Value {
				fieldBy(f, "dest").err = "must differ from the source zone"
			}
			for _, x := range ed.config().Sections {
				if x.Type == "forwarding" && opt(x, "src") == fieldBy(f, "src").Value && opt(x, "dest") == fieldBy(f, "dest").Value && (ed.edit == nil || x.Ref() != ed.edit.Ref()) {
					fieldBy(f, "dest").err = "this forwarding already exists"
				}
			}
		},
		NewName: func(ed *secEditor, f *form) string {
			return ed.uniqueName("fwd", fieldBy(f, "src").Value+"_"+fieldBy(f, "dest").Value)
		},
	}
}

var protos = []string{"tcp", "udp", "tcp udp", "icmp", "all"}

func needZones(ed *secEditor) string {
	if len(ed.zones()) == 0 {
		return "no firewall zones are defined yet"
	}
	return ""
}

func ruleSpec() secSpec {
	return secSpec{
		ID: "rules", Title: "Rules", Cfg: "firewall", Types: []string{"rule"}, NewType: "rule", Ordered: true,
		Empty:     "No custom traffic rules.",
		ToggleOpt: "enabled", ToggleOff: "0",
		Cols: []col{
			{"NAME", 24, func(s uci.Section) string { return orDash(opt(s, "name")) }},
			{"FROM", 9, func(s uci.Section) string { return orDash(opt(s, "src")) }},
			{"TO", 9, func(s uci.Section) string {
				if opt(s, "dest") == "" {
					return "router"
				}
				return opt(s, "dest")
			}},
			{"PROTO", 8, func(s uci.Section) string { return orDash(opt(s, "proto")) }},
			{"PORT", 11, func(s uci.Section) string { return orDash(joinOpt(s, "dest_port")) }},
			{"ACTION", 6, func(s uci.Section) string { return orDash(opt(s, "target")) }},
		},
		Pre: needZones,
		Fields: func(ed *secEditor, s *uci.Section) []*field {
			get := func(k, def string) string {
				if s != nil {
					return opt(*s, k)
				}
				return def
			}
			zs := ed.zones()
			return []*field{
				req(textf("Name", "name", s, "what this rule is for", validate.Text(60))),
				choice("From zone", "src", append([]string{"(any zone)"}, zs...), get("src", pick(zs, "wan")), "(any zone)", "(any zone)"),
				choice("To", "dest", append([]string{"(router)", "(any zone)"}, zs...), get("dest", ""), "(router)", "(any zone)"),
				choice("Protocol", "proto", protos, get("proto", "tcp"), "all", ""),
				textf("Source address", "src_ip", s, "optional: a device or network, e.g. 192.168.1.50 or 10.0.0.0/8", validIPorCIDR),
				textf("Source MAC", "src_mac", s, "optional: aa:bb:cc:dd:ee:ff", validate.MAC),
				textf("Source port", "src_port", s, "optional", validPorts),
				textf("Destination address", "dest_ip", s, "optional", validIPorCIDR),
				textf("Destination port", "dest_port", s, "e.g. 22 or 8000-8100", validPorts),
				choice("Action", "target", policies, get("target", "ACCEPT"), "ACCEPT", ""),
				choice("Address family", "family", []string{"(any family)", "ipv4", "ipv6"}, get("family", ""), "(any family)", ""),
			}
		},
		Cross: func(ed *secEditor, f *form) {
			p := fieldBy(f, "proto").Value
			if (fieldBy(f, "dest_port").Value != "" || fieldBy(f, "src_port").Value != "") && p != "tcp" && p != "udp" && p != "tcp udp" {
				fieldBy(f, "dest_port").err = "ports need protocol tcp or udp"
			}
		},
		NewName: func(ed *secEditor, f *form) string { return ed.uniqueName("rule", fieldBy(f, "name").Value) },
	}
}

func redirectSpec() secSpec {
	return secSpec{
		ID: "redirects", Title: "Port forwards", Cfg: "firewall", Types: []string{"redirect"}, NewType: "redirect", Ordered: true,
		Empty:     "No port forwards.",
		ToggleOpt: "enabled", ToggleOff: "0",
		Cols: []col{
			{"NAME", 20, func(s uci.Section) string { return orDash(opt(s, "name")) }},
			{"FROM", 6, func(s uci.Section) string { return orDash(opt(s, "src")) }},
			{"PORT", 11, func(s uci.Section) string { return orDash(opt(s, "src_dport")) }},
			{"TO", 22, func(s uci.Section) string {
				port := opt(s, "dest_port")
				if port == "" {
					port = opt(s, "src_dport")
				}
				if opt(s, "dest_ip") == "" {
					return "-"
				}
				return opt(s, "dest_ip") + ":" + port
			}},
			{"PROTO", 8, func(s uci.Section) string { return orDash(opt(s, "proto")) }},
			{"TYPE", 5, func(s uci.Section) string { return orDash(opt(s, "target")) }},
		},
		Pre: needZones,
		Fields: func(ed *secEditor, s *uci.Section) []*field {
			get := func(k, def string) string {
				if s != nil {
					return opt(*s, k)
				}
				return def
			}
			zs := ed.zones()
			return []*field{
				req(textf("Name", "name", s, "", validate.Text(60))),
				choice("From zone", "src", zs, get("src", pick(zs, "wan")), "", ""),
				req(textf("External port", "src_dport", s, "the port on the outside", validPorts)),
				choice("To zone", "dest", zs, get("dest", pick(zs, "lan")), "", ""),
				req(textf("Inside address", "dest_ip", s, "the device that receives it", validate.IPv4)),
				textf("Inside port", "dest_port", s, "empty = same as external", validPorts),
				choice("Protocol", "proto", []string{"tcp", "udp", "tcp udp"}, get("proto", "tcp"), "tcp udp", ""),
				choice("Type", "target", []string{"DNAT", "SNAT"}, get("target", "DNAT"), "DNAT", ""),
			}
		},
		NewName: func(ed *secEditor, f *form) string { return ed.uniqueName("redir", fieldBy(f, "name").Value) },
	}
}

// ---- routes ----

func routeSpec() secSpec {
	return secSpec{
		ID: "routes", Title: "Routes", Cfg: "network", Types: []string{"route", "route6"}, NewType: "route",
		Empty: "No static routes.",
		Cols: []col{
			{"TARGET", 20, func(s uci.Section) string {
				t := opt(s, "target")
				if m := opt(s, "netmask"); m != "" && !strings.Contains(t, "/") {
					t += "/" + maskBits(m)
				}
				return orDash(t)
			}},
			{"VIA", 16, func(s uci.Section) string { return orDash(opt(s, "gateway")) }},
			{"INTERFACE", 10, func(s uci.Section) string { return orDash(opt(s, "interface")) }},
			{"METRIC", 6, func(s uci.Section) string { return orDash(opt(s, "metric")) }},
		},
		Live: func(ctx context.Context, e *env) []string {
			l, _ := e.S.Routes(ctx)
			return l
		},
		Pre: func(ed *secEditor) string {
			if len(ed.ifaces()) == 0 {
				return "no interfaces are defined"
			}
			return ""
		},
		CanEdit: func(s uci.Section) string {
			switch {
			case s.Type == "route6":
				return "IPv6 routes are edited on the Config screen"
			case opt(s, "netmask") != "":
				return "this route uses the old target + netmask form; edit it on the Config screen"
			}
			return ""
		},
		Fields: func(ed *secEditor, s *uci.Section) []*field {
			ifc := ""
			if s != nil {
				ifc = opt(*s, "interface")
			} else {
				ifc = pick(ed.ifaces(), "lan")
			}
			return []*field{
				choice("Interface", "interface", ed.ifaces(), ifc, "", ""),
				req(textf("Target network", "target", s, "CIDR, e.g. 10.5.0.0/16", validate.CIDR4)),
				textf("Gateway", "gateway", s, "the next hop; must be inside the interface's network", validate.IPv4),
				textf("Metric", "metric", s, "optional; lower wins", validate.IntRange(0, 9999)),
			}
		},
		NewName: func(ed *secEditor, f *form) string {
			return ed.uniqueName("route", strings.ReplaceAll(strings.ReplaceAll(fieldBy(f, "target").Value, "/", "_"), ".", "_"))
		},
	}
}

// ---- bridges and VLANs ----

func bridgeSpec() secSpec {
	return secSpec{
		ID: "bridges", Title: "Bridges", Cfg: "network", Types: []string{"device"}, NewType: "device",
		Empty: "No devices are defined (the system may create br-lan on its own).",
		Cols: []col{
			{"NAME", 12, func(s uci.Section) string { return orDash(opt(s, "name")) }},
			{"TYPE", 8, func(s uci.Section) string { return orDash(opt(s, "type")) }},
			{"PORTS", 30, func(s uci.Section) string { return orDash(joinOpt(s, "ports")) }},
			{"VLAN FILTER", 11, func(s uci.Section) string {
				if opt(s, "vlan_filtering") == "1" {
					return "on"
				}
				return ""
			}},
		},
		ListKeys: []string{"ports"},
		CanEdit: func(s uci.Section) string {
			if opt(s, "type") != "bridge" {
				return "only bridges are edited here; other device types are on the Config screen"
			}
			return ""
		},
		Fields: func(ed *secEditor, s *uci.Section) []*field {
			var f []*field
			if s == nil {
				f = append(f, req(&field{Label: "Name", Key: "name", Hint: "br-something", Validate: func(v string) error {
					if !regexp.MustCompile(`^br-[a-z0-9_-]{1,11}$`).MatchString(v) {
						return fmt.Errorf("use br- followed by letters, digits, '-' or '_'")
					}
					for _, b := range ed.bridges() {
						if b == v {
							return fmt.Errorf("%s already exists", v)
						}
					}
					return nil
				}}), &field{Label: "Type", Key: "type", Kind: kChoice, Choices: []string{"bridge"}, Value: "bridge"})
			}
			f = append(f,
				textf("Ports", "ports", s, "physical ports, separated by spaces (e.g. lan1 lan2)", validDevList),
				boolf("VLAN filtering", "vlan_filtering", s, "needed for VLANs on a DSA switch"),
				boolf("STP", "stp", s, "spanning tree; on only if you loop bridges"),
			)
			return f
		},
		Cross: func(ed *secEditor, f *form) {
			self := ""
			if ed.edit != nil {
				self = opt(*ed.edit, "name")
			} else {
				self = fieldBy(f, "name").Value
			}
			mine := map[string]bool{}
			for _, p := range strings.Fields(fieldBy(f, "ports").Value) {
				mine[p] = true
			}
			for _, x := range ed.config().Sections {
				if x.Type == "device" && opt(x, "type") == "bridge" && opt(x, "name") != self {
					for _, p := range strings.Fields(joinOpt(x, "ports")) {
						if mine[p] {
							fieldBy(f, "ports").err = fmt.Sprintf("%s is already in %s", p, opt(x, "name"))
						}
					}
				}
			}
		},
		NewName: func(ed *secEditor, f *form) string {
			return ed.uniqueName("dev", strings.TrimPrefix(fieldBy(f, "name").Value, "br-"))
		},
		Delete: func(ed *secEditor, s uci.Section) ([]changes.Change, []string, error) {
			if opt(s, "type") != "bridge" {
				return nil, nil, fmt.Errorf("only bridges are deleted here")
			}
			name := opt(s, "name")
			var users []string
			var out []changes.Change
			for _, x := range ed.config().Sections {
				switch {
				case x.Type == "interface" && (opt(x, "device") == name || strings.HasPrefix(opt(x, "device"), name+".")):
					users = append(users, "interface "+x.Name)
				case x.Type == "bridge-vlan" && opt(x, "device") == name:
					out = append(out, changes.Change{Op: changes.OpDelete, Config: "network", Section: x.Ref(), Old: "VLAN " + opt(x, "vlan") + " on " + name})
				}
			}
			if len(users) > 0 {
				return nil, nil, fmt.Errorf("%s is used by %s; change those first", name, strings.Join(users, ", "))
			}
			return append(out, changes.Change{Op: changes.OpDelete, Config: "network", Section: s.Ref(), Old: "bridge " + name}), nil, nil
		},
	}
}

func vlanSpec() secSpec {
	return secSpec{
		ID: "vlans", Title: "VLANs", Cfg: "network", Types: []string{"bridge-vlan"}, NewType: "bridge-vlan",
		Empty: "No bridge VLANs (DSA switches only).",
		Cols: []col{
			{"BRIDGE", 10, func(s uci.Section) string { return orDash(opt(s, "device")) }},
			{"VLAN", 5, func(s uci.Section) string { return orDash(opt(s, "vlan")) }},
			{"PORTS", 40, func(s uci.Section) string { return orDash(joinOpt(s, "ports")) }},
		},
		ListKeys: []string{"ports"},
		Pre: func(ed *secEditor) string {
			if len(ed.bridges()) == 0 {
				return "define a bridge first (Bridges tab)"
			}
			return ""
		},
		Fields: func(ed *secEditor, s *uci.Section) []*field {
			dev := ""
			if s != nil {
				dev = opt(*s, "device")
			} else {
				dev = pick(ed.bridges(), "br-lan")
			}
			return []*field{
				choice("Bridge", "device", ed.bridges(), dev, "", ""),
				req(textf("VLAN ID", "vlan", s, "1 to 4094", validate.IntRange(1, 4094))),
				textf("Ports", "ports", s, "port or port:flags, e.g. lan1:u* lan2:t  (u untagged, t tagged, * PVID)", validVlanPorts),
			}
		},
		Cross: func(ed *secEditor, f *form) {
			for _, x := range ed.config().Sections {
				if x.Type == "bridge-vlan" && opt(x, "device") == fieldBy(f, "device").Value && opt(x, "vlan") == fieldBy(f, "vlan").Value && (ed.edit == nil || x.Ref() != ed.edit.Ref()) {
					fieldBy(f, "vlan").err = "this VLAN already exists on that bridge"
				}
			}
		},
		NewName: func(ed *secEditor, f *form) string {
			return ed.uniqueName("vlan", fieldBy(f, "device").Value+"_"+fieldBy(f, "vlan").Value)
		},
	}
}
