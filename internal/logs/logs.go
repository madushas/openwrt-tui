// Package logs reads and filters the OpenWrt system log (logread).
package logs

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/transport"
)

// Entry is one parsed logread line. Lines that do not match the usual format
// keep their text in Msg and Raw with empty metadata, so nothing is dropped.
type Entry struct {
	Time     string
	Facility string
	Severity string
	Proc     string
	PID      int
	Msg      string
	Raw      string
}

var lineRe = regexp.MustCompile(`^(\w{3} \w{3} +\d{1,2} \d{2}:\d{2}:\d{2} \d{4}) (\w+)\.(\w+) ([^\s:\[]+)(?:\[(\d+)\])?: ?(.*)$`)

// ParseLine parses one logread line.
func ParseLine(s string) Entry {
	m := lineRe.FindStringSubmatch(s)
	if m == nil {
		return Entry{Msg: s, Raw: s}
	}
	pid, _ := strconv.Atoi(m[5])
	return Entry{Time: m[1], Facility: m[2], Severity: m[3], Proc: m[4], PID: pid, Msg: m[6], Raw: s}
}

var rank = map[string]int{
	"emerg": 0, "alert": 1, "crit": 2, "err": 3, "warn": 4, "warning": 4,
	"notice": 5, "info": 6, "debug": 7,
}

// ValidSeverity reports whether s is a severity name logread can print.
func ValidSeverity(s string) bool {
	_, ok := rank[s]
	return ok
}

// Rank returns 0 (emerg) to 7 (debug). Unparsed entries rank as info.
func (e Entry) Rank() int {
	if r, ok := rank[e.Severity]; ok {
		return r
	}
	return 6
}

// Filter selects entries. Zero values match everything.
type Filter struct {
	MinSeverity string         // show this severity and worse, e.g. "warn"
	Proc        string         // case-insensitive substring of the process name
	Contains    string         // case-insensitive substring of the whole line
	Pattern     *regexp.Regexp // optional regexp on the whole line
}

// Match reports whether e passes the filter.
func (f Filter) Match(e Entry) bool {
	if f.MinSeverity != "" {
		if max, ok := rank[f.MinSeverity]; ok && e.Rank() > max {
			return false
		}
	}
	if f.Proc != "" && !strings.Contains(strings.ToLower(e.Proc), strings.ToLower(f.Proc)) {
		return false
	}
	if f.Contains != "" && !strings.Contains(strings.ToLower(e.Raw), strings.ToLower(f.Contains)) {
		return false
	}
	if f.Pattern != nil && !f.Pattern.MatchString(e.Raw) {
		return false
	}
	return true
}

// Apply returns the entries that pass f.
func (f Filter) Apply(in []Entry) []Entry {
	var out []Entry
	for _, e := range in {
		if f.Match(e) {
			out = append(out, e)
		}
	}
	return out
}

// Fetch returns the last n log lines (n is clamped to 1..5000).
func Fetch(ctx context.Context, t transport.Transport, n int) ([]Entry, error) {
	n = clamp(n)
	res, err := t.Run(ctx, fmt.Sprintf("logread -l %d", n), nil)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("logread failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	var out []Entry
	for _, l := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, ParseLine(l))
		}
	}
	return out, nil
}

// Follow prints the last backlog lines and then new lines as they arrive, until
// ctx is cancelled (which returns ctx.Err()).
func Follow(ctx context.Context, t transport.Transport, backlog int, onEntry func(Entry)) error {
	cmd := fmt.Sprintf("logread -l %d -f", clamp(backlog))
	return transport.Stream(ctx, t, cmd, func(l string) {
		if strings.TrimSpace(l) != "" {
			onEntry(ParseLine(l))
		}
	})
}

func clamp(n int) int {
	switch {
	case n < 1:
		return 1
	case n > 5000:
		return 5000
	}
	return n
}
