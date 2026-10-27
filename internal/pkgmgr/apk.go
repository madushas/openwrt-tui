package pkgmgr

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/madushakv/owrt-tui/internal/shq"
)

// APK is the Alpine Package Keeper backend used by OpenWrt 25.12 and newer.
type APK struct{}

func (APK) Name() string             { return "apk" }
func (APK) CmdUpdate() string        { return "apk update" }
func (APK) CmdListInstalled() string { return "apk list --installed" }
func (APK) CmdListUpgradable() string {
	return "apk list --upgradeable"
}

func (APK) CmdSearch(p string) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid search pattern")
	}
	return "apk search -v -- " + shq.Quote(p), nil
}

func (APK) CmdInfo(p string) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	return "apk info --all -- " + shq.Quote(p), nil
}

func (APK) CmdFiles(p string) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	return "apk info --contents -- " + shq.Quote(p), nil
}

func (APK) CmdWhoOwns(path string) (string, error) {
	if !shq.NoControl(path) || path == "" {
		return "", fmt.Errorf("invalid path")
	}
	return "apk info --who-owns -- " + shq.Quote(path), nil
}

func (APK) CmdAdd(p string, sim bool) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	flags := ""
	if sim {
		flags = " --simulate"
	}
	return "apk add" + flags + " -- " + shq.Quote(p), nil
}

func (APK) CmdDel(p string, sim bool) (string, error) {
	if !shq.PkgName(p) {
		return "", fmt.Errorf("invalid package name")
	}
	flags := ""
	if sim {
		flags = " --simulate"
	}
	return "apk del" + flags + " -- " + shq.Quote(p), nil
}

// listRe matches "apk list" lines, with an optional leading "<provides>" tag:
//
//	busybox-1.37.0-r1 aarch64 {busybox} (GPL-2.0-only) [installed]
//
// The version is the trailing "<digits...>-r<n>" so names containing digits,
// such as kmod-8021q, are not split in the wrong place.
var listRe = regexp.MustCompile(`^(?:<[^>]*>\s+)?(.+)-(\d[^\s-]*-r\d+)\s+(\S+)\s+\{[^}]*\}\s+\([^)]*\)(?:\s+\[(.*)\])?\s*$`)

func (APK) ParseList(out string) []Package {
	var pk []Package
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if m := listRe.FindStringSubmatch(l); m != nil {
			pk = append(pk, Package{Name: m[1], Version: m[2], Arch: m[3], Flags: m[4]})
		}
	}
	return pk
}

var searchRe = regexp.MustCompile(`^(.+)-(\d[^\s-]*-r\d+)(?:\s+-\s+(.*))?$`)

func (APK) ParseSearch(out string) []Package {
	var pk []Package
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if m := searchRe.FindStringSubmatch(l); m != nil {
			pk = append(pk, Package{Name: m[1], Version: m[2], Desc: m[3]})
		}
	}
	return pk
}

var actionRe = regexp.MustCompile(`^\(\d+/\d+\)\s+(Installing|Upgrading|Downgrading|Reinstalling|Replacing|Purging)\s+(\S+)\s+\(([^)]*)\)`)

func (APK) ParseActions(out string) []Action {
	var a []Action
	for _, l := range strings.Split(out, "\n") {
		if m := actionRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			a = append(a, Action{Verb: m[1], Name: m[2], Version: m[3]})
		}
	}
	return a
}
