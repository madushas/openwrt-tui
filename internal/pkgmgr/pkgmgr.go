// Package pkgmgr wraps apk (OpenWrt 25.12+) and opkg (24.10 and older) behind
// one interface. Command strings and parsers are pure functions so they can be
// tested without a router.
package pkgmgr

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/madushakv/owrt-tui/internal/transport"
)

// Package is a row in a package list.
type Package struct {
	Name    string
	Version string
	Arch    string
	Desc    string
	Flags   string // e.g. "installed", "upgradable from: x-1.0-r1"
}

// Action is one line of a simulated transaction.
type Action struct {
	Verb    string // Installing, Upgrading, Purging, Replacing, ...
	Name    string
	Version string
}

// Backend builds commands and parses their output for one package manager.
type Backend interface {
	Name() string
	CmdUpdate() string
	CmdListInstalled() string
	CmdListUpgradable() string
	CmdSearch(pattern string) (string, error)
	CmdInfo(pkg string) (string, error)
	CmdFiles(pkg string) (string, error)
	CmdWhoOwns(path string) (string, error)
	CmdAdd(pkg string, simulate bool) (string, error)
	CmdDel(pkg string, simulate bool) (string, error)
	ParseList(out string) []Package
	ParseSearch(out string) []Package
	ParseActions(out string) []Action
}

// ForName returns the backend for "apk" or "opkg".
func ForName(name string) (Backend, error) {
	switch name {
	case "apk":
		return APK{}, nil
	case "opkg":
		return OPKG{}, nil
	}
	return nil, fmt.Errorf("no supported package manager found (apk or opkg)")
}

// Runner executes backend commands over a transport.
type Runner struct {
	B Backend
	T transport.Transport
}

// Exec runs a command and returns combined output. apk writes ordinary status
// lines to stderr, so a non-zero exit code is the only failure signal.
func (r Runner) Exec(ctx context.Context, cmd string) (string, error) {
	res, err := r.T.Run(ctx, cmd+" 2>&1", nil)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("exit %d: %s", res.ExitCode, firstNonEmpty(res.Stdout, res.Stderr))
	}
	return res.Stdout, nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s = strings.TrimSpace(s); s != "" {
			if len(s) > 300 {
				s = s[:300] + "..."
			}
			return s
		}
	}
	return ""
}

// RankSearch orders results so exact and prefix name matches come first, then
// other name matches, then description-only matches. apk search is noisy.
func RankSearch(pkgs []Package, query string) []Package {
	q := strings.ToLower(query)
	score := func(p Package) int {
		n := strings.ToLower(p.Name)
		switch {
		case n == q:
			return 0
		case strings.HasPrefix(n, q):
			return 1
		case strings.Contains(n, q):
			return 2
		}
		return 3
	}
	out := append([]Package(nil), pkgs...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := score(out[i]), score(out[j])
		if si != sj {
			return si < sj
		}
		return out[i].Name < out[j].Name
	})
	return out
}
