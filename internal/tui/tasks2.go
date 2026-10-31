package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/backup"
	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/cron"
	"github.com/madushakv/owrt-tui/internal/discover"
	"github.com/madushakv/owrt-tui/internal/postcheck"
	"github.com/madushakv/owrt-tui/internal/qr"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
	"github.com/madushakv/owrt-tui/internal/wg"
)

// infoMsg opens the information view of the Tasks screen.
type infoMsg struct {
	lines    []string
	qrText   string // the text to encode as a QR code, if any
	saveConf string // a config the user may save as a file
	saveName string
	after    func() tea.Cmd
}

func (t *tasksScreen) showInfo(m infoMsg) {
	t.info, t.after, t.saveConf, t.saveName, t.qrc = m.lines, m.after, m.saveConf, m.saveName, nil
	if len(t.info) == 0 {
		t.info = []string{""}
	}
	if m.qrText != "" {
		if c, err := qr.Encode([]byte(m.qrText), qr.L); err == nil {
			t.qrc = c
		}
	}
}

func (t *tasksScreen) infoKey(k string) (screen, tea.Cmd) {
	if k == "w" && t.saveConf != "" {
		p, err := saveWGConfig(t.e.S.Host(), t.saveName, t.saveConf)
		if err != nil {
			return t, toastErr(err)
		}
		t.saveConf = ""
		return t, toast("saved " + p + " (private: it holds the device's key)")
	}
	after := t.after
	t.info, t.qrc, t.saveConf, t.after = nil, nil, "", nil
	if after != nil {
		return t, after()
	}
	return t, nil
}

func saveWGConfig(router, name, conf string) (string, error) {
	safe := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	dir := filepath.Join(discover.ConfigDir(), "wireguard", strings.Trim(safe.ReplaceAllString(router, "_"), "._"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	p := filepath.Join(dir, safe.ReplaceAllString(name, "_")+".conf")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(conf); err != nil {
		f.Close()
		return "", err
	}
	return p, f.Close()
}

func (t *tasksScreen) infoView(w, h int) string {
	var b strings.Builder
	for _, l := range t.info {
		b.WriteString("  " + clip(l, w-4) + "\n")
	}
	if t.qrc != nil {
		need := len(t.qrc.Modules) + 4
		rows := (need + 1) / 2
		if asciiOn {
			need, rows = 2*need, need
		}
		if w < need || h < rows+len(t.info)+1 {
			b.WriteString("\n  " + sWarn.Render(fmt.Sprintf("The QR code needs a terminal of at least %d columns x %d rows; enlarge it and reopen, or press w to save the config as a file.", need, rows+len(t.info)+1)) + "\n")
		} else {
			b.WriteString("\n")
			txt := t.qrc.Text(2)
			if asciiOn {
				txt = t.qrc.TextWide(2)
			}
			for _, l := range strings.Split(strings.TrimRight(txt, "\n"), "\n") {
				b.WriteString("  " + l + "\n")
			}
		}
	}
	return b.String()
}

// ---- helpers ----

var hhmm = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

func validHHMM(s string) error {
	if !hhmm.MatchString(s) {
		return fmt.Errorf("use HH:MM, e.g. 23:30")
	}
	return nil
}

func splitHHMM(s string) (int, int) {
	p := strings.SplitN(s, ":", 2)
	h, _ := strconv.Atoi(p[0])
	m, _ := strconv.Atoi(p[1])
	return h, m
}

// wgServers lists WireGuard interfaces that listen (servers).
func wgServers(cfgs []uci.Config) []string {
	var out []string
	for _, c := range cfgs {
		if c.Name != "network" {
			continue
		}
		for _, s := range c.Sections {
			if s.Type == "interface" {
				if p, _ := s.Option("proto"); p.Value() == "wireguard" {
					if lp, _ := s.Option("listen_port"); lp.Value() != "" {
						out = append(out, s.Ref())
					}
				}
			}
		}
	}
	return out
}

func ddnsHost(cfgs []uci.Config) string {
	for _, c := range cfgs {
		if c.Name == "ddns" {
			for _, s := range c.Sections {
				if o, ok := s.Option("lookup_host"); ok && o.Value() != "" {
					return o.Value()
				}
			}
		}
	}
	return ""
}

// ---- opening the extra wizards ----

func (t *tasksScreen) openMore(key string) bool {
	switch key {
	case "wgserver":
		t.form = newForm("WireGuard server",
			&field{Label: "Name", Key: "name", Value: "wgserver", Required: true, Hint: "interface and firewall zone name"},
			&field{Label: "Port", Key: "port", Value: "51820", Required: true, Validate: validate.Port, Hint: "UDP port opened on the internet side"},
			&field{Label: "Router address in the VPN", Key: "addr", Value: "10.8.0.1/24", Required: true, Hint: "a private range that nothing else uses"},
		)
	case "wgpeer":
		srv := wgServers(t.cfgs)
		if len(srv) == 0 {
			t.running = ""
			return true
		}
		ep := ddnsHost(t.cfgs)
		if ep != "" {
			ep += ":51820"
		}
		t.form = newForm("Add a WireGuard device",
			&field{Label: "Server", Key: "iface", Kind: kChoice, Choices: srv, Value: srv[0]},
			&field{Label: "Device name", Key: "name", Required: true, Hint: "e.g. phone, laptop"},
			&field{Label: "Router's public address", Key: "endpoint", Value: ep, Required: true, Hint: "host:port the device will connect to (a DDNS name is best)"},
			&field{Label: "Send all traffic via the router", Key: "full", Kind: kBool, Value: "1", Hint: "off = only the VPN and your LAN go through it"},
			&field{Label: "DNS for the device", Key: "dns", Hint: "optional, e.g. the router's VPN address so ad blocking applies"},
		)
	case "wgclient":
		t.form = newForm("WireGuard client",
			&field{Label: "Name", Key: "name", Value: "provider", Required: true},
			&field{Label: "Address on the VPN", Key: "addr", Required: true, Hint: "given by the provider, e.g. 10.2.0.2/32"},
			&field{Label: "Server public key", Key: "peerpub", Required: true, Validate: wgKey},
			&field{Label: "Server endpoint", Key: "endpoint", Required: true, Hint: "host:port"},
			&field{Label: "Pre-shared key", Key: "psk", Secret: true, Validate: wgKey, Hint: "optional"},
			&field{Label: "Your private key", Key: "priv", Secret: true, Validate: wgKey, Hint: "empty = generate a new key pair and show you the public key to register"},
			&field{Label: "Send all LAN traffic through it", Key: "full", Kind: kBool, Value: "1"},
		)
	case "wifisched":
		radios := t.radios()
		if len(radios) == 0 {
			t.running = ""
			return true
		}
		t.form = newForm("Wi-Fi schedule",
			&field{Label: "Radio", Key: "radio", Kind: kChoice, Choices: radios, Value: radios[0], Hint: "switches every SSID on this radio (check which with the Wi-Fi screen)"},
			&field{Label: "Off at", Key: "off", Value: "23:30", Required: true, Validate: validHHMM},
			&field{Label: "On at", Key: "on", Value: "06:30", Required: true, Validate: validHHMM},
		)
	case "reboot":
		t.form = newForm("Scheduled reboot",
			&field{Label: "At", Key: "at", Value: "04:00", Required: true, Validate: validHHMM},
			&field{Label: "On", Key: "dow", Kind: kChoice, Choices: []string{"every day", "Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}, Value: "every day"},
		)
	case "firmware":
		t.form = newForm("Firmware image check",
			&field{Label: "Image path on the router", Key: "path", Required: true, Hint: "e.g. /tmp/openwrt-sysupgrade.bin (upload it first with scp)"},
		)
	default:
		return false
	}
	return true
}

func wgKey(s string) error { return wg.CheckKey(s) }

// ---- running the extra wizards ----

func (t *tasksScreen) stageAll(ch []changes.Change) error {
	for _, c := range ch {
		if err := t.e.Staged.Add(c); err != nil {
			return err
		}
	}
	return nil
}

func gotoChanges() tea.Msg { return gotoMsg{"Changes"} }

func (t *tasksScreen) buildMore(f *form, key string) (tea.Cmd, bool) {
	switch key {
	case "wgserver":
		port, _ := strconv.Atoi(val(f, "port"))
		ch, pub, err := wg.NewServer(wg.Server{Name: val(f, "name"), Port: port, Address: val(f, "addr")}, t.cfgs)
		if err != nil {
			return toastErr(err), true
		}
		if err := t.stageAll(ch); err != nil {
			return toastErr(err), true
		}
		lines := []string{
			fmt.Sprintf("WireGuard server staged (%d changes). Review and apply it next.", len(ch)),
			"",
			"Server public key: " + pub,
			"The server's private key was generated here and goes straight into the staged changes (masked on screen).",
			"",
			"After applying: open Tasks > 'WireGuard: add a device' to create a phone or laptop and scan its QR code.",
			"Make sure UDP port " + val(f, "port") + " reaches the router (it is not behind another NAT without a forward).",
		}
		return func() tea.Msg { return infoMsg{lines: lines, after: func() tea.Cmd { return gotoChanges }} }, true
	case "wgpeer":
		ch, conf, ip, err := wg.AddPeer(wg.Peer{Iface: val(f, "iface"), Name: val(f, "name"), Endpoint: val(f, "endpoint"), FullTun: val(f, "full") != "", DNS: val(f, "dns")}, t.cfgs)
		if err != nil {
			return toastErr(err), true
		}
		if err := t.stageAll(ch); err != nil {
			return toastErr(err), true
		}
		lines := []string{
			fmt.Sprintf("Device %q staged with address %s. Apply the changes, THEN scan this on the device.", val(f, "name"), ip),
			"This config holds the device's private key. It is shown once and not stored unless you press w.",
		}
		return func() tea.Msg {
			return infoMsg{lines: lines, qrText: conf, saveConf: conf, saveName: val(f, "iface") + "-" + val(f, "name"), after: func() tea.Cmd { return gotoChanges }}
		}, true
	case "wgclient":
		ch, pub, warn, err := wg.NewClient(wg.Client{Name: val(f, "name"), Address: val(f, "addr"), PeerPub: val(f, "peerpub"), Endpoint: val(f, "endpoint"), PSK: val(f, "psk"), PrivateKey: val(f, "priv"), FullTun: val(f, "full") != ""}, t.cfgs)
		if err != nil {
			return toastErr(err), true
		}
		if err := t.stageAll(ch); err != nil {
			return toastErr(err), true
		}
		lines := []string{
			fmt.Sprintf("WireGuard client staged (%d changes). Review and apply it next.", len(ch)),
			"",
			"This router's public key (give it to the provider / other server): " + pub,
		}
		for _, w := range warn {
			lines = append(lines, "", "Note: "+w)
		}
		return func() tea.Msg { return infoMsg{lines: lines, after: func() tea.Cmd { return gotoChanges }} }, true
	case "wifisched":
		oh, om := splitHHMM(val(f, "off"))
		nh, nm := splitHHMM(val(f, "on"))
		jobs, err := cron.WifiSchedule(val(f, "radio"), oh, om, nh, nm)
		if err != nil {
			return toastErr(err), true
		}
		return t.cronAdd(jobs[:], fmt.Sprintf("Switch %s off at %s and on at %s every day?", val(f, "radio"), val(f, "off"), val(f, "on"))), true
	case "reboot":
		h, m := splitHHMM(val(f, "at"))
		dows := map[string]string{"every day": "*", "Sunday": "0", "Monday": "1", "Tuesday": "2", "Wednesday": "3", "Thursday": "4", "Friday": "5", "Saturday": "6"}
		s, c, tag, err := cron.Reboot(h, m, dows[val(f, "dow")])
		if err != nil {
			return toastErr(err), true
		}
		return t.cronAdd([][3]string{{s, c, tag}}, fmt.Sprintf("Reboot the router at %s (%s)?", val(f, "at"), val(f, "dow"))), true
	case "firmware":
		path := val(f, "path")
		return t.e.call(func(ctx context.Context) tea.Msg {
			out, err := t.e.S.FirmwareTest(ctx, path)
			lines := []string{"sysupgrade -T " + path, ""}
			lines = append(lines, strings.Split(out, "\n")...)
			if err != nil {
				lines = append(lines, "", "RESULT: "+err.Error()+". Do not flash this image.")
			} else {
				lines = append(lines, "", "RESULT: the image is valid for this router.")
			}
			lines = append(lines, "", "owrt-tui does not flash firmware: a failed flash cannot be rolled back remotely.",
				"Take a backup first (Tasks > Back up the configuration), then flash from the router with: sysupgrade -v "+path)
			return infoMsg{lines: lines}
		}), true
	}
	return nil, false
}

// cronAdd asks, then adds the jobs (each as [schedule, command, tag]) to the crontab.
func (t *tasksScreen) cronAdd(jobs [][3]string, prompt string) tea.Cmd {
	return t.e.call(func(ctx context.Context) tea.Msg {
		text, err := t.e.S.Crontab(ctx)
		if err != nil {
			return clientActionMsg{"", err}
		}
		for _, j := range jobs {
			if text, err = cron.Add(text, j[0], j[1], j[2]); err != nil {
				return clientActionMsg{"", err}
			}
		}
		for _, ex := range cron.Parse(text) { // another reboot job already there?
			if ex.Tag == "" && strings.Contains(ex.Command, "reboot") && len(jobs) == 1 && jobs[0][2] == "reboot" {
				return clientActionMsg{"", fmt.Errorf("a reboot job already exists (%s %s); remove it on the Cron screen first", ex.Schedule, ex.Command)}
			}
		}
		return askMsg{Prompt: prompt, Yes: func() tea.Cmd { return t.e.writeCron(text, "schedule saved") }}
	})
}

// writeCron installs a new crontab.
func (e *env) writeCron(text, done string) tea.Cmd {
	return e.call(func(ctx context.Context) tea.Msg {
		if err := e.S.SetCrontab(ctx, text); err != nil {
			return clientActionMsg{"", err}
		}
		return cronSavedMsg{done}
	})
}

type cronSavedMsg struct{ what string }

// ---- backup and upgrade ----

func (t *tasksScreen) runAction(key string) tea.Cmd {
	switch key {
	case "backup":
		return t.e.callFor(2*time.Minute, func(ctx context.Context) tea.Msg {
			p, err := t.takeBackup(ctx)
			if err != nil {
				return clientActionMsg{"", err}
			}
			return infoMsg{lines: []string{"Backup saved:", p, "", "It holds Wi-Fi passwords and VPN keys; the file is private (0600).", "Restore on the router with: sysupgrade -r <file>  (or the router's web UI)."}}
		})
	case "upgrade":
		return t.e.callFor(10*time.Minute, func(ctx context.Context) tea.Msg {
			if _, err := t.e.S.PkgUpdate(ctx); err != nil {
				return clientActionMsg{"", fmt.Errorf("package index update failed: %w", err)}
			}
			up, err := t.e.S.PkgUpgradable(ctx)
			if err != nil {
				return clientActionMsg{"", err}
			}
			if len(up) == 0 {
				return infoMsg{lines: []string{"Everything is up to date."}}
			}
			facts, _ := t.e.S.HealthFacts(ctx)
			lines := []string{fmt.Sprintf("%d package(s) can be upgraded:", len(up))}
			for i, p := range up {
				if i >= 12 {
					lines = append(lines, fmt.Sprintf("  ... and %d more", len(up)-i))
					break
				}
				lines = append(lines, "  "+p.Name+" "+p.Version)
			}
			if facts.OverlayFreeKB > 0 && facts.OverlayFreeKB < 3072 {
				lines = append(lines, "", fmt.Sprintf("Only %d KB of flash is free: an upgrade could fill it. Free some space first.", facts.OverlayFreeKB))
				return infoMsg{lines: lines}
			}
			return askMsg{Prompt: fmt.Sprintf("Upgrade %d packages? A backup is taken first and health is checked afterwards.", len(up)), Danger: false, Yes: func() tea.Cmd {
				return t.e.callFor(15*time.Minute, func(ctx context.Context) tea.Msg { return t.doUpgrade(ctx, len(up)) })
			}}
		})
	}
	return nil
}

func (t *tasksScreen) takeBackup(ctx context.Context) (string, error) {
	data, err := t.e.S.ConfigBackup(ctx)
	if err != nil {
		return "", err
	}
	return backup.Save(discover.ConfigDir(), t.e.S.Host(), data, time.Now())
}

func (t *tasksScreen) doUpgrade(ctx context.Context, n int) tea.Msg {
	p, err := t.takeBackup(ctx)
	if err != nil {
		return clientActionMsg{"", fmt.Errorf("no upgrade done: the backup failed: %w", err)}
	}
	before, _ := t.e.S.PostCheck(ctx)
	out, err := t.e.S.PkgUpgradeAll(ctx)
	lines := []string{"Backup: " + p}
	if err != nil {
		lines = append(lines, "", "UPGRADE FAILED: "+err.Error(), "apk upgrades are all-or-nothing; the previous packages should still be in place.")
		return infoMsg{lines: lines}
	}
	lines = append(lines, "", fmt.Sprintf("Upgraded %d packages.", n))
	time.Sleep(5 * time.Second)
	after, err := t.e.S.PostCheck(ctx)
	switch {
	case err != nil:
		lines = append(lines, "", "Could not run the health check afterwards: "+err.Error())
	case before != nil && len(postcheck.Regressions(before, after)) > 0:
		lines = append(lines, "", "HEALTH REGRESSED after the upgrade:")
		for _, r := range postcheck.Regressions(before, after) {
			lines = append(lines, "  "+r)
		}
		lines = append(lines, "Check the Logs screen and the Services screen; some services need a restart or a reboot after an upgrade.")
	default:
		lines = append(lines, "", "Health check after the upgrade: nothing regressed.")
	}
	if strings.Contains(out, "kernel") || strings.Contains(out, "kmod-") {
		lines = append(lines, "", "Kernel modules were upgraded: reboot the router to load them.")
	}
	return infoMsg{lines: lines}
}

func qrEncode(s string) (*qr.Code, error) { return qr.Encode([]byte(s), qr.L) }
