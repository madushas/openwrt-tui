// Package profiles remembers routers by name, so "owrt-tui -profile office"
// replaces typing user@host, a key path and a port. Only connection details
// are stored; never passwords or router configuration.
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Profile is one saved router.
type Profile struct {
	Name     string `json:"name"`
	Target   string `json:"target"` // user@host or an ssh_config alias
	Identity string `json:"identity,omitempty"`
	Port     string `json:"port,omitempty"`
	Note     string `json:"note,omitempty"`
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// ErrNotFound is returned for an unknown profile name.
var ErrNotFound = errors.New("no such profile")

// Store is a profiles file.
type Store struct{ Path string }

// DefaultPath is OWRT_TUI_PROFILES, else <user config dir>/owrt-tui/profiles.json.
func DefaultPath() (string, error) {
	if p := os.Getenv("OWRT_TUI_PROFILES"); p != "" {
		return p, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "owrt-tui", "profiles.json"), nil
}

// Open returns the default store.
func Open() (*Store, error) {
	p, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return &Store{Path: p}, nil
}

// List returns the profiles sorted by name. A missing file is an empty list.
func (s *Store) List() ([]Profile, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps []Profile
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps, nil
}

// Get finds one profile.
func (s *Store) Get(name string) (Profile, error) {
	ps, err := s.List()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("%w: %q", ErrNotFound, name)
}

// Validate checks a profile before it is saved.
func Validate(p Profile) error {
	if !nameRe.MatchString(p.Name) {
		return fmt.Errorf("profile name %q: use letters, digits, '.', '_' or '-' (up to 32)", p.Name)
	}
	if p.Target == "" || p.Target[0] == '-' {
		return fmt.Errorf("target %q is not a host (it must not start with '-')", p.Target)
	}
	for _, r := range p.Target + p.Identity + p.Port {
		if r < 0x20 || r == 0x7f {
			return errors.New("control characters are not allowed")
		}
	}
	return nil
}

// Put adds or replaces a profile.
func (s *Store) Put(p Profile) error {
	if err := Validate(p); err != nil {
		return err
	}
	ps, err := s.List()
	if err != nil {
		return err
	}
	replaced := false
	for i := range ps {
		if ps[i].Name == p.Name {
			ps[i], replaced = p, true
		}
	}
	if !replaced {
		ps = append(ps, p)
	}
	return s.write(ps)
}

// Remove deletes a profile.
func (s *Store) Remove(name string) error {
	ps, err := s.List()
	if err != nil {
		return err
	}
	out := ps[:0]
	found := false
	for _, p := range ps {
		if p.Name == name {
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return s.write(out)
}

func (s *Store) write(ps []Profile) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
