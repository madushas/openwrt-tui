// Package accepted remembers which Health findings the user has marked as
// intended, per router. An accepted finding is tied to a fingerprint of its
// text, so when the configuration changes in a way that changes the finding it
// is shown again instead of staying hidden.
package accepted

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/madushakv/owrt-tui/internal/health"
)

// Fingerprint identifies the finding as it reads now.
func Fingerprint(f health.Finding) string {
	h := sha256.Sum256([]byte(f.ID + "\x00" + f.Title + "\x00" + f.Detail))
	return hex.EncodeToString(h[:8])
}

// Store keeps one JSON file per router under Dir.
type Store struct{ Dir string }

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s Store) path(router string) string {
	n := strings.Trim(unsafe.ReplaceAllString(router, "_"), "._")
	if n == "" {
		n = "router"
	}
	return filepath.Join(s.Dir, n+".json")
}

// Load returns finding ID -> fingerprint. A missing or damaged file is empty.
func (s Store) Load(router string) map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(s.path(router))
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m
}

func (s Store) save(router string, m map[string]string) error {
	if s.Dir == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := s.path(router) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(router))
}

// Accept marks the finding as intended.
func (s Store) Accept(router string, f health.Finding) error {
	m := s.Load(router)
	m[f.ID] = Fingerprint(f)
	return s.save(router, m)
}

// Forget removes the acceptance.
func (s Store) Forget(router, id string) error {
	m := s.Load(router)
	delete(m, id)
	return s.save(router, m)
}

// Split separates findings into those still needing attention and those the
// user accepted (and whose text has not changed since).
func Split(all []health.Finding, acc map[string]string) (active, done []health.Finding) {
	for _, f := range all {
		if fp, ok := acc[f.ID]; ok && fp == Fingerprint(f) {
			done = append(done, f)
		} else {
			active = append(active, f)
		}
	}
	return
}
