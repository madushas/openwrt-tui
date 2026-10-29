// Package history keeps what each apply changed, on the user's PC, so a change
// can be undone later and so changes made behind owrt-tui's back are noticed.
//
// The snapshots hold the real configuration (including Wi-Fi passwords and VPN
// keys), because that is what a restore needs. They are written only to a
// private directory (0700) as private files (0600) and never leave the PC.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Entry is one apply.
type Entry struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Summary []string  `json:"summary"` // masked, one line per change
	// Configs holds each touched config as it was BEFORE the change, in
	// "uci export" form. Unmasked.
	Configs map[string]string `json:"configs,omitempty"`
}

// Store is one router's history.
type Store struct{ dir string }

var (
	idRe   = regexp.MustCompile(`^[a-z0-9-]{4,40}$`)
	safeRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// Open prepares the directory for a router. router is any stable name, such
// as the host the session connects to.
func Open(root, router string) (*Store, error) {
	if root == "" {
		return nil, errors.New("no directory for history")
	}
	name := strings.Trim(safeRe.ReplaceAllString(router, "_"), "._")
	if name == "" {
		name = "router"
	}
	d := filepath.Join(root, "history", name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(d, 0o700)
	return &Store{dir: d}, nil
}

// Dir is where this store keeps its files.
func (s *Store) Dir() string { return s.dir }

func (s *Store) write(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.dir, name))
}

// Save stores an entry. It keeps the 100 most recent.
func (s *Store) Save(e Entry) error {
	if !idRe.MatchString(e.ID) {
		return fmt.Errorf("invalid history id %q", e.ID)
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if err := s.write(e.ID+".json", e); err != nil {
		return err
	}
	return s.Prune(100)
}

func (s *Store) read(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Load returns one entry with its snapshots.
func (s *Store) Load(id string) (Entry, error) {
	if !idRe.MatchString(id) {
		return Entry{}, fmt.Errorf("invalid history id %q", id)
	}
	var e Entry
	return e, s.read(id+".json", &e)
}

// List returns the entries, newest first, without their snapshots.
func (s *Store) List() ([]Entry, error) {
	files, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, f := range files {
		id := strings.TrimSuffix(f.Name(), ".json")
		if f.IsDir() || id == f.Name() || !idRe.MatchString(id) {
			continue
		}
		var e Entry
		if err := s.read(f.Name(), &e); err != nil {
			continue // a damaged file must not hide the rest
		}
		e.Configs = nil
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// Prune deletes all but the newest keep entries.
func (s *Store) Prune(keep int) error {
	l, err := s.List()
	if err != nil || len(l) <= keep {
		return err
	}
	for _, e := range l[keep:] {
		_ = os.Remove(filepath.Join(s.dir, e.ID+".json"))
	}
	return nil
}

// Baseline is the fingerprint of every config at the last moment owrt-tui
// knew the router's state (after an apply, or when it first connected).
type Baseline struct {
	Time   time.Time         `json:"time"`
	Hashes map[string]string `json:"hashes"`
}

// SaveBaseline records the known state.
func (s *Store) SaveBaseline(hashes map[string]string) error {
	return s.write("baseline.state", Baseline{Time: time.Now().UTC(), Hashes: hashes})
}

// LoadBaseline returns the recorded state, or ok=false if there is none.
func (s *Store) LoadBaseline() (Baseline, bool) {
	var b Baseline
	if err := s.read("baseline.state", &b); err != nil {
		return Baseline{}, false
	}
	return b, true
}
