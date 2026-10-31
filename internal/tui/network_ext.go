package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/netedit"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

func (n *netScreen) allCfgs() []uci.Config { return n.e.peekUCI() }

func (n *netScreen) zoneNames() []string {
	var out []string
	for _, c := range n.allCfgs() {
		if c.Name == "firewall" {
			for _, s := range c.Sections {
				if s.Type == "zone" {
					out = append(out, opt(s, "name"))
				}
			}
		}
	}
	return out
}

func (n *netScreen) openNew() (screen, tea.Cmd) {
	if n.e.S.ReadOnly {
		return n, toast("read-only mode: start with -write to stage edits")
	}
	if n.allCfgs() == nil {
		return n, toast("the configuration is still loading")
	}
	zones := append([]string{"(none)"}, n.zoneNames()...)
	n.adding = true
	n.edit = uci.Section{}
	n.form = newForm("New interface",
		&field{Label: "Name", Key: "name", Required: true, Hint: "lowercase letters, digits, '_'  (e.g. iot)"},
		&field{Label: "Protocol", Key: "proto", Kind: kChoice, Choices: netedit.Protos, Value: "static"},
		&field{Label: "Device", Key: "device", Hint: "a bridge or port, e.g. br-iot, wan, br-lan.10 (empty = none yet)"},
		&field{Label: "Address", Key: "addr", Hint: "static only, CIDR e.g. 192.168.40.1/24", Validate: validate.CIDR4},
		&field{Label: "Gateway", Key: "gw", Hint: "static uplinks only", Validate: validate.IPv4},
		&field{Label: "DNS servers", Key: "dns", Hint: "separated by spaces", Validate: func(v string) error {
			for _, a := range strings.Fields(v) {
				if err := validate.IP(a); err != nil {
					return fmt.Errorf("%q: %w", a, err)
				}
			}
			return nil
		}},
		&field{Label: "PPPoE username", Key: "user"},
		&field{Label: "PPPoE password", Key: "pass", Secret: true},
		&field{Label: "Firewall zone", Key: "zone", Kind: kChoice, Choices: zones, Value: "(none)", Hint: "which zone the interface belongs to"},
		&field{Label: "Serve DHCP", Key: "dhcp", Kind: kBool, Hint: "static only: start an address pool on it"},
	)
	return n, nil
}

func (n *netScreen) stageNew(f *form) tea.Cmd {
	zone := val(f, "zone")
	if zone == "(none)" {
		zone = ""
	}
	ch, err := netedit.NewInterface(netedit.Interface{
		Name: val(f, "name"), Proto: val(f, "proto"), Device: val(f, "device"), Address: val(f, "addr"),
		Gateway: val(f, "gw"), DNS: strings.Fields(val(f, "dns")), Username: val(f, "user"), Password: val(f, "pass"),
		Zone: zone, DHCP: val(f, "dhcp") != "",
	}, n.allCfgs())
	if err != nil {
		return toastErr(err)
	}
	for _, c := range ch {
		if err := n.e.Staged.Add(c); err != nil {
			return toastErr(err)
		}
	}
	return toast(fmt.Sprintf("interface staged (%d changes); review and apply on the Changes screen", len(ch)))
}

func (n *netScreen) askDelete(s uci.Section) (screen, tea.Cmd) {
	if s.Type != "interface" {
		return n, toast("delete bridges on the Bridges tab")
	}
	if n.e.S.ReadOnly {
		return n, toast("read-only mode: start with -write to stage edits")
	}
	ch, warn, err := netedit.DeleteInterface(s.Name, n.allCfgs())
	if err != nil {
		return n, toastErr(err)
	}
	prompt := fmt.Sprintf("Delete interface %s? (%d related changes: firewall zone, DHCP pool, routes)", s.Name, len(ch)-1)
	return n, func() tea.Msg {
		return askMsg{Prompt: prompt, Danger: true, Yes: func() tea.Cmd {
			for _, c := range ch {
				if err := n.e.Staged.Add(c); err != nil {
					return toastErr(err)
				}
			}
			msg := "delete staged; review and apply on the Changes screen"
			if len(warn) > 0 {
				msg += ". Note: " + strings.Join(warn, "; ")
			}
			return toast(msg)
		}}
	}
}
