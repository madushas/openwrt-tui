package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/metrics"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

type netScreen struct {
	e       *env
	cfg     uci.Config
	live    map[string]metrics.Iface
	cur     int
	loading bool
	err     error
	form    *form
	edit    uci.Section
}

func newNetwork(e *env) *netScreen { return &netScreen{e: e, loading: true} }

func (n *netScreen) Title() string   { return "Network" }
func (n *netScreen) Capturing() bool { return n.form != nil }
func (n *netScreen) Keys() string {
	if n.form != nil {
		return "enter edit  s stage  esc cancel"
	}
	return "enter edit (static and DHCP)  g reload"
}

type netMsg struct {
	cfg  uci.Config
	live map[string]metrics.Iface
	err  error
}

func (n *netScreen) Init() tea.Cmd { return n.load() }

func (n *netScreen) load() tea.Cmd {
	return n.e.call(func(ctx context.Context) tea.Msg {
		all, err := n.e.loadUCI(ctx)
		if err != nil {
			return netMsg{err: err}
		}
		m := netMsg{live: map[string]metrics.Iface{}}
		for _, c := range all {
			if c.Name == "network" {
				m.cfg = c
			}
		}
		if s, err := n.e.S.Sample(ctx); err == nil { // live state is a nicety
			for _, i := range s.Ifaces {
				m.live[i.Name] = i
			}
		}
		return m
	})
}

func (n *netScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case netMsg:
		n.loading, n.err = false, m.err
		if m.err == nil {
			n.cfg, n.live = m.cfg, m.live
			n.cur = clampInt(n.cur, 0, len(n.rows())-1)
		}
	case uciChangedMsg:
		return n, n.load()
	case tea.KeyPressMsg:
		if n.form != nil {
			return n.formKey(m)
		}
		return n.key(m)
	}
	return n, nil
}

// rows are the logical interfaces, then the bridges and other devices.
func (n *netScreen) rows() []uci.Section {
	var ifs, devs []uci.Section
	for _, s := range n.cfg.Sections {
		switch s.Type {
		case "interface":
			ifs = append(ifs, s)
		case "device":
			devs = append(devs, s)
		}
	}
	return append(ifs, devs...)
}

func (n *netScreen) key(m tea.KeyPressMsg) (screen, tea.Cmd) {
	rows := n.rows()
	switch m.String() {
	case "up", "k":
		n.cur = clampInt(n.cur-1, 0, len(rows)-1)
	case "down", "j":
		n.cur = clampInt(n.cur+1, 0, len(rows)-1)
	case "g":
		n.e.invalidate()
		return n, n.load()
	case "enter", "e":
		if n.cur >= 0 && n.cur < len(rows) {
			return n.open(rows[n.cur])
		}
	}
	return n, nil
}

func (n *netScreen) open(s uci.Section) (screen, tea.Cmd) {
	if s.Type != "interface" {
		return n, toast("bridges and devices are edited on the Config screen for now")
	}
	proto := opt(s, "proto")
	if proto != "static" && proto != "dhcp" {
		return n, toast(fmt.Sprintf("protocol %q is edited on the Config screen", proto))
	}
	if o, ok := s.Option("ipaddr"); ok && o.IsList && len(o.Values) > 1 {
		return n, toast("this interface has several addresses; edit it on the Config screen")
	}
	if n.e.S.ReadOnly {
		return n, toast("read-only mode: start with -write to stage edits")
	}
	n.edit = s
	mtu := &field{Label: "MTU", Key: "mtu", Value: opt(s, "mtu"), Hint: "leave empty for the default", Validate: validate.IntRange(576, 9000)}
	if proto == "dhcp" {
		n.form = newForm("Interface "+s.Name+" (DHCP client)",
			&field{Label: "Hostname", Key: "hostname", Value: opt(s, "hostname"), Hint: "sent to the upstream DHCP server (optional)", Validate: validate.Hostname},
			&field{Label: "Metric", Key: "metric", Value: opt(s, "metric"), Hint: "lower wins when several uplinks exist", Validate: validate.IntRange(0, 9999)},
			mtu,
		)
		return n, nil
	}
	cidr := strings.Contains(opt(s, "ipaddr"), "/")
	fields := []*field{{Label: "Address", Key: "ipaddr", Value: opt(s, "ipaddr"), Required: true, Hint: "the router's own address on this network"}}
	if cidr {
		fields[0].Validate = validate.CIDR4
		fields[0].Hint += " (CIDR, e.g. 192.168.1.1/24)"
	} else {
		fields[0].Validate = validate.IPv4
		fields = append(fields, &field{Label: "Netmask", Key: "netmask", Value: opt(s, "netmask"), Required: true, Hint: "e.g. 255.255.255.0", Validate: validate.Netmask})
	}
	fields = append(fields,
		&field{Label: "Gateway", Key: "gateway", Value: opt(s, "gateway"), Hint: "only for an uplink; leave empty on a LAN", Validate: validate.IPv4},
		&field{Label: "DNS servers", Key: "dns", Value: opt(s, "dns"), Hint: "separated by spaces", Validate: func(v string) error {
			for _, a := range strings.Fields(v) {
				if err := validate.IP(a); err != nil {
					return fmt.Errorf("%q: %w", a, err)
				}
			}
			return nil
		}},
		mtu,
	)
	n.form = newForm("Interface "+s.Name+" (static)", fields...)
	return n, nil
}

func (n *netScreen) formKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	cmd := n.form.Update(m)
	switch {
	case n.form.cancelled:
		n.form = nil
	case n.form.done:
		f := n.form
		n.form = nil
		return n, n.stage(f)
	}
	return n, cmd
}

func (n *netScreen) stage(f *form) tea.Cmd {
	list := listify(f.Changes("network", n.edit.Ref()), "dns")
	if len(list) == 0 {
		return toast("nothing changed")
	}
	for _, c := range list {
		if err := n.e.Staged.Add(c); err != nil {
			return toastErr(err)
		}
	}
	return toast("staged; review and apply on the Changes screen (c)")
}

func (n *netScreen) View(w, h int) string {
	if n.form != nil {
		return n.form.View(w, h)
	}
	if n.loading && len(n.cfg.Sections) == 0 {
		return "\n  loading network settings..."
	}
	if n.err != nil {
		return "\n  " + sBad.Render("error: "+n.err.Error())
	}
	rows := n.rows()
	if len(rows) == 0 {
		return "\n  No interfaces found."
	}
	var b strings.Builder
	b.WriteString(sDim.Render(fmt.Sprintf("  %-5s %-12s %-8s %-20s %s", "", "NAME", "PROTO", "ADDRESS", "DEVICE / STATE")) + "\n")
	lo, hi := window(len(rows), n.cur, h-1)
	for i := lo; i < hi; i++ {
		s := rows[i]
		var line string
		if s.Type == "device" {
			line = fmt.Sprintf("  %-5s %-12s %-8s %-20s ports: %s", "DEV", orDash(opt(s, "name")), orDash(opt(s, "type")), "", orDash(strings.Join(optList(s, "ports"), " ")))
		} else {
			addr := opt(s, "ipaddr")
			if m := opt(s, "netmask"); m != "" && !strings.Contains(addr, "/") {
				addr += "/" + maskBits(m)
			}
			state := ""
			if l, ok := n.live[s.Name]; ok {
				state = "down"
				if l.Up {
					state = "up"
				}
				if addr == "" && len(l.Addrs) > 0 {
					addr = l.Addrs[0]
				}
			}
			line = fmt.Sprintf("  %-5s %-12s %-8s %-20s %s %s", "IF", s.Name, orDash(opt(s, "proto")), orDash(addr), orDash(opt(s, "device")), state)
		}
		if _, p := n.e.Staged.Pending("network", s.Ref(), ""); p {
			line += "  (change staged)"
		}
		if i == n.cur {
			b.WriteString(sSel.Render(clip(line, w)) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func optList(s uci.Section, k string) []string {
	o, _ := s.Option(k)
	return o.Values
}

// maskBits turns 255.255.255.0 into "24"; anything else is shown as it is.
func maskBits(m string) string {
	if validate.Netmask(m) != nil {
		return m
	}
	ones := 0
	for _, p := range strings.Split(m, ".") {
		var v int
		fmt.Sscanf(p, "%d", &v)
		for ; v > 0; v = (v << 1) & 0xff {
			ones++
		}
	}
	return fmt.Sprint(ones)
}
