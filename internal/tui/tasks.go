package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/qr"
	"github.com/madushakv/owrt-tui/internal/tasks"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

type taskItem struct{ key, title, desc string }

var taskList = []taskItem{
	{"guest", "Guest / free Wi-Fi network", "A separate SSID and subnet that reaches the internet only: no access to your LAN or to the router's admin."},
	{"forward", "Port forward", "Open one port from the internet to a device inside (warns about risky ports)."},
	{"dns", "Upstream DNS", "Choose the DNS servers the router uses (Cloudflare, Quad9, Google, AdGuard)."},
	{"wgserver", "WireGuard server", "Let phones and laptops connect to your network from outside (generates the server key here)."},
	{"wgpeer", "WireGuard: add a device (QR code)", "Create a device's keys, register it on the server and show its config as a QR code to scan."},
	{"wgclient", "WireGuard client (VPN provider)", "Connect the router to a VPN provider or another WireGuard server."},
	{"wifisched", "Wi-Fi schedule", "Switch a radio off at night and on in the morning (runtime only; nothing written to flash)."},
	{"reboot", "Scheduled reboot", "Reboot at a set time, every day or weekly."},
	{"sim", "SIM / cellular (domains, devices or failover)", "Set up a modem interface and choose what uses it: only listed domains, only listed devices, or as a backup when the main link fails."},
	{"crontpl", "Cron templates (watchdogs and housekeeping)", "Ready-made jobs: reconnect a link when it fails, restart WireGuard when it goes stale, restart a service or Wi-Fi on a schedule."},
	{"cronjobs", "Cron jobs", "See, switch off and remove scheduled jobs, or add your own."},
	{"backup", "Back up the configuration to this PC", "Downloads a restorable archive. It contains passwords and keys, so it is stored privately."},
	{"upgrade", "Safe package upgrade", "Checks flash space, takes a backup, upgrades, then checks the router is still healthy."},
	{"firmware", "Firmware image check", "Ask the router to validate a firmware image you uploaded, without flashing it."},
}

type tasksScreen struct {
	e       *env
	cur     int
	cfgs    []uci.Config
	form    *form
	running string
	err     error

	info     []string
	qrc      *qr.Code
	saveConf string // client config text the user can save with w
	saveName string
	after    func() tea.Cmd
	note     []string // why a wizard could not open, shown instead of nothing
}

func newTasks(e *env) *tasksScreen { return &tasksScreen{e: e} }

func (t *tasksScreen) Title() string   { return "Tasks" }
func (t *tasksScreen) Capturing() bool { return t.form != nil || t.info != nil }
func (t *tasksScreen) Keys() string {
	if t.info != nil {
		if t.saveConf != "" {
			return "w save this config as a file  any other key to continue"
		}
		return "any key to continue"
	}
	if t.form != nil {
		return "enter edit  s stage/run  esc cancel"
	}
	return "enter start the selected task"
}

func (t *tasksScreen) Init() tea.Cmd { return nil }

type tasksCfgMsg struct {
	key  string
	cfgs []uci.Config
	err  error
}

func (t *tasksScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case tasksCfgMsg:
		if m.err != nil {
			return t, toastErr(m.err)
		}
		t.cfgs = m.cfgs
		t.note = nil
		t.open(m.key)
		if t.form == nil && t.note != nil {
			t.showInfo(infoMsg{lines: t.note})
			t.note = nil
		}
	case infoMsg:
		t.showInfo(m)
	case tea.KeyPressMsg:
		if t.info != nil {
			return t.infoKey(m.String())
		}
		if t.form != nil {
			return t.formKey(m)
		}
		switch m.String() {
		case "up", "k":
			t.cur = clampInt(t.cur-1, 0, len(taskList)-1)
		case "down", "j":
			t.cur = clampInt(t.cur+1, 0, len(taskList)-1)
		case "enter":
			if t.e.S.ReadOnly {
				return t, toast("read-only mode: start with -write to use tasks")
			}
			key := taskList[t.cur].key
			switch key {
			case "backup", "upgrade":
				return t, t.runAction(key)
			case "cronjobs":
				return t, func() tea.Msg { return gotoMsg{"Cron"} }
			}
			return t, t.e.call(func(ctx context.Context) tea.Msg {
				c, err := t.e.loadUCI(ctx)
				return tasksCfgMsg{key, c, err}
			})
		}
	}
	return t, nil
}

func (t *tasksScreen) radios() []string {
	var out []string
	for _, c := range t.cfgs {
		if c.Name == "wireless" {
			for _, s := range c.Sections {
				if s.Type == "wifi-device" {
					out = append(out, s.Ref())
				}
			}
		}
	}
	return out
}

// freeOctet is the first 192.168.N.0/24 (N from 2) no interface uses.
func (t *tasksScreen) freeOctet() int {
	used := map[int]bool{}
	for _, c := range t.cfgs {
		if c.Name != "network" {
			continue
		}
		for _, s := range c.Sections {
			o, _ := s.Option("ipaddr")
			p := strings.Split(o.Value(), ".")
			if len(p) == 4 && p[0] == "192" && p[1] == "168" {
				n, _ := strconv.Atoi(p[2])
				used[n] = true
			}
		}
	}
	for n := 2; n < 255; n++ {
		if !used[n] {
			return n
		}
	}
	return 99
}

func (t *tasksScreen) open(key string) {
	t.running = key
	if t.openMore(key) {
		return
	}
	switch key {
	case "guest":
		radios := t.radios()
		if len(radios) == 0 {
			t.form, t.running = nil, ""
			t.note = []string{"No Wi-Fi radio was found in /etc/config/wireless, so there is nothing to host a guest SSID.", "Add a radio first (Wi-Fi screen), then try again."}
			return
		}
		t.form = newForm("Guest network",
			&field{Label: "Name", Key: "name", Value: "guest", Required: true, Hint: "lowercase letters and digits; used for the interface and firewall zone"},
			&field{Label: "SSID", Key: "ssid", Value: "Guest", Required: true, Validate: validate.SSID},
			&field{Label: "Password", Key: "key", Secret: true, Required: true, Validate: validate.WPAKey, Hint: "8 to 63 characters"},
			&field{Label: "Subnet 192.168.N.0/24, N =", Key: "octet", Value: strconv.Itoa(t.freeOctet()), Required: true, Validate: validate.IntRange(1, 254), Hint: "pre-filled with the first unused one"},
			&field{Label: "Radio", Key: "radio", Kind: kChoice, Choices: radios, Value: radios[0], Hint: "2.4 GHz radios work for most guest devices"},
			&field{Label: "Isolate clients", Key: "isolate", Kind: kBool, Value: "1", Hint: "guests cannot see each other"},
		)
	case "forward":
		t.form = newForm("Port forward",
			&field{Label: "Name", Key: "name", Required: true, Hint: "e.g. plex"},
			&field{Label: "Protocol", Key: "proto", Kind: kChoice, Choices: []string{"tcp", "udp", "tcp udp"}, Value: "tcp"},
			&field{Label: "External port", Key: "ext", Required: true, Validate: validate.Port, Hint: "the port on the internet side"},
			&field{Label: "Inside address", Key: "ip", Required: true, Validate: validate.IPv4, Hint: "give that device a static lease first"},
			&field{Label: "Inside port", Key: "int", Validate: validate.Port, Hint: "empty = same as external"},
		)
	case "dns":
		var names []string
		for _, p := range tasks.DNSPresets {
			names = append(names, p.Name)
		}
		cur := strings.Join(tasks.CurrentDNS(t.cfgs), ", ")
		if cur == "" {
			cur = "provider default"
		}
		t.form = newForm("Upstream DNS (now: "+cur+")",
			&field{Label: "Provider", Key: "p", Kind: kChoice, Choices: names, Value: names[0]})
	}
}

func val(f *form, key string) string {
	for _, fl := range f.fields {
		if fl.Key == key {
			return fl.Value
		}
	}
	return ""
}

func (t *tasksScreen) formKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	if t.form.done || t.form.cancelled { // stale form from a previous run
		t.form = nil
		return t, nil
	}
	cmd := t.form.Update(m)
	switch {
	case t.form.cancelled:
		t.form = nil
	case t.form.done:
		f, key := t.form, t.running
		t.form, t.running = nil, ""
		return t, t.build(f, key)
	}
	return t, cmd
}

func (t *tasksScreen) build(f *form, key string) tea.Cmd {
	var ch []changes.Change
	var warn []string
	var err error
	if cmd, ok := t.buildMore(f, key); ok {
		return cmd
	}
	switch key {
	case "guest":
		oct, _ := strconv.Atoi(val(f, "octet"))
		ch, err = tasks.GuestNetwork(tasks.Guest{Name: val(f, "name"), SSID: val(f, "ssid"), Key: val(f, "key"), Octet: oct, Radio: val(f, "radio"), Isolate: val(f, "isolate") != ""}, t.cfgs)
	case "forward":
		ch, warn, err = tasks.PortForward(tasks.Forward{Name: val(f, "name"), Proto: val(f, "proto"), ExtPort: val(f, "ext"), IP: val(f, "ip"), IntPort: val(f, "int")}, t.cfgs)
	case "dns":
		for _, p := range tasks.DNSPresets {
			if p.Name == val(f, "p") {
				ch, warn, err = tasks.UpstreamDNS(p.Servers, t.cfgs)
			}
		}
	}
	if err != nil {
		return toastErr(err)
	}
	for _, c := range ch {
		if err := t.e.Staged.Add(c); err != nil {
			return toastErr(err)
		}
	}
	msg := fmt.Sprintf("staged %d changes; review and apply", len(ch))
	if len(warn) > 0 {
		msg += ". Note: " + strings.Join(warn, "; ")
	}
	return tea.Batch(toast(msg), func() tea.Msg { return gotoMsg{"Changes"} })
}

func (t *tasksScreen) View(w, h int) string {
	if t.info != nil {
		return t.infoView(w, h)
	}
	if t.form != nil {
		return t.form.View(w, h)
	}
	var b strings.Builder
	for _, l := range wrap("Guided jobs. Each one stages ordinary changes you can review, then apply with rollback.", w-4) {
		b.WriteString("  " + sDim.Render(l) + "\n")
	}
	b.WriteString("\n")
	for i, it := range taskList {
		line := "  " + it.title
		if i == t.cur {
			b.WriteString(sSel.Render(clip(line, w)) + "\n")
			for _, l := range wrap(it.desc, w-6) {
				b.WriteString("    " + sDim.Render(l) + "\n")
			}
			continue
		}
		b.WriteString(clip(line, w) + "\n")
	}
	return b.String()
}
