package tui

import (
	"os"
	"strings"
)

// asciiOn switches the whole UI to plain 7-bit ASCII, for the Linux console,
// PuTTY with a non-UTF-8 character set, and other terminals without the glyphs.
var asciiOn = detectASCII()

// SetIcons forces a mode: "ascii", "unicode" or "auto"/"" (detect).
func SetIcons(mode string) {
	switch strings.ToLower(mode) {
	case "ascii":
		asciiOn = true
	case "unicode":
		asciiOn = false
	default:
		asciiOn = detectASCII()
	}
}

// detectASCII honours OWRT_TUI_ICONS, then falls back to ASCII when the locale
// is not UTF-8 or the terminal is the bare Linux console / dumb.
func detectASCII() bool {
	switch strings.ToLower(os.Getenv("OWRT_TUI_ICONS")) {
	case "ascii":
		return true
	case "unicode":
		return false
	}
	switch os.Getenv("TERM") {
	case "linux", "dumb", "vt100", "vt220":
		return true
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			u := strings.ToUpper(v)
			return !(strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8"))
		}
	}
	return false // unknown: assume a modern terminal
}

var asciiRepl = strings.NewReplacer(
	"─", "-", "━", "-", "│", "|", "┃", "|", "╭", "+", "╮", "+", "╰", "+", "╯", "+",
	"┌", "+", "┐", "+", "└", "+", "┘", "+", "├", "+", "┤", "+", "┬", "+", "┴", "+", "┼", "+",
	"●", "*", "○", "o", "◌", "o", "▸", ">", "▶", ">", "◀", "<", "•", "*", "·", ".", "…", "~",
	"✓", "v", "✗", "x", "✔", "v", "✘", "x", "★", "*", "↑", "^", "↓", "v", "▲", "^", "▼", "v",
	"❚", "|", "▏", "|", "±", "+", "°", "o", "–", "-", "—", "-", "‘", "'", "’", "'", "“", `"`, "”", `"`,
	"█", "#", "▇", "#", "▆", "=", "▅", "=", "▄", "-", "▃", "-", "▂", ".", "▁", "_", "░", ".", "▒", ":", "▓", "#",
	"→", ">", "←", "<", "↦", ">", "↺", "<",
)

// finish applies the ASCII fallback to a rendered frame. Layout is unchanged:
// every replaced glyph is one column wide and becomes one ASCII character
// (the few that expand are only used in short labels). Anything still
// non-ASCII, such as a Unicode SSID, becomes '?' outside escape sequences.
func finish(s string) string {
	if !asciiOn {
		return s
	}
	s = asciiRepl.Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r > 0x7f {
			b.WriteByte('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
