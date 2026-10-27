// Package shq quotes strings for POSIX sh (BusyBox ash on OpenWrt) and
// validates identifiers that are interpolated into remote commands.
package shq

import (
	"regexp"
	"strings"
)

// Quote returns s as a single-quoted POSIX shell word.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var (
	pkgName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+@~-]*$`)
	uciName  = regexp.MustCompile(`^[A-Za-z0-9_@\[\]\-.]+$`)
	hostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	unitName = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)
)

// PkgName reports whether s is a safe package name.
func PkgName(s string) bool { return len(s) <= 128 && pkgName.MatchString(s) }

// UCIName reports whether s is a safe uci config/section/option identifier.
func UCIName(s string) bool { return len(s) <= 128 && uciName.MatchString(s) }

// Host reports whether s is a safe hostname or IP literal for ping and friends.
func Host(s string) bool { return len(s) <= 253 && hostName.MatchString(s) }

// Unit reports whether s is a safe init script name.
func Unit(s string) bool { return len(s) <= 128 && unitName.MatchString(s) }

// NoControl reports whether s contains no NUL, CR or LF characters.
func NoControl(s string) bool {
	return !strings.ContainsAny(s, "\x00\r\n")
}
