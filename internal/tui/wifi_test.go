package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/wificaps"
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
	openTab(t, a, "Wi-Fi")
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
	openTab(t, a, "Wi-Fi")
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
	openTab(t, a, "Wi-Fi")
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
	openTab(t, a, "Changes")
	if v := plain(a.render()); strings.Contains(v, pw) || strings.Contains(v, "demo-wifi-passphrase") {
		t.Fatalf("password leaked on the Changes screen:\n%s", v)
	}
	if strings.Contains(a.env.Staged.Diff(), "demo-wifi") {
		t.Fatal("old password in the diff")
	}
}

func TestWifiRejectsShortPasswordAndMissingKey(t *testing.T) {
	a := newApp(t, false)
	openTab(t, a, "Wi-Fi")
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
	openTab(t, a, "Wi-Fi")
	w := wifiOf(t, a)
	selectSSID(t, w, "HomeNet")
	press(t, a, "t")
	items := a.env.Staged.Items()
	if len(items) != 1 || items[0].Op != changes.OpSet || items[0].Option != "disabled" || items[0].Value != "1" {
		t.Fatalf("staged %+v", items)
	}
}

const realWireless = `package wireless

config wifi-device 'radio0'
	option type 'mac80211'
	option path '1e140000.pcie/pci0000:00/0000:00:01.0/0000:02:00.0'
	option band '2g'
	option channel '1'
	option htmode 'HT20'

config wifi-device 'radio1'
	option type 'mac80211'
	option path '1e140000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0'
	option band '5g'
	option channel '36'
	option htmode 'VHT80'
`

// realCapsApp opens the Wi-Fi screen with the user's real radios loaded.
func realCapsApp(t *testing.T) (*App, *wifiScreen) {
	t.Helper()
	b, err := os.ReadFile("../wificaps/testdata/mt7603-mt7662.txt")
	if err != nil {
		t.Fatal(err)
	}
	cfgs, err := uci.ParseExport(realWireless)
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(t, false)
	w := wifiOf(t, a)
	w.cfg, w.caps = cfgs[0], wificaps.Parse(string(b))
	openTab(t, a, "Wi-Fi")
	return a, w
}

func TestRadioFormOffersOnlyWhatTheChipSupports(t *testing.T) {
	a, w := realCapsApp(t)
	w.cur = 1 // radio1, 5 GHz
	press(t, a, "enter")
	f := w.form
	if got := strings.Join(f.fields[1].Choices, ","); got != "VHT20,VHT40,VHT80" {
		t.Fatalf("5 GHz modes %q: this MT7662 has no 160 MHz", got)
	}
	if !strings.Contains(f.fields[0].Hint, "36-64, 100-144") || !strings.Contains(f.fields[0].Hint, "149-165 need a country") {
		t.Fatalf("channel hint: %q", f.fields[0].Hint)
	}
	if !strings.Contains(f.fields[2].Hint, "not set") {
		t.Fatalf("country hint: %q", f.fields[2].Hint)
	}
	for ch, want := range map[string]string{"149": "locked until a country", "169": "disabled", "11": "no channel", "52": ""} {
		f.fields[0].Value = ch
		f.fields[0].check()
		if (want == "") != (f.fields[0].err == "") || !strings.Contains(f.fields[0].err, want) {
			t.Errorf("channel %s: err %q, want %q", ch, f.fields[0].err, want)
		}
	}
}

func TestRadio24FormHasOnlyHT(t *testing.T) {
	a, w := realCapsApp(t)
	w.cur = 0
	press(t, a, "enter")
	if got := strings.Join(w.form.fields[1].Choices, ","); got != "HT20,HT40" {
		t.Fatalf("2.4 GHz modes %q", got)
	}
	w.form.fields[0].Value = "13"
	w.form.fields[0].check()
	if !strings.Contains(w.form.fields[0].err, "country") {
		t.Fatalf("channel 13 under the world region: %q", w.form.fields[0].err)
	}
}

func TestCompactChannels(t *testing.T) {
	if got := compactChannels([]int{1, 2, 3, 6, 11}, 1); got != "1-3, 6, 11" {
		t.Fatal(got)
	}
	if got := compactChannels([]int{36, 40, 44, 100}, 4); got != "36-44, 100" {
		t.Fatal(got)
	}
}
