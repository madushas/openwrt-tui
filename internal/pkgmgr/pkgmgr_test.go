package pkgmgr

import "testing"

func TestAPKParseList(t *testing.T) {
	out := `busybox-1.37.0-r1 aarch64 {busybox} (GPL-2.0-only) [installed]
kmod-8021q-6.12.94-r1 aarch64 {kernel} (GPL-2.0-only) [installed]
dnsmasq-full-2.91-r3 aarch64 {feeds/base/package/network/services/dnsmasq} (GPL-2.0) [upgradable from: dnsmasq-full-2.90-r3]
<dnsmasq> dnsmasq-full-2.90-r3 x86_64 {feeds/base/package/network/services/dnsmasq} (GPL-2.0) [installed]
nmap-7.95-r1 aarch64 {nmap} (Nmap)
garbage line
`
	p := APK{}.ParseList(out)
	if len(p) != 5 {
		t.Fatalf("got %d: %+v", len(p), p)
	}
	if p[1].Name != "kmod-8021q" || p[1].Version != "6.12.94-r1" {
		t.Fatalf("digit-leading segment split wrongly: %+v", p[1])
	}
	if p[2].Flags != "upgradable from: dnsmasq-full-2.90-r3" || p[2].Name != "dnsmasq-full" {
		t.Fatalf("upgradable: %+v", p[2])
	}
	if p[3].Name != "dnsmasq-full" || p[3].Arch != "x86_64" {
		t.Fatalf("provides tag: %+v", p[3])
	}
	if p[4].Flags != "" {
		t.Fatalf("flags: %+v", p[4])
	}
}

func TestAPKParseSearch(t *testing.T) {
	p := APK{}.ParseSearch("wireguard-tools-1.0.20250521-r1 - WireGuard userspace tools\nluci-proto-wireguard-26.100-r1\n")
	if len(p) != 2 || p[0].Name != "wireguard-tools" || p[0].Desc == "" || p[1].Name != "luci-proto-wireguard" {
		t.Fatalf("%+v", p)
	}
}

func TestAPKActions(t *testing.T) {
	a := APK{}.ParseActions("(1/2) Installing libfoo (1.0-r1)\n(2/2) Purging nmap (7.95-r1)\nOK: 47 MiB in 288 packages\n")
	if len(a) != 2 || a[0].Verb != "Installing" || a[1].Verb != "Purging" || a[1].Version != "7.95-r1" {
		t.Fatalf("%+v", a)
	}
}

func TestAPKCommands(t *testing.T) {
	c, err := APK{}.CmdAdd("wireguard-tools", true)
	if err != nil || c != "apk add --simulate -- 'wireguard-tools'" {
		t.Fatalf("%q %v", c, err)
	}
	for _, bad := range []string{"", "x; reboot", "a b", "$(id)", "-rf"} {
		if _, err := (APK{}).CmdAdd(bad, false); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestOPKG(t *testing.T) {
	o := OPKG{}
	p := o.ParseList("busybox - 1.36.1-1\nwpad - 2023-1 - 2024-2\n")
	if len(p) != 2 || p[1].Version != "2024-2" || p[1].Flags != "upgradable from: 2023-1" {
		t.Fatalf("%+v", p)
	}
	a := o.ParseActions("Installing foo (1.0) to root...\nRemoving package bar from root...\nRemoving baz (2.0) from root\n")
	if len(a) != 2 || a[0].Verb != "Installing" || a[1].Verb != "Purging" || a[1].Name != "baz" {
		t.Fatalf("%+v", a)
	}
	c, _ := o.CmdDel("foo", true)
	if c != "opkg remove --noaction 'foo'" {
		t.Fatal(c)
	}
}

func TestRankSearch(t *testing.T) {
	in := []Package{{Name: "luci-app-wireguard"}, {Name: "kmod-wireguard-x", Desc: "wireguard"}, {Name: "wireguard"}, {Name: "wireguard-tools"}, {Name: "other", Desc: "wireguard thing"}}
	out := RankSearch(in, "wireguard")
	got := []string{out[0].Name, out[1].Name, out[2].Name, out[3].Name, out[4].Name}
	want := []string{"wireguard", "wireguard-tools", "kmod-wireguard-x", "luci-app-wireguard", "other"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestForName(t *testing.T) {
	if b, err := ForName("apk"); err != nil || b.Name() != "apk" {
		t.Fatal("apk")
	}
	if _, err := ForName(""); err == nil {
		t.Fatal("empty should error")
	}
}
