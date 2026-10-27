package pkgmgr

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/madushakv/owrt-tui/internal/shq"
)

// OPKG is the legacy backend for OpenWrt 24.10 and older.
type OPKG struct{}

func (OPKG) Name() string             { return "opkg" }
func (OPKG) CmdUpdate() string        { return "opkg update" }
func (OPKG) CmdListInstalled() string { return "opkg list-installed" }
func (OPKG) CmdListUpgradable() string {
	return "opkg list-upgradable"
}

func (OPKG) CmdSearch(p string) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid search pattern")
	}
	return "opkg list " + shq.Quote("*"+p+"*"), nil
}

func (OPKG) CmdInfo(p string) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	return "opkg info " + shq.Quote(p), nil
}

func (OPKG) CmdFiles(p string) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	return "opkg files " + shq.Quote(p), nil
}

func (OPKG) CmdWhoOwns(path string) (string, error) {
	if !shq.NoControl(path) || path == "" {
		return "", fmt.Errorf("invalid path")
	}
	return "opkg search " + shq.Quote(path), nil
}

func (OPKG) CmdAdd(p string, sim bool) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	flags := ""
	if sim {
		flags = " --noaction"
	}
	return "opkg install" + flags + " " + shq.Quote(p), nil
}

func (OPKG) CmdDel(p string, sim bool) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	flags := ""
	if sim {
		flags = " --noaction"
	}
	return "opkg remove" + flags + " " + shq.Quote(p), nil
}

// ParseList reads "name - version" and "name - old - new" lines.
func (OPKG) ParseList(out string) []Package {
	var pk []Package
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), " - ")
		switch len(f) {
		case 2:
			pk = append(pk, Package{Name: f[0], Version: f[1], Flags: "installed"})
		case 3:
			pk = append(pk, Package{Name: f[0], Version: f[2], Flags: "upgradable from: " + f[1]})
		}
	}
	return pk
}

// ParseSearch reads "name - version - description".
func (OPKG) ParseSearch(out string) []Package {
	var pk []Package
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(l), " - ", 3)
		if len(f) >= 2 {
			p := Package{Name: f[0], Version: f[1]}
			if len(f) == 3 {
				p.Desc = f[2]
			}
			pk = append(pk, p)
		}
	}
	return pk
}

var opkgInstall = regexp.MustCompile(`^(Installing|Upgrading|Downgrading|Reinstalling|Removing)\s+(\S+)\s+\(([^)]*)\)`)

func (OPKG) ParseActions(out string) []Action {
	var a []Action
	for _, l := range strings.Split(out, "\n") {
		if m := opkgInstall.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			v := m[1]
			if v == "Removing" {
				v = "Purging"
			}
			a = append(a, Action{Verb: v, Name: m[2], Version: m[3]})
		}
	}
	return a
}
