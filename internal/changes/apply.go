package changes

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/madushakv/owrt-tui/internal/shq"
)

// Mode selects how a change set is applied.
type Mode int

const (
	// CommitOnly stages, commits and reloads with no safety net.
	CommitOnly Mode = iota
	// WithRollback also starts a detached watchdog on the router that restores
	// the pre-change /etc/config unless a confirm marker appears in time.
	WithRollback
)

// StateDir is where snapshots and markers live on the router (RAM-backed).
const StateDir = "/tmp/owrt-tui"

var idRe = regexp.MustCompile(`^[a-z0-9-]{4,40}$`)

// Plan describes one apply operation.
type Plan struct {
	ID      string
	Mode    Mode
	Timeout int // seconds before rollback, WithRollback only
}

// NewPlan creates a plan with a time-based id.
func NewPlan(mode Mode, timeout int) Plan {
	if timeout <= 0 {
		timeout = 60
	}
	return Plan{ID: "chg-" + time.Now().UTC().Format("20060102-150405"), Mode: mode, Timeout: timeout}
}

// Script builds the shell script that applies the set. It is meant to be fed to
// "sh -s". Exit codes: 3 snapshot failed, 4 staging failed (reverted), 0 ok.
func (s *Set) Script(p Plan) (string, error) {
	if !idRe.MatchString(p.ID) {
		return "", fmt.Errorf("invalid plan id")
	}
	if s.Len() == 0 {
		return "", fmt.Errorf("nothing staged")
	}
	cfgs := s.Configs()
	for _, c := range cfgs {
		if !shq.UCIName(c) {
			return "", fmt.Errorf("invalid config name %q", c)
		}
	}
	list := strings.Join(cfgs, " ")
	d := StateDir + "/" + p.ID

	var b strings.Builder
	b.WriteString("D=" + shq.Quote(StateDir) + "\n")
	b.WriteString("SNAP=" + shq.Quote(d) + "\n")
	b.WriteString("mkdir -p \"$SNAP\" || exit 3\n")
	b.WriteString("cp -a /etc/config/. \"$SNAP\"/ || exit 3\n")
	for _, c := range s.ordered() {
		if err := c.Validate(); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s || { uci -q revert %s; echo 'owrt-tui: staging failed' >&2; exit 4; }\n", c.Command(), list)
	}
	if p.Mode == WithRollback {
		// The watchdog is a separate file started with nohup so it survives the
		// ssh session dropping when network settings change.
		fmt.Fprintf(&b, "cat > \"$SNAP.wd.sh\" <<'OWRT_TUI_WD'\n")
		fmt.Fprintf(&b, "sleep %d\n", p.Timeout)
		b.WriteString("if [ ! -e \"$SNAP.confirm\" ]; then\n")
		b.WriteString("  cp -a \"$SNAP\"/. /etc/config/ && reload_config\n")
		b.WriteString("  echo rolled-back > \"$SNAP.rolledback\"\n")
		b.WriteString("fi\n")
		b.WriteString("OWRT_TUI_WD\n")
		b.WriteString("nohup sh \"$SNAP.wd.sh\" </dev/null >/dev/null 2>&1 &\n")
	}
	fmt.Fprintf(&b, "uci commit %s || exit 5\n", list)
	b.WriteString("reload_config\n")
	b.WriteString("echo owrt-tui: applied\n")
	return b.String(), nil
}

var anonRef = regexp.MustCompile(`^@([A-Za-z0-9_]+)\[(-?\d+)\]$`)

// ordered returns the changes in the order they must run on the router.
//
// Sections that have no name are addressed by position, "@host[2]". Deleting
// one renumbers the ones after it, so a delete that ran early would make every
// later "@host[n]" in the same batch point at the wrong section. Edits and
// additions therefore run first, in the order they were staged, and whole
// section deletes run last, highest position first, so each delete leaves the
// positions of the remaining ones unchanged.
// Ordered returns the changes in the order the apply script runs them.
func (s *Set) Ordered() []Change { return s.ordered() }

func (s *Set) ordered() []Change {
	var first, deletes, moves []Change
	for _, c := range s.items {
		if c.Op == OpDelete && c.Option == "" {
			deletes = append(deletes, c)
		} else if c.Op == OpReorder {
			moves = append(moves, c)
		} else {
			first = append(first, c)
		}
	}
	pos := func(c Change) int {
		if m := anonRef.FindStringSubmatch(c.Section); m != nil {
			n, _ := strconv.Atoi(m[2])
			return n
		}
		return -1
	}
	sort.SliceStable(deletes, func(i, j int) bool {
		a, b := deletes[i], deletes[j]
		if a.Config != b.Config {
			return a.Config < b.Config
		}
		return pos(a) > pos(b)
	})
	return append(append(first, deletes...), moves...)
}

// ConfirmCommand returns the command that cancels a pending rollback.
func ConfirmCommand(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("invalid plan id")
	}
	return "touch " + shq.Quote(StateDir+"/"+id+".confirm") + " && echo confirmed", nil
}

// StatusCommand reports "confirmed", "rolledback" or "pending" for a plan.
func StatusCommand(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("invalid plan id")
	}
	base := shq.Quote(StateDir + "/" + id)
	// 'path'.suffix is valid shell: a quoted word followed by a bare suffix.
	return fmt.Sprintf("if [ -e %[1]s.rolledback ]; then echo rolledback; elif [ -e %[1]s.confirm ]; then echo confirmed; else echo pending; fi", base), nil
}

// RestoreCommand copies a snapshot back and reloads (manual undo after apply).
func RestoreCommand(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("invalid plan id")
	}
	d := shq.Quote(StateDir + "/" + id)
	return fmt.Sprintf("[ -d %s ] && cp -a %s/. /etc/config/ && reload_config && echo restored", d, d), nil
}
