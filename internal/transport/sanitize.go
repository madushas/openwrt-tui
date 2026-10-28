package transport

import (
	"strings"
	"unicode/utf8"
)

// Sanitize removes characters that could do something to the user's terminal
// or mislead the eye when router output is shown: escape sequences (ESC and the
// other C0 controls), the C1 controls (which some terminals treat as the same
// sequences), DEL, and the bidirectional overrides used for look-alike text.
// Newline and tab are kept; CRLF becomes LF and a lone CR is dropped.
//
// Router output is not trusted text. Log lines carry names that strangers type
// (a failed SSH login attempt with a crafted user name), DHCP host names come
// from any device on the LAN, and a Wi-Fi network name is chosen by whoever
// runs the access point. Without this, any of them could rewrite the terminal
// title, move the cursor over earlier output or hide text.
func Sanitize(s string) string {
	clean := utf8.ValidString(s)
	if clean {
		for _, r := range s {
			if drop(r) {
				clean = false
				break
			}
		}
	}
	if clean {
		return s
	}
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !drop(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func drop(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069: // bidi embeddings, overrides, isolates
		return true
	case r == 0x200e || r == 0x200f || r == 0x061c: // direction marks
		return true
	}
	return false
}

// clean applies Sanitize to both streams of a result.
func (r Result) clean() Result {
	r.Stdout, r.Stderr = Sanitize(r.Stdout), Sanitize(r.Stderr)
	return r
}
