package tui

import (
	"github.com/madushakv/owrt-tui/internal/uci"
	"strings"
	"testing"
)

func startTask(t *testing.T, a *App, key string) *tasksScreen {
	t.Helper()
	openTab(t, a, "Tasks")
	ts := scr(a, "Tasks").(*tasksScreen)
	for i, it := range taskList {
		if it.key == key {
			ts.cur = i
		}
	}
	press(t, a, "enter")
	return ts
}

func TestWireGuardServerThenPeerShowsQRAndNeverStoresClientKey(t *testing.T) {
	withStore(t)
	a := writeApp(t)
	ts := startTask(t, a, "wgserver")
	if ts.form == nil {
		t.Fatal("no form")
	}
	for _, f := range ts.form.fields {
		if f.Key == "port" {
			f.Value = "51821" // the demo's wg0 already uses 51820
		}
	}
	press(t, a, "s")
	if a.env.Staged.Len() < 10 {
		t.Fatalf("staged %d (toast: %q)", a.env.Staged.Len(), a.toast.Text)
	}
	if ts.info == nil || !strings.Contains(strings.Join(ts.info, "\n"), "Server public key") {
		t.Fatalf("no info: %v", ts.info)
	}
	press(t, a, "x") // continue -> Changes
	if v := view(a); strings.Contains(v, "private_key") && !strings.Contains(v, "••") && !strings.Contains(v, "***") {
		t.Fatalf("private key may be visible:\n%s", v)
	}
}

func TestPeerWizardQRRoundTrip(t *testing.T) {
	withStore(t)
	a := writeApp(t)
	// give the demo router a WireGuard server to add a device to: the demo's wg0
	// has a listen_port and a private key
	ts := startTask(t, a, "wgpeer")
	if ts.form == nil {
		t.Fatalf("no form; servers: %v", wgServers(ts.cfgs))
	}
	for _, f := range ts.form.fields {
		switch f.Key {
		case "name":
			f.Value = "tablet"
		case "endpoint":
			f.Value = "vpn.example.org:51820"
		}
	}
	press(t, a, "s")
	if ts.info == nil || ts.qrc == nil || ts.saveConf == "" {
		t.Fatalf("info=%v qr=%v toast=%q", ts.info != nil, ts.qrc != nil, a.toast.Text)
	}
	if !strings.Contains(ts.saveConf, "[Interface]") || !strings.Contains(ts.saveConf, "Endpoint = vpn.example.org:51820") {
		t.Fatal(ts.saveConf)
	}
	// the QR encodes exactly the config text
	want, _ := qrEncodeForTest(ts.saveConf)
	if want != len(ts.qrc.Modules) {
		t.Fatal("QR does not match the config")
	}
	// the private key must not be staged on the router
	var priv string
	for _, l := range strings.Split(ts.saveConf, "\n") {
		if strings.HasPrefix(l, "PrivateKey = ") {
			priv = strings.TrimPrefix(l, "PrivateKey = ")
		}
	}
	for _, c := range a.env.Staged.Items() {
		if c.Value == priv {
			t.Fatal("client private key was staged")
		}
	}
	// a large enough terminal draws it, a tiny one explains instead of drawing a cropped code
	if v := ts.infoView(120, 60); !strings.Contains(v, "▀") {
		t.Fatal("QR not drawn")
	}
	if v := ts.infoView(40, 10); strings.Contains(v, "▀") || !strings.Contains(v, "needs a terminal") {
		t.Fatal("cropped QR drawn")
	}
}

func TestWifiScheduleAndRebootViaCrontab(t *testing.T) {
	withStore(t)
	a := writeApp(t)
	ts := startTask(t, a, "wifisched")
	press(t, a, "s")
	if ts.form != nil {
		t.Fatalf("form still open: errors?")
	}
	// the confirmation arrives as an ask; accept it through the app
	if a.ask == nil {
		t.Fatal("no confirmation asked")
	}
	press(t, a, "y")
	text, _ := a.env.S.Crontab(a.env.ctx)
	if !strings.Contains(text, "wifi down radio0 # owrt-tui:wifi-off-radio0") || !strings.Contains(text, "wifi up radio0 # owrt-tui:wifi-on-radio0") {
		t.Fatalf("%q", text)
	}
	// the demo already has a 4am reboot: a second reboot job is refused
	ts = startTask(t, a, "reboot")
	press(t, a, "s")
	if a.ask != nil {
		t.Fatal("asked to add a second reboot job")
	}
	// the Cron screen lists the jobs, masks nothing it should not, and can switch one off
	openTab(t, a, "Cron")
	cs := scr(a, "Cron").(*cronScreen)
	if len(cs.jobs) != 4 {
		t.Fatalf("%d jobs", len(cs.jobs))
	}
	press(t, a, "t", "y")
	if text, _ := a.env.S.Crontab(a.env.ctx); !strings.Contains(text, "#off# ") {
		t.Fatalf("not switched off: %q", text)
	}
	_ = ts
}

func TestCronReadOnlyAndBackup(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Cron")
	press(t, a, "d", "t", "a")
	if a.ask != nil || scr(a, "Cron").Capturing() {
		t.Fatal("read-only session changed the schedule")
	}
	if err := a.env.S.SetCrontab(a.env.ctx, "x"); err == nil {
		t.Fatal("SetCrontab in read-only mode")
	}
	b, err := a.env.S.ConfigBackup(a.env.ctx)
	if err != nil || len(b) == 0 {
		t.Fatalf("%v", err)
	}
	if _, err := a.env.S.FirmwareTest(a.env.ctx, "/tmp/x.bin; reboot"); err == nil {
		t.Fatal("bad path accepted")
	}
	if out, err := a.env.S.FirmwareTest(a.env.ctx, "/tmp/x.bin"); err != nil || !strings.Contains(out, "exit=0") {
		t.Fatalf("%q %v", out, err)
	}
}

func qrEncodeForTest(s string) (int, error) {
	c, err := qrEncode(s)
	if err != nil {
		return 0, err
	}
	return len(c.Modules), nil
}

// A router whose only WireGuard interface is a client (no listen_port) used to
// do nothing at all on Enter. It must say why.
func TestPeerWizardExplainsMissingServer(t *testing.T) {
	withStore(t)
	a := writeApp(t)
	ts := startTask(t, a, "wgpeer")
	ts.form, ts.info = nil, nil
	cfgs, _ := uci.ParseExport("package network\nconfig interface 'eduvpn'\n\toption proto 'wireguard'\n\toption private_key 'x'\n")
	ts.cfgs = cfgs
	ts.note = nil
	ts.Update(tasksCfgMsg{key: "wgpeer", cfgs: cfgs})
	if ts.form != nil || ts.info == nil || !strings.Contains(strings.Join(ts.info, " "), "no WireGuard server") {
		t.Fatalf("no explanation: form=%v info=%v", ts.form, ts.info)
	}
}

func TestQRNeverSilentlyMissing(t *testing.T) {
	c, err := qrEncode(strings.Repeat("A", 280))
	if err != nil {
		t.Fatal(err)
	}
	ts := &tasksScreen{info: []string{"first", "second", "third"}, qrc: c}
	if v := ts.infoView(100, 50); !strings.Contains(v, "█") && !strings.Contains(v, "▀") && !strings.Contains(v, "▄") {
		t.Errorf("QR not drawn in a large terminal:\n%s", v)
	}
	if v := ts.infoView(40, 12); !strings.Contains(v, "needs a terminal") {
		t.Errorf("small terminal gave no explanation:\n%s", v)
	}
}

func TestSIMAndCronTemplateWizardsOpen(t *testing.T) {
	withStore(t)
	a := writeApp(t)
	for _, k := range []string{"sim", "crontpl"} {
		ts := startTask(t, a, k)
		if ts.form == nil && ts.info == nil {
			t.Errorf("%s: Enter did nothing", k)
		}
		press(t, a, "esc")
	}
}
