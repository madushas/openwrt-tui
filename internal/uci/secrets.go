package uci

import (
	"regexp"
	"strings"
)

// MaskText is shown in place of a secret value.
const MaskText = "********"

var wepKey = regexp.MustCompile(`^key[1-4]$`)

// IsSecret reports whether an option holds a secret that must be masked by
// default: private and preshared keys, passwords, tokens and Wi-Fi passphrases.
// Public keys are not secret.
func IsSecret(key string) bool {
	k := strings.ToLower(key)
	switch {
	case k == "public_key" || strings.HasSuffix(k, "pubkey") || strings.HasSuffix(k, "public_key"):
		return false
	case k == "key" || wepKey.MatchString(k):
		return true // wifi-iface passphrase and WEP keys
	}
	for _, suf := range []string{"_key", "_password", "_passwd", "_secret", "_token", "_psk"} {
		if strings.HasSuffix(k, suf) {
			return true
		}
	}
	switch k {
	case "password", "passwd", "secret", "token", "psk", "auth_pass", "username_password":
		return true
	}
	return false
}

// Display returns the option value for screens and exports. Secrets are masked
// unless reveal is true; the masked form never depends on the real value, so it
// leaks neither content nor length.
func (o Option) Display(reveal bool) string {
	if !reveal && IsSecret(o.Key) {
		return MaskText
	}
	return o.Value()
}
