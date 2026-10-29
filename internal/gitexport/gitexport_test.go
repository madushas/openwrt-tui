package gitexport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return out
}

func TestExportCommitsOnlyWhenSomethingChanged(t *testing.T) {
	needGit(t)
	dir := filepath.Join(t.TempDir(), "repo")
	files := map[string]string{"dhcp": "package dhcp\n", "network": "package network\n"}
	if ok, err := Export(dir, "root@router.lan", files, "first"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, err := Export(dir, "root@router.lan", files, "again"); err != nil || ok {
		t.Fatalf("unchanged export made a commit: %v %v", ok, err)
	}
	files["dhcp"] = "package dhcp\n\nconfig dhcp 'lan'\n"
	if ok, err := Export(dir, "root@router.lan", files, "dhcp changed"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	log := gitOut(t, dir, "log", "--format=%s")
	if log != "dhcp changed\nfirst" {
		t.Fatalf("log %q", log)
	}
	delete(files, "network")
	Export(dir, "root@router.lan", files, "network removed")
	if _, err := os.Stat(filepath.Join(dir, "root_router.lan", "network.uci")); err == nil {
		t.Fatal("a removed config stayed in the repository")
	}
}

func TestExportWorksWithoutAnyGitIdentityAndRejectsBadNames(t *testing.T) {
	needGit(t)
	home := t.TempDir() // no ~/.gitconfig
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	dir := filepath.Join(t.TempDir(), "r")
	if ok, err := Export(dir, "r", map[string]string{"a": "x"}, "m"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if _, err := Export(dir, "r", map[string]string{"../evil": "x"}, "m"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if !strings.Contains(gitOut(t, dir, "log", "--format=%an"), "owrt-tui") {
		t.Fatal("commit not attributed to owrt-tui")
	}
}
