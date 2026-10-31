package health

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Accepted records findings the user has marked "intended". Each entry stores
// a fingerprint of the finding's text, so when the configuration changes in a
// way that changes the finding, it comes back instead of staying hidden.
type Accepted map[string]string // finding ID -> fingerprint

// Fingerprint identifies the current wording and severity of a finding.
func Fingerprint(f Finding) string {
	h := sha256.Sum256([]byte(f.ID + "\x00" + f.Sev.String() + "\x00" + f.Title + "\x00" + f.Detail))
	return hex.EncodeToString(h[:6])
}

// Split separates findings into active ones and accepted ones.
func Split(list []Finding, acc Accepted) (active, accepted []Finding) {
	for _, f := range list {
		if fp, ok := acc[f.ID]; ok && fp == Fingerprint(f) {
			accepted = append(accepted, f)
		} else {
			active = append(active, f)
		}
	}
	return
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func acceptPath(dir, router string) (string, error) {
	if dir == "" {
		return "", errors.New("no configuration directory")
	}
	n := strings.Trim(unsafeName.ReplaceAllString(router, "_"), "._")
	if n == "" {
		n = "router"
	}
	return filepath.Join(dir, "accepted", n+".json"), nil
}

// LoadAccepted reads the router's accepted findings; a missing file is empty.
func LoadAccepted(dir, router string) Accepted {
	acc := Accepted{}
	p, err := acceptPath(dir, router)
	if err != nil {
		return acc
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &acc)
	}
	if acc == nil {
		acc = Accepted{}
	}
	return acc
}

// SaveAccepted writes the set privately (0600 file in a 0700 directory).
func SaveAccepted(dir, router string, acc Accepted) error {
	p, err := acceptPath(dir, router)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(acc, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
