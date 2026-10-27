package device

import "testing"

const probeOut = `##board
{
	"kernel": "6.12.94",
	"hostname": "gw-home",
	"system": "ARMv8",
	"model": "Dynalink DL-WRX36",
	"board_name": "dynalink,dl-wrx36",
	"rootfs_type": "squashfs",
	"release": {
		"distribution": "OpenWrt",
		"version": "25.12.3",
		"revision": "r32912-6639b15f62",
		"target": "qualcommax/ipq807x",
		"description": "OpenWrt 25.12.3 r32912-6639b15f62"
	}
}
##uname
aarch64
##tools
apk
nft
fw4
wg
ubus
uci
##swconfig
##df
Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/ubi0_2              61440     21000     40440  34% /overlay
tmpfs                   262144      3000    259144   1% /tmp
##uptime
1036800.55 2000000.00
`

func TestParseProbe(t *testing.T) {
	i, err := ParseProbe(probeOut)
	if err != nil {
		t.Fatal(err)
	}
	if i.PkgMgr != "apk" || i.Firewall != "fw4" || i.Swconfig {
		t.Fatalf("caps: %+v", i)
	}
	if i.Version != "25.12.3" || i.Series() != "25.12" || i.Hostname != "gw-home" {
		t.Fatalf("release: %+v", i)
	}
	if i.OverlayKB.Avail != 40440 || i.TmpKB.Total != 262144 {
		t.Fatalf("df: %+v %+v", i.OverlayKB, i.TmpKB)
	}
	if i.UptimeSecs != 1036800 {
		t.Fatalf("uptime %d", i.UptimeSecs)
	}
	adv := i.Advisories()
	if len(adv) != 1 || adv[0].Level != "warn" {
		t.Fatalf("advisories: %+v", adv)
	}
}

func TestAdvisories(t *testing.T) {
	cases := []struct {
		ver  string
		want int
	}{
		{"25.12.5", 0}, {"25.12.4", 1}, {"24.10.4", 1}, {"23.05.5", 1}, {"SNAPSHOT", 1}, {"25.12-SNAPSHOT", 1},
	}
	for _, c := range cases {
		i := &Info{Version: c.ver}
		if got := len(i.Advisories()); got != c.want {
			t.Errorf("%s: got %d advisories, want %d (%+v)", c.ver, got, c.want, i.Advisories())
		}
	}
}

func TestParseProbeNotOpenWrt(t *testing.T) {
	if _, err := ParseProbe("##board\n\n##uname\nx86_64\n"); err == nil {
		t.Fatal("expected error when board info is missing")
	}
}

func TestOpkgFallback(t *testing.T) {
	i, _ := ParseProbe(`##board
{"hostname":"a","release":{"version":"24.10.0"}}
##tools
opkg
fw4
`)
	if i.PkgMgr != "opkg" {
		t.Fatal("expected opkg")
	}
}
