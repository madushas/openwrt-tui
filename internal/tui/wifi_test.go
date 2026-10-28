package tui

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

func wifiOf(t *testing.T, a *App) *wifiScreen { return scr(a, "Wi-Fi").(*wifiScreen) }

func selectSSID(t *testing.T, w *wifiScreen, ssid string) {
	t.Helper()
	for i, r := range w.rows() {
		if r.Type == "wifi-iface" && opt(r, "ssid") == ssid {
			w.cur = i
			return
		}
	}
	t.Fatalf("no network %q", ssid)
}

func TestWifiListsRadiosAndNetworks(t *testing.T) {
	a := newApp(t, true)
	press(t, a, tab(a, "Wi-Fi"))
	v := plain(a.render())
	for _, want := range []string{"RADIO", "radio0", "2g", "HT20", "SSID", "HomeNet", "sae-mixed"} {
		if !strings.Contains(v, want) {
			t.Fatalf("Wi-Fi screen missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "demo-wifi-passphrase") {
		t.Fatal("password on the Wi-Fi list")
	}
}

func TestWifiReadOnlyStagesNothing(t *testing.T) {
	a := newApp(t, true)
	press(t, a, tab(a, "Wi-Fi"), "j", "enter", "t")
	if wifiOf(t, a).form != nil || a.env.Staged.Len() != 0 {
		t.Fatal("read-only mode allowed an edit")
	}
}

func TestWifiRenameKeepsPasswordUntouched(t *testing.T) {
	a := newApp(t, false)
	press(t, a, tab(a, "Wi-Fi"))
	w := wifiOf(t, a)
	selectSSID(t, w, "HomeNet")
	press(t, a, "enter")
	if w.form == nil || w.form.fields[2].Value != "" {
		t.Fatalf("the current password was loaded into the form: %+v", w.form)
	}
	w.form.fields[0].Value = "HomeNet-5"
	press(t, a, "s")
	items := a.env.Staged.Items()
	if len(items) != 1 || items[0].Option != "ssid" || items[0].Value != "HomeNet-5" {
		t.Fatalf("staged %+v", items)
	}
}

func TestWifiPasswordNeverShownOrLeaked(t *testing.T) {
	a := newApp(t, false)
	press(t, a, tab(a, "Wi-Fi"))
	w := wifiOf(t, a)
	selectSSID(t, w, "HomeNet")
	press(t, a, "enter", "down", "down", "enter") // password field
	const pw = "SuperSecret-Pass-123"
	typeText(t, a, pw)
	if v := plain(a.render()); strings.Contains(v, pw) {
		t.Fatalf("password echoed while typing:\n%s", v)
	}
	press(t, a, "enter")
	if v := plain(a.render()); strings.Contains(v, pw) {
		t.Fatalf("password shown in the form:\n%s", v)
	}
	press(t, a, "s")
	if a.env.Staged.Len() != 1 {
		t.Fatalf("staged %d, want 1", a.env.Staged.Len())
	}
	c := a.env.Staged.Items()[0]
	if c.Option != "key" || c.Value != pw || c.Old != "" {
		t.Fatalf("unexpected change %+v (old value must stay out of the diff)", c)
	}
	press(t, a, tab(a, "Changes"))
	if v := plain(a.render()); strings.Contains(v, pw) || strings.Contains(v, "demo-wifi-passphrase") {
		t.Fatalf("password leaked on the Changes screen:\n%s", v)
	}
	if strings.Contains(a.env.Staged.Diff(), "demo-wifi") {
		t.Fatal("old password in the diff")
	}
}

func TestWifiRejectsShortPasswordAndMissingKey(t *testing.T) {
	a := newApp(t, false)
	press(t, a, tab(a, "Wi-Fi"))
	w := wifiOf(t, a)
	selectSSID(t, w, "HomeNet")
	press(t, a, "enter")
	w.form.fields[2].Value = "short"
	press(t, a, "s")
	if w.form == nil || w.form.fields[2].err == "" || a.env.Staged.Len() != 0 {
		t.Fatal("a 5 character password was accepted")
	}
	// a network that has no password cannot be switched to WPA without one
	cfgs, _ := uci.ParseExport("package wireless\n\nconfig wifi-device 'radio0'\n\toption band '2g'\n\nconfig wifi-iface 'guest'\n\toption device 'radio0'\n\toption ssid 'Guest'\n\toption encryption 'none'\n")
	w.cfg = cfgs[0]
	w.form = nil
	selectSSID(t, w, "Guest")
	press(t, a, "enter")
	w.form.fields[1].Value = "psk2"
	press(t, a, "s")
	if w.form == nil || !strings.Contains(w.form.fields[2].err, "required") {
		t.Fatalf("encryption without a password accepted: %+v", w.form.fields[2])
	}
	w.form.fields[2].Value = "a-good-password"
	press(t, a, "s")
	if w.form != nil || a.env.Staged.Len() != 2 {
		t.Fatalf("valid change rejected (staged %d)", a.env.Staged.Len())
	}
}

func TestWifiRadioFormValidates(t *testing.T) {
	a := newApp(t, false)
	press(t, a, tab(a, "Wi-Fi"), "enter")
	w := wifiOf(t, a)
	if w.form == nil || !strings.Contains(w.form.Title, "radio0") {
		t.Fatalf("radio form not open: %+v", w.form)
	}
	w.form.fields[0].Value = "99"
	w.form.fields[2].Value = "lk"
	press(t, a, "s")
	if w.form == nil || w.form.fields[0].err == "" || w.form.fields[2].err == "" {
		t.Fatal("bad channel / country accepted")
	}
	w.form.fields[0].Value = "auto"
	w.form.fields[2].Value = "LK"
	press(t, a, "s")
	items := a.env.Staged.Items()
	if w.form != nil || len(items) != 2 {
		t.Fatalf("staged %+v", items)
	}
}

func TestWifiToggleDisable(t *testing.T) {
	a := newApp(t, false)
	press(t, a, tab(a, "Wi-Fi"))
	w := wifiOf(t, a)
	selectSSID(t, w, "HomeNet")
	press(t, a, "t")
	items := a.env.Staged.Items()
	if len(items) != 1 || items[0].Op != changes.OpSet || items[0].Option != "disabled" || items[0].Value != "1" {
		t.Fatalf("staged %+v", items)
	}
}
