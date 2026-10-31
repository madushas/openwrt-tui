package compare

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/uci"
)

func parse(t *testing.T, s string) []uci.Config {
	t.Helper()
	c, err := uci.ParseExport(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const a = `package network

config interface 'lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
	option macaddr 'aa:aa:aa:aa:aa:aa'
	list dns '1.1.1.1'

config interface 'wwan'
	option proto 'ncm'

package wireless

config wifi-iface 'wlan0'
	option ssid 'home'
	option key 'sekret-one'

package firewall

config rule
	option name 'a'

config rule
	option name 'b'
`

const b = `package network

config interface 'lan'
	option proto 'static'
	option ipaddr '10.0.0.1'
	option macaddr 'bb:bb:bb:bb:bb:bb'
	list dns '9.9.9.9'
	option metric '5'

config interface 'direct'
	option proto 'dhcp'

package wireless

config wifi-iface 'wlan0'
	option ssid 'home'
	option key 'sekret-two'

package firewall

config rule
	option name 'a'

config rule
	option name 'c'
`

func TestCompare(t *testing.T) {
	d := Compare(parse(t, a), parse(t, b), DefaultOptions())
	text := Format(d, "A", "B")
	for _, want := range []string{
		"~ ipaddr: 192.168.1.1  ->  10.0.0.1",
		"~ dns: [1.1.1.1]  ->  [9.9.9.9]",
		"> metric = 5",
		"whole section only on A",
		"whole section only on B",
		"~ name: b  ->  c",
		"~ key: ********  ->  ********",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, bad := range []string{"macaddr", "sekret", "aa:aa"} {
		if strings.Contains(text, bad) {
			t.Errorf("%q must not appear:\n%s", bad, text)
		}
	}
}

func TestIdenticalAndFilter(t *testing.T) {
	if got := Format(Compare(parse(t, a), parse(t, a), DefaultOptions()), "A", "B"); got != "no differences\n" {
		t.Fatal(got)
	}
	o := DefaultOptions()
	o.Configs = []string{"firewall"}
	text := Format(Compare(parse(t, a), parse(t, b), o), "A", "B")
	if strings.Contains(text, "network") || !strings.Contains(text, "firewall") {
		t.Fatalf("filter: %s", text)
	}
}

func TestSecretDifferenceIsReportedWithoutValues(t *testing.T) {
	d := Compare(parse(t, a), parse(t, b), Options{Configs: []string{"wireless"}})
	if len(d) != 1 || d[0].Option != "key" || d[0].Kind != Differ || d[0].A != uci.MaskText || d[0].B != uci.MaskText {
		t.Fatalf("%+v", d)
	}
}
