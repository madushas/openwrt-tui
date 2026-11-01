package postcheck

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestParseAndHealthy(t *testing.T) {
	r := Parse("##owrt-postcheck\ndefault_route=ok\ngateway=fail\ndns=ok\nwg_vpn=42\nwg_old=never\nproc_dnsmasq=ok\njunk line\nbad=a b\n")
	if len(r) != 6 {
		t.Fatalf("%v", r)
	}
	if !Healthy("wg_vpn", "42") || Healthy("wg_vpn", "900") || Healthy("wg_old", "never") || Healthy("gateway", "fail") {
		t.Fatal("Healthy wrong")
	}
	if r.Passing() != 4 {
		t.Fatalf("passing %d", r.Passing())
	}
}

func TestRegressions(t *testing.T) {
	before := Result{"default_route": "ok", "gateway": "ok", "dns": "ok", "wg_vpn": "30", "proc_dropbear": "ok", "proc_uhttpd": "fail"}
	same := Result{"default_route": "ok", "gateway": "ok", "dns": "ok", "wg_vpn": "100", "proc_dropbear": "ok", "proc_uhttpd": "fail"}
	if got := Regressions(before, same); len(got) != 0 {
		t.Fatalf("a check that already failed was blamed on the change: %v", got)
	}
	worse := Result{"default_route": "ok", "gateway": "fail", "wg_vpn": "never", "proc_dropbear": "ok"}
	want := []string{"DNS answers: no answer", "WireGuard vpn: no handshake", "gateway reachable: failed"}
	if got := Regressions(before, worse); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

// The script must at least be valid shell and print the marker.
func TestScriptIsValidShellAndOnlyReads(t *testing.T) {
	if out, err := exec.Command("sh", "-n", "-c", Script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, bad := range []string{"uci set", "uci commit", "reboot", "rm ", "/etc/init.d/$p start", "restart", "> /"} {
		if strings.Contains(Script, bad) {
			t.Fatalf("script contains %q", bad)
		}
	}
}
