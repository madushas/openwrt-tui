package cron

import (
	"strings"
	"testing"
)

const sample = "# keep\nMAILTO=\n0 4 * * * reboot\n*/15 * * * * /usr/bin/ddns_update.sh >> /root/ddns.log 2>&1\n"

func TestParseIgnoresCommentsAndVariables(t *testing.T) {
	js := Parse(sample)
	if len(js) != 2 || js[0].Line != 2 || js[0].Command != "reboot" || js[1].Schedule != "*/15 * * * *" {
		t.Fatalf("%+v", js)
	}
}

func TestAddRemoveToggleKeepOtherLines(t *testing.T) {
	out, err := Add(sample, "30 23 * * *", "wifi down radio0", "wifi-off-radio0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, sample) || !strings.HasSuffix(out, "30 23 * * * wifi down radio0 # owrt-tui:wifi-off-radio0\n") {
		t.Fatalf("%q", out)
	}
	js := Parse(out)
	if js[2].Tag != "wifi-off-radio0" || js[2].Command != "wifi down radio0" {
		t.Fatalf("%+v", js[2])
	}
	if _, err := Add(out, "0 1 * * *", "x", "wifi-off-radio0"); err == nil {
		t.Fatal("same tag twice")
	}
	off, err := Toggle(out, js[0].Line)
	if err != nil || !Parse(off)[0].Off {
		t.Fatalf("%v %q", err, off)
	}
	on, _ := Toggle(off, js[0].Line)
	if on != out {
		t.Fatal("toggle is not reversible")
	}
	rm, err := Remove(out, js[1].Line)
	if err != nil || strings.Contains(rm, "ddns_update") || !strings.Contains(rm, "# keep") || !strings.Contains(rm, "MAILTO=") {
		t.Fatalf("%v %q", err, rm)
	}
	if _, err := Remove(out, 0); err == nil {
		t.Fatal("removed a comment line")
	}
	if _, err := Toggle(out, 99); err == nil {
		t.Fatal("bad line")
	}
}

func TestValidation(t *testing.T) {
	for _, ok := range []string{"* * * * *", "*/15 * * * *", "0 4 * * 1-5", "0,30 8-17 * * *", "5 4 1 12 7"} {
		if err := ValidSchedule(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "*/0 * * * *", "a * * * *", "* * * * * *", "1- * * * *"} {
		if ValidSchedule(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, bad := range []string{"", "a\nb", "# x", "x # owrt-tui:y"} {
		if ValidCommand(bad) == nil {
			t.Errorf("command accepted %q", bad)
		}
	}
}

func TestPresetsAndHelpers(t *testing.T) {
	s, c, tag, err := Reboot(4, 0, "*")
	if err != nil || s != "0 4 * * *" || !strings.Contains(c, "reboot") || tag != "reboot" {
		t.Fatalf("%v %v %v %v", s, c, tag, err)
	}
	if _, _, _, err := Reboot(24, 0, "*"); err == nil {
		t.Fatal("bad hour")
	}
	if _, _, _, err := Reboot(4, 0, "9"); err == nil {
		t.Fatal("bad day of week")
	}
	j, err := WifiSchedule("radio0", 23, 30, 6, 0)
	if err != nil || j[0][0] != "30 23 * * *" || j[1][1] != "wifi up radio0" {
		t.Fatalf("%v %v", j, err)
	}
	if _, err := WifiSchedule("radio0; reboot", 1, 0, 2, 0); err == nil {
		t.Fatal("injection")
	}
	if _, err := WifiSchedule("radio0", 1, 0, 1, 0); err == nil {
		t.Fatal("same times")
	}
	if FlashWrite("x >> /root/a.log 2>&1") != "/root/a.log" || FlashWrite("x >> /tmp/a.log") != "" || FlashWrite("x > /dev/null") != "" {
		t.Fatal("FlashWrite")
	}
	if !Frequent("*/15 * * * *") || Frequent("*/30 * * * *") || Frequent("0 4 * * *") || !Frequent("* * * * *") {
		t.Fatal("Frequent")
	}
}

func TestTemplates(t *testing.T) {
	s, c, tag, err := WGWatchdog("eduvpn", 5)
	if err != nil || s != "*/5 * * * *" || tag != "wg-watchdog-eduvpn" || !strings.Contains(c, `date +\%s`) {
		t.Fatalf("%q %q %q %v", s, c, tag, err)
	}
	if _, err := Add("", s, c, tag); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Reconnect("sim; reboot", "1.1.1.1", 5); err == nil {
		t.Fatal("injection in interface name accepted")
	}
	if _, _, _, err := Reconnect("sim", "1.1.1.1;reboot", 5); err == nil {
		t.Fatal("injection in target accepted")
	}
	if _, _, _, err := RestartService("../x", "restart", 3, 0); err == nil {
		t.Fatal("bad service accepted")
	}
	if _, _, _, err := RestartService("adblock", "stop", 3, 0); err == nil {
		t.Fatal("bad action accepted")
	}
	if s, c, _, _ := Reconnect("sim", "1.1.1.1", 5); !strings.Contains(c, "ifup sim") || s != "*/5 * * * *" {
		t.Fatal(c)
	}
}
