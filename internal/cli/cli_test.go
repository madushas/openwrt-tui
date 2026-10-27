package cli

import (
	"bytes"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = Run(append([]string{"-demo"}, args...), &o, &e)
	return code, o.String(), e.String()
}

func TestProbe(t *testing.T) {
	code, out, errs := run(t, "probe")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"gw-demo", "25.12.3", "apk", "fw4", "12d 1h 46m", "wg"} {
		if !strings.Contains(out, want) {
			t.Errorf("probe output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "WARN: 25.12.3 is older than 25.12.5") {
		t.Errorf("expected the security advisory for an old 25.12 release:\n%s", out)
	}
}

func TestUCIMasksSecretsByDefault(t *testing.T) {
	code, out, errs := run(t, "uci")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, secret := range []string{"DEMOdemoDEMOdemo", "DEMOpsk", "demo-wifi-passphrase"} {
		if strings.Contains(out, secret) {
			t.Errorf("secret %q leaked without -reveal:\n%s", secret, out)
		}
	}
	if strings.Count(out, "********") != 3 {
		t.Errorf("expected exactly 3 masked values:\n%s", out)
	}
	if !strings.Contains(out, "PUBLICpublic") {
		t.Error("public keys must stay visible")
	}
}

func TestUCIReveal(t *testing.T) {
	_, out, _ := run(t, "uci", "-reveal", "wireless")
	if !strings.Contains(out, "demo-wifi-passphrase") || strings.Contains(out, "network.lan") {
		t.Errorf("-reveal with a config filter:\n%s", out)
	}
}

func TestUCIUnknownConfig(t *testing.T) {
	code, _, errs := run(t, "uci", "nosuch")
	if code != 1 || !strings.Contains(errs, `no config named "nosuch"`) {
		t.Errorf("code %d, %q", code, errs)
	}
}

func TestServices(t *testing.T) {
	_, out, _ := run(t, "services")
	if !strings.Contains(out, "dnsmasq") || !strings.Contains(out, "running") || !strings.Contains(out, "stopped") {
		t.Errorf("%s", out)
	}
}

func TestLogsFilter(t *testing.T) {
	_, all, _ := run(t, "logs")
	if n := strings.Count(all, "\n"); n != 5 {
		t.Errorf("unfiltered: %d lines", n)
	}
	_, warn, _ := run(t, "logs", "-level", "warn")
	if n := strings.Count(warn, "\n"); n != 2 { // err and warn
		t.Errorf("warn and worse: %d lines:\n%s", n, warn)
	}
	_, byProc, _ := run(t, "logs", "-proc", "dropbear")
	if n := strings.Count(byProc, "\n"); n != 1 || !strings.Contains(byProc, "Bad password") {
		t.Errorf("by process:\n%s", byProc)
	}
	if code, _, errs := run(t, "logs", "-level", "loud"); code != 2 || !strings.Contains(errs, "unknown level") {
		t.Errorf("bad level: %d %q", code, errs)
	}
}

func TestPkg(t *testing.T) {
	_, out, _ := run(t, "pkg", "search", "wireguard")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// lines[0] is the "package manager:" header; name matches must precede the
	// description-only match (luci-app-statistics).
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "luci-app-statistics") {
		t.Errorf("description-only match should rank last:\n%s", out)
	}
	_, plan, _ := run(t, "pkg", "plan-add", "wireguard-tools")
	if !strings.Contains(plan, "Installing") || !strings.Contains(plan, "nothing was changed") {
		t.Errorf("%s", plan)
	}
	if code, _, _ := run(t, "pkg", "search"); code != 2 {
		t.Errorf("missing argument should be a usage error, got %d", code)
	}
	if code, _, _ := run(t, "pkg", "install", "x"); code != 2 {
		t.Errorf("the CLI has no install command, got %d", code)
	}
}

func TestDiag(t *testing.T) {
	_, out, _ := run(t, "ping", "1.1.1.1")
	if !strings.Contains(out, "4 sent, 4 received, 0% loss") || !strings.Contains(out, "9.1/9.6/10.4") {
		t.Errorf("%s", out)
	}
	_, tr, _ := run(t, "trace", "1.1.1.1")
	if strings.Count(tr, "\n") != 4 {
		t.Errorf("%s", tr)
	}
	if code, _, errs := run(t, "ping", "1.1.1.1; reboot"); code != 1 || !strings.Contains(errs, "invalid host") {
		t.Errorf("injection must be rejected: %d %q", code, errs)
	}
}

func TestUsageErrors(t *testing.T) {
	var o, e bytes.Buffer
	if code := Run(nil, &o, &e); code != 2 {
		t.Errorf("no command: %d", code)
	}
	if code := Run([]string{"-demo", "frobnicate"}, &o, &e); code != 2 {
		t.Errorf("unknown command: %d", code)
	}
	e.Reset()
	if code := Run([]string{"probe"}, &o, &e); code != 2 || !strings.Contains(e.String(), "no router given") {
		t.Errorf("no target: %d %q", code, e.String())
	}
}

func TestSample(t *testing.T) {
	code, out, errs := run(t, "sample")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"Load", "Memory", "Interfaces", "wan(up)", "Wi-Fi clients"} {
		if !strings.Contains(out, want) {
			t.Errorf("sample output missing %q:\n%s", want, out)
		}
	}
	code, raw, _ := run(t, "sample", "-raw")
	if code != 0 || !strings.Contains(raw, "##owrt-metrics") || !strings.Contains(raw, "##end") {
		t.Fatalf("-raw should print the unparsed router output:\n%s", raw)
	}
}
