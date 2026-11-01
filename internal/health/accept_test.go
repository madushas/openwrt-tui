package health

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

const twoZones = `package firewall
config zone
	option name 'lan'
	option input 'ACCEPT'
	list network 'lan'
	list network 'direct'
config zone
	option name 'direct_zone'
	option input 'ACCEPT'
	list network 'direct'
package wireless
config wifi-iface 'w'
	option network 'direct'
	option ssid 'Five'
	option encryption 'psk2'
`

func TestFindingsStateTheWifiFactsAndStayCalm(t *testing.T) {
	fs := Check(Input{Configs: parse(t, twoZones), Facts: Facts{SSHKeys: -1}})
	f := find(fs, "multi-zone:direct")
	if f == nil || f.Sev != Warn || !strings.Contains(f.Detail, "uses a password (psk2)") {
		t.Fatalf("%+v", f)
	}
	if z := find(fs, "open-zone:direct_zone"); z == nil || z.Sev != Info {
		t.Fatalf("password-protected network should only be a note: %+v", z)
	}
	open := strings.Replace(twoZones, "option encryption 'psk2'", "option encryption 'none'", 1)
	fs = Check(Input{Configs: parse(t, open), Facts: Facts{SSHKeys: -1}})
	if f := find(fs, "multi-zone:direct"); f == nil || f.Sev != Bad || !strings.Contains(f.Detail, "OPEN") {
		t.Fatalf("open Wi-Fi in two zones must be a problem: %+v", f)
	}
	if z := find(fs, "open-zone:direct_zone"); z == nil || z.Sev != Warn {
		t.Fatalf("open Wi-Fi reaching the router must warn: %+v", z)
	}
}

func TestAcceptHidesUntilTheFindingChanges(t *testing.T) {
	in := Input{Configs: parse(t, twoZones), Facts: Facts{SSHKeys: -1}}
	list := Check(in)
	f := *find(list, "multi-zone:direct")
	acc := Accepted{f.ID: Fingerprint(f)}
	active, accepted := Split(list, acc)
	if find(active, f.ID) != nil || find(accepted, f.ID) == nil {
		t.Fatal("accepted finding still active")
	}
	// the Wi-Fi is later made open: the finding's text and severity change
	in.Configs = parse(t, strings.Replace(twoZones, "option encryption 'psk2'", "option encryption 'none'", 1))
	active, _ = Split(Check(in), acc)
	if find(active, f.ID) == nil {
		t.Fatal("a changed finding must come back even though an older one was accepted")
	}
}

func TestAcceptedStoreIsPrivateAndSurvivesRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not portable to Windows")
	}
	dir := t.TempDir()
	if err := SaveAccepted(dir, "root@192.168.50.1", Accepted{"a": "1"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadAccepted(dir, "root@192.168.50.1"); got["a"] != "1" {
		t.Fatalf("%v", got)
	}
	p, _ := acceptPath(dir, "root@192.168.50.1")
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", st, err)
	}
	if _, err := acceptPath(dir, "../../etc/x"); err != nil {
		t.Fatal(err)
	}
	if p2, _ := acceptPath(dir, "../../etc/x"); !strings.HasPrefix(p2, dir) {
		t.Fatalf("path escaped: %s", p2)
	}
	if len(LoadAccepted(dir, "none")) != 0 {
		t.Fatal("missing file must be empty")
	}
}
