// Package gitexport records the router's configuration in a git repository on
// the user's PC after every apply, so the whole history is reviewable with
// ordinary git tools. Only masked text is ever written: secret values are
// replaced before they reach the disk.
package gitexport

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}$`)

// Export writes the masked configs (name -> text) under <dir>/<router>/ and
// commits them. The repository is created if needed. It returns whether a
// commit was made; an unchanged export makes no commit.
func Export(dir, router string, files map[string]string, message string) (bool, error) {
	if dir == "" {
		return false, errors.New("no export directory")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return false, errors.New("git is not installed")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if out, err := git(dir, "init", "-q"); err != nil {
			return false, fmt.Errorf("git init: %s", out)
		}
	}
	sub := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(router, "_"), "._")
	if sub == "" {
		sub = "router"
	}
	target := filepath.Join(dir, sub)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return false, err
	}
	// drop files of configs that no longer exist
	old, _ := filepath.Glob(filepath.Join(target, "*.uci"))
	for _, f := range old {
		if _, keep := files[strings.TrimSuffix(filepath.Base(f), ".uci")]; !keep {
			_ = os.Remove(f)
		}
	}
	for name, text := range files {
		if !nameRe.MatchString(name) {
			return false, fmt.Errorf("invalid config name %q", name)
		}
		if err := os.WriteFile(filepath.Join(target, name+".uci"), []byte(text), 0o644); err != nil {
			return false, err
		}
	}
	if out, err := git(dir, "add", "-A", "--", sub); err != nil {
		return false, fmt.Errorf("git add: %s", out)
	}
	if _, err := git(dir, "diff", "--cached", "--quiet", "--", sub); err == nil {
		return false, nil // nothing changed
	}
	if message == "" {
		message = "update " + sub
	}
	// An explicit identity, so a missing global git config cannot block the commit.
	out, err := git(dir, "-c", "user.name=owrt-tui", "-c", "user.email=owrt-tui@localhost", "commit", "-q", "-m", message, "--", sub)
	if err != nil {
		return false, fmt.Errorf("git commit: %s", out)
	}
	return true, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
