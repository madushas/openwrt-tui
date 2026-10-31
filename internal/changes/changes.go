// Package changes holds the staged change set: nothing is written to the router
// until the user reviews the diff and applies it.
package changes

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/shq"
)

// Op is the kind of staged edit.
type Op string

const (
	OpSet     Op = "set"      // set option value
	OpAddList Op = "add_list" // append a list value
	OpDelList Op = "del_list" // remove a list value
	OpDelete  Op = "delete"   // delete an option, or a whole section when Option is empty
	OpAddSect Op = "add"      // create a named section of Type
	// OpReorder moves a section to a position (0-based, among all sections of
	// the config file) with Value holding the position. Order matters for
	// firewall rules.
	OpReorder Op = "reorder"
)

// Change is one staged edit.
type Change struct {
	Op      Op
	Config  string
	Section string // section ref: name or @type[n]
	Option  string
	Value   string
	Type    string // for OpAddSect
	Old     string // previous value, for the diff display only
}

// Validate checks every identifier that will be interpolated into a script.
func (c Change) Validate() error {
	if !shq.UCIName(c.Config) || !shq.UCIName(c.Section) {
		return fmt.Errorf("invalid config or section name")
	}
	if c.Option != "" && !shq.UCIName(c.Option) {
		return fmt.Errorf("invalid option name %q", c.Option)
	}
	if !shq.NoControl(c.Value) {
		return fmt.Errorf("value may not contain control characters or newlines")
	}
	switch c.Op {
	case OpSet, OpAddList, OpDelList:
		if c.Option == "" {
			return fmt.Errorf("%s needs an option", c.Op)
		}
	case OpAddSect:
		if !shq.UCIName(c.Type) {
			return fmt.Errorf("invalid section type")
		}
	case OpDelete:
	case OpReorder:
		if n, err := strconv.Atoi(c.Value); err != nil || n < 0 || n > 10000 || c.Option != "" {
			return fmt.Errorf("reorder needs a section and a position 0-10000")
		}
	default:
		return fmt.Errorf("unknown op %q", c.Op)
	}
	return nil
}

// path returns config.section[.option].
func (c Change) path() string {
	p := c.Config + "." + c.Section
	if c.Option != "" {
		p += "." + c.Option
	}
	return p
}

// Command returns the uci shell command for this change.
func (c Change) Command() string {
	switch c.Op {
	case OpSet:
		return "uci set " + shq.Quote(c.path()+"="+c.Value)
	case OpAddList:
		return "uci add_list " + shq.Quote(c.path()+"="+c.Value)
	case OpDelList:
		return "uci del_list " + shq.Quote(c.path()+"="+c.Value)
	case OpDelete:
		return "uci delete " + shq.Quote(c.path())
	case OpAddSect:
		return "uci set " + shq.Quote(c.Config+"."+c.Section+"="+c.Type)
	case OpReorder:
		return "uci reorder " + shq.Quote(c.Config+"."+c.Section+"="+c.Value)
	}
	return ""
}

// Describe returns a one-line human diff.
func (c Change) Describe() string {
	switch c.Op {
	case OpSet:
		if c.Old == "" {
			return fmt.Sprintf("%s  + %s", c.path(), c.Value)
		}
		return fmt.Sprintf("%s  %s -> %s", c.path(), c.Old, c.Value)
	case OpAddList:
		return fmt.Sprintf("%s  + list %s", c.path(), c.Value)
	case OpDelList:
		return fmt.Sprintf("%s  - list %s", c.path(), c.Value)
	case OpDelete:
		if c.Old != "" {
			return fmt.Sprintf("%s  - %s", c.path(), c.Old)
		}
		return fmt.Sprintf("%s  deleted", c.path())
	case OpAddSect:
		return fmt.Sprintf("%s.%s  + new %s section", c.Config, c.Section, c.Type)
	case OpReorder:
		return fmt.Sprintf("%s.%s  moved to position %s", c.Config, c.Section, c.Value)
	}
	return c.path()
}

// Set is an ordered, de-duplicated list of staged changes.
type Set struct{ items []Change }

// Add stages a change. A later set/delete of the same target replaces the
// earlier one, and setting a value back to its original removes the entry.
func (s *Set) Add(c Change) error {
	if err := c.Validate(); err != nil {
		return err
	}
	// Anonymous sections are addressed by position ("@rule[3]"), and a reorder
	// changes positions, so a reorder may not be mixed with other edits to the
	// same file: apply one first.
	for _, e := range s.items {
		if e.Config != c.Config {
			continue
		}
		if c.Op == OpReorder && e.Op == OpReorder && e.Section != c.Section {
			return fmt.Errorf("%s already has a staged reorder; apply it before moving another section", c.Config)
		}
		if c.Op == OpReorder && e.Op != OpReorder {
			return fmt.Errorf("%s has other staged changes; apply or clear them before reordering", c.Config)
		}
		if c.Op != OpReorder && e.Op == OpReorder {
			return fmt.Errorf("%s has a staged reorder; apply or clear it before making other changes there", c.Config)
		}
	}
	if c.Op == OpReorder {
		for i, e := range s.items {
			if e.Op == OpReorder && e.Config == c.Config && e.Section == c.Section {
				s.items = append(s.items[:i], s.items[i+1:]...)
				break
			}
		}
	}
	if c.Op == OpSet || c.Op == OpDelete {
		for i, e := range s.items {
			if (e.Op == OpSet || e.Op == OpDelete) && e.Config == c.Config && e.Section == c.Section && e.Option == c.Option {
				if c.Old == "" {
					c.Old = e.Old // keep the original value for the diff
				}
				s.items = append(s.items[:i], s.items[i+1:]...)
				break
			}
		}
		if c.Op == OpSet && c.Old != "" && c.Old == c.Value {
			return nil // reverted to original
		}
	}
	s.items = append(s.items, c)
	return nil
}

// Remove discards the i-th staged change.
func (s *Set) Remove(i int) {
	if i >= 0 && i < len(s.items) {
		s.items = append(s.items[:i], s.items[i+1:]...)
	}
}

// Clear discards everything.
func (s *Set) Clear() { s.items = nil }

// Len returns the number of staged changes.
func (s *Set) Len() int { return len(s.items) }

// Items returns a copy of the staged changes.
func (s *Set) Items() []Change { return append([]Change(nil), s.items...) }

// Configs returns the sorted, unique config names touched.
func (s *Set) Configs() []string {
	seen := map[string]bool{}
	for _, c := range s.items {
		seen[c.Config] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Pending reports whether the given option has a staged change, returning it.
func (s *Set) Pending(config, section, option string) (Change, bool) {
	for i := len(s.items) - 1; i >= 0; i-- {
		c := s.items[i]
		if c.Config == config && c.Section == section && c.Option == option {
			return c, true
		}
	}
	return Change{}, false
}

// Diff returns the human-readable diff, one line per change.
func (s *Set) Diff() string {
	var b strings.Builder
	for _, c := range s.items {
		b.WriteString(c.Describe())
		b.WriteByte('\n')
	}
	return b.String()
}
