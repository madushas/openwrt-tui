package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const procRoute = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
	"wlan0\t00000000\t0100A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
	"eth0\t00000000\t010200C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
	"eth0\t000200C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"

func TestParseProcRoutePicksLowestMetric(t *testing.T) {
	got, err := ParseProcRoute(procRoute)
	if err != nil || got != "192.0.2.1" {
		t.Fatalf("got %q, %v; want 192.0.2.1 (eth0, metric 100)", got, err)
	}
	if _, err := ParseProcRoute("Iface\tDestination\tGateway\tFlags\n"); err == nil {
		t.Fatal("expected an error when there is no default route")
	}
}

func TestParseProcRouteLittleEndian(t *testing.T) {
	text := "h\n" + "e\t00000000\t0100A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	if got, _ := ParseProcRoute(text); got != "192.168.0.1" {
		t.Fatalf("got %q, want 192.168.0.1", got)
	}
}

func TestParseRouteGet(t *testing.T) {
	out := "   route to: default\ndestination: default\n       mask: default\n    gateway: 192.168.1.1\n  interface: en0\n"
	if got, err := ParseRouteGet(out); err != nil || got != "192.168.1.1" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ParseRouteGet("gateway: fe80::1%en0\n"); err == nil {
		t.Fatal("IPv6 link-local gateway should not be returned")
	}
}

func TestNormalizeTarget(t *testing.T) {
	good := map[string]string{
		"192.168.1.1":       "root@192.168.1.1",
		"openwrt.lan":       "root@openwrt.lan",
		"admin@192.168.1.1": "admin@192.168.1.1",
		" root@router-1 ":   "root@router-1",
		"root@fe80::1":      "root@fe80::1",
	}
	for in, want := range good {
		if got, err := NormalizeTarget(in); err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "root@-oProxyCommand=x", "a b", "root@host;rm", "root@host name", "$(id)@h", "root@"} {
		if got, err := NormalizeTarget(bad); err == nil {
			t.Errorf("%q should be rejected, got %q", bad, got)
		}
	}
}

func TestParseHosts(t *testing.T) {
	cfg := "Host *\n  ServerAliveInterval 5\nHost gw openwrt-main !bad *.corp\n  User root\nhost Second # comment\nMatch host x\n"
	got := strings.Join(ParseHosts(cfg), ",")
	if got != "gw,openwrt-main,Second" {
		t.Fatalf("got %q", got)
	}
}

func TestAppendHostIsAppendOnlyAndRefusesDuplicates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config")
	if err := AppendHost(p, HostEntry{Alias: "gw", HostName: "192.168.1.1", User: "root", IdentityFile: "~/.ssh/id_ed25519_owrt"}); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(p)
	if err := AppendHost(p, HostEntry{Alias: "gw", HostName: "10.0.0.1"}); err == nil {
		t.Fatal("duplicate alias accepted")
	}
	if now, _ := os.ReadFile(p); string(now) != string(orig) {
		t.Fatal("file changed after refused append")
	}
	if err := AppendHost(p, HostEntry{Alias: "two", HostName: "10.0.0.2"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), string(orig)) {
		t.Fatal("existing content was modified")
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v, want 0600", st.Mode().Perm())
	}
	if err := AppendHost(p, HostEntry{Alias: "x", HostName: "h\nProxyCommand evil"}); err == nil {
		t.Fatal("newline injection accepted")
	}
}

func TestRecentOrderAndLimit(t *testing.T) {
	d := t.TempDir()
	for _, tg := range []string{"root@a", "root@b", "root@c", "root@a", "root@d", "root@e", "root@f"} {
		if err := AddRecent(d, tg); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(Recent(d), ",")
	if got != "root@f,root@e,root@d,root@a,root@c" {
		t.Fatalf("got %q", got)
	}
}

func TestPubKeys(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "id_ed25519.pub"), []byte("ssh-ed25519 AAAAC3Nza me@laptop\n"), 0o644)
	os.WriteFile(filepath.Join(d, "evil.pub"), []byte("ssh-ed25519 AAAA\" ; rm -rf x\n"), 0o644)
	os.WriteFile(filepath.Join(d, "junk.pub"), []byte("not a key\n"), 0o644)
	ks := PubKeys(d)
	if len(ks) != 1 || ks[0].Comment != "me@laptop" || filepath.Base(ks[0].Private) != "id_ed25519" {
		t.Fatalf("got %+v", ks)
	}
}

func TestNewKeyPathNeverOverwrites(t *testing.T) {
	d := t.TempDir()
	if p := NewKeyPath(d); filepath.Base(p) != "id_ed25519" {
		t.Fatalf("empty dir: got %s", p)
	}
	os.WriteFile(filepath.Join(d, "id_ed25519"), nil, 0o600)
	if p := NewKeyPath(d); filepath.Base(p) != "id_ed25519_owrt" {
		t.Fatalf("got %s", p)
	}
	os.WriteFile(filepath.Join(d, "id_ed25519_owrt.pub"), nil, 0o600)
	if p := NewKeyPath(d); filepath.Base(p) != "id_ed25519_owrt2" {
		t.Fatalf("got %s", p)
	}
}
