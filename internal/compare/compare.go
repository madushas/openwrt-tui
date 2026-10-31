// Package compare finds the differences between two routers' configurations.
// It is read-only and never prints a secret: where a secret differs it says so
// without showing either value.
package compare

import (
	"fmt"
	"sort"
	"strings"

	"github.com/madushakv/owrt-tui/internal/uci"
)

// Kind says which side a difference is on.
type Kind int

const (
	OnlyA  Kind = iota // present on A only
	OnlyB              // present on B only
	Differ             // present on both with different values
)

// Diff is one difference. Option is empty when a whole section differs.
type Diff struct {
	Kind    Kind
	Config  string
	Section string // ref, e.g. "lan" or "@rule[3]"
	Type    string
	Option  string
	A, B    string // display values, masked for secrets
}

// Options tune the comparison.
type Options struct {
	Configs     []string // only these config files; empty = all
	IgnoreKeys  map[string]bool
	IgnoreFiles map[string]bool
}

// DefaultOptions skips values that differ between any two devices by nature.
func DefaultOptions() Options {
	return Options{
		IgnoreKeys:  map[string]bool{"macaddr": true, "ula_prefix": true, "path": true, "country": false},
		IgnoreFiles: map[string]bool{"ubootenv": true, "luci": true, "rpcd": true},
	}
}

func key(s uci.Section) string {
	if !s.Anonymous && s.Name != "" {
		return "n:" + s.Name
	}
	return fmt.Sprintf("a:%s:%d", s.Type, s.Index)
}

func val(o uci.Option) string {
	if o.IsList {
		return "[" + strings.Join(o.Values, ", ") + "]"
	}
	return o.Value()
}

func shown(o uci.Option) string {
	if uci.IsSecret(o.Key) {
		return uci.MaskText
	}
	return val(o)
}

// Compare returns the differences, ordered by config, section and option.
func Compare(a, b []uci.Config, opt Options) []Diff {
	byName := func(cs []uci.Config) map[string]uci.Config {
		m := map[string]uci.Config{}
		for _, c := range cs {
			m[c.Name] = c
		}
		return m
	}
	am, bm := byName(a), byName(b)
	names := map[string]bool{}
	for n := range am {
		names[n] = true
	}
	for n := range bm {
		names[n] = true
	}
	want := map[string]bool{}
	for _, c := range opt.Configs {
		want[c] = true
	}
	var order []string
	for n := range names {
		if opt.IgnoreFiles[n] || (len(want) > 0 && !want[n]) {
			continue
		}
		order = append(order, n)
	}
	sort.Strings(order)

	var out []Diff
	for _, n := range order {
		out = append(out, compareConfig(n, am[n], bm[n], opt)...)
	}
	return out
}

func compareConfig(name string, a, b uci.Config, opt Options) []Diff {
	as, bs := map[string]uci.Section{}, map[string]uci.Section{}
	var keys []string
	seen := map[string]bool{}
	for _, s := range a.Sections {
		as[key(s)] = s
		if !seen[key(s)] {
			seen[key(s)] = true
			keys = append(keys, key(s))
		}
	}
	for _, s := range b.Sections {
		bs[key(s)] = s
		if !seen[key(s)] {
			seen[key(s)] = true
			keys = append(keys, key(s))
		}
	}
	var out []Diff
	for _, k := range keys {
		sa, oka := as[k]
		sb, okb := bs[k]
		switch {
		case oka && !okb:
			out = append(out, Diff{Kind: OnlyA, Config: name, Section: sa.Ref(), Type: sa.Type})
		case !oka && okb:
			out = append(out, Diff{Kind: OnlyB, Config: name, Section: sb.Ref(), Type: sb.Type})
		default:
			out = append(out, compareSection(name, sa, sb, opt)...)
		}
	}
	return out
}

func compareSection(cfg string, a, b uci.Section, opt Options) []Diff {
	ao, bo := map[string]uci.Option{}, map[string]uci.Option{}
	var keys []string
	seen := map[string]bool{}
	for _, o := range a.Options {
		ao[o.Key] = o
		seen[o.Key] = true
		keys = append(keys, o.Key)
	}
	for _, o := range b.Options {
		bo[o.Key] = o
		if !seen[o.Key] {
			keys = append(keys, o.Key)
		}
	}
	sort.Strings(keys)
	var out []Diff
	for _, k := range keys {
		if opt.IgnoreKeys[k] {
			continue
		}
		x, okx := ao[k]
		y, oky := bo[k]
		d := Diff{Config: cfg, Section: a.Ref(), Type: a.Type, Option: k}
		switch {
		case okx && !oky:
			d.Kind, d.A = OnlyA, shown(x)
		case !okx && oky:
			d.Kind, d.B = OnlyB, shown(y)
		case val(x) != val(y):
			d.Kind, d.A, d.B = Differ, shown(x), shown(y)
		default:
			continue
		}
		out = append(out, d)
	}
	return out
}

// Format renders the differences as text. la and lb label the two routers.
func Format(diffs []Diff, la, lb string) string {
	if len(diffs) == 0 {
		return "no differences\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "< only on %s   > only on %s   ~ differs\n", la, lb)
	last := ""
	for _, d := range diffs {
		head := d.Config + "." + d.Section
		if head != last {
			fmt.Fprintf(&b, "\n%s  (%s)\n", head, d.Type)
			last = head
		}
		switch {
		case d.Option == "" && d.Kind == OnlyA:
			fmt.Fprintf(&b, "  < whole section only on %s\n", la)
		case d.Option == "" && d.Kind == OnlyB:
			fmt.Fprintf(&b, "  > whole section only on %s\n", lb)
		case d.Kind == OnlyA:
			fmt.Fprintf(&b, "  < %s = %s\n", d.Option, d.A)
		case d.Kind == OnlyB:
			fmt.Fprintf(&b, "  > %s = %s\n", d.Option, d.B)
		default:
			fmt.Fprintf(&b, "  ~ %s: %s  ->  %s\n", d.Option, d.A, d.B)
		}
	}
	return b.String()
}
