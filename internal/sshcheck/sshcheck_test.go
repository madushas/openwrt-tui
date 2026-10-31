package sshcheck

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		msg  string
		want Kind
	}{
		{"ssh: connect to host 192.168.1.1 port 22: Connection refused", Refused},
		{"ssh: connect to host 192.168.1.1 port 22: Operation timed out", Timeout},
		{"ssh: connect to host 10.9.9.9 port 22: No route to host", Unreachable},
		{"ssh: Could not resolve hostname gw: Name or service not known", Unreachable},
		{"root@192.168.1.1: Permission denied (publickey,password).", AuthDenied},
		{"Host key verification failed.", HostKeyUnknown},
		{"@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@; Offending ED25519 key in /home/u/.ssh/known_hosts:7; Host key verification failed.", HostKeyChanged},
		{"ssh exited with status 255 and no message", Other},
	}
	for _, c := range cases {
		if got := Classify(c.msg); got != c.want {
			t.Errorf("%q -> %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestOffendingKey(t *testing.T) {
	if got := OffendingKey("x; Offending ED25519 key in /home/u/.ssh/known_hosts:7; y"); got != "/home/u/.ssh/known_hosts:7" {
		t.Fatalf("got %q", got)
	}
}

func TestParseKeyscanPrefersEd25519(t *testing.T) {
	out := "# 192.168.1.1:22 SSH-2.0-dropbear\n192.168.1.1 ssh-rsa AAAArsa\n192.168.1.1 ssh-ed25519 AAAAed\n192.168.1.1 weird-type AAAAx\n"
	line, typ, err := ParseKeyscan(out)
	if err != nil || typ != "ssh-ed25519" || !strings.Contains(line, "AAAAed") {
		t.Fatalf("got %q %q %v", line, typ, err)
	}
	if _, _, err := ParseKeyscan("# nothing\n"); err == nil {
		t.Fatal("expected error with no keys")
	}
}

func TestParseFingerprintAndG(t *testing.T) {
	if got := ParseFingerprint("256 SHA256:abc123 192.168.1.1 (ED25519)\n"); got != "SHA256:abc123" {
		t.Fatalf("got %q", got)
	}
	h, p, k := ParseG("user root\nhostname 10.1.1.1\nport 2222\nuserknownhostsfile ~/.ssh/known_hosts ~/.ssh/known_hosts2\n")
	if h != "10.1.1.1" || p != "2222" || k != "~/.ssh/known_hosts" {
		t.Fatalf("got %q %q %q", h, p, k)
	}
}

func TestPinAppendsExactlyTheShownKey(t *testing.T) {
	kh := filepath.Join(t.TempDir(), ".ssh", "known_hosts")
	os.MkdirAll(filepath.Dir(kh), 0o700)
	os.WriteFile(kh, []byte("old ssh-rsa AAAAold\n"), 0o600)
	hk := HostKey{Line: "192.168.1.1 ssh-ed25519 AAAAed", KnownHosts: kh}
	if err := hk.Pin(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(kh)
	if string(b) != "old ssh-rsa AAAAold\n192.168.1.1 ssh-ed25519 AAAAed\n" {
		t.Fatalf("got %q", b)
	}
	if err := (HostKey{Line: "a\nb", KnownHosts: kh}).Pin(); err == nil {
		t.Fatal("multi-line key accepted")
	}
}

func TestInstallCmdKeepsKeyOutOfTheCommandLine(t *testing.T) {
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabc me@laptop"
	c, err := InstallCmd("root@192.168.1.1", []string{"-p", "2222"}, pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(c.Args, " "), "AAAAC3") {
		t.Fatal("key text is in argv")
	}
	if !strings.Contains(strings.Join(c.Args, " "), "-- root@192.168.1.1") {
		t.Fatalf("target not after --: %v", c.Args)
	}
	for _, bad := range []string{"ssh-ed25519 AAAA\"; reboot", "ssh-ed25519 AAAA\nssh-rsa BBBB", "nope"} {
		if _, err := InstallCmd("root@h", nil, bad); err == nil {
			t.Errorf("accepted key %q", bad)
		}
	}
	if _, err := InstallCmd("-oProxyCommand=x", nil, pub); err == nil {
		t.Fatal("option-looking target accepted")
	}
}

// fakeTools puts scripted ssh, ssh-keyscan and ssh-keygen on PATH.
func fakeTools(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	w := func(name, body string) {
		if err := os.WriteFile(filepath.Join(d, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w("ssh", `printf 'hostname 10.1.1.1\nport 2222\nuserknownhostsfile %s\n' "$KH"`)
	w("ssh-keyscan", `echo "$*" > "$ARGS"; printf '# c\n10.1.1.1 ssh-rsa AAAArsa\n[10.1.1.1]:2222 ssh-ed25519 AAAAed\n'`)
	w("ssh-keygen", `cat >/dev/null; echo "256 SHA256:fp1 x (ED25519)"`)
	t.Setenv("PATH", d+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestScanResolvesAliasAndPortThroughSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test doubles are not executable on Windows")
	}
	fakeTools(t)
	kh := filepath.Join(t.TempDir(), "kh")
	args := filepath.Join(t.TempDir(), "args")
	t.Setenv("KH", kh)
	t.Setenv("ARGS", args)
	hk, err := Scan(context.Background(), "root@gw", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hk.Host != "10.1.1.1" || hk.Port != "2222" || hk.KnownHosts != kh || hk.Type != "ssh-ed25519" || hk.Fingerprint != "SHA256:fp1" {
		t.Fatalf("got %+v", hk)
	}
	if b, _ := os.ReadFile(args); !strings.Contains(string(b), "-p 2222 10.1.1.1") {
		t.Fatalf("keyscan args %q", b)
	}
}
