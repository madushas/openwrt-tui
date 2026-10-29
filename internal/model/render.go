package model

import (
	"fmt"
	"sort"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

func quote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

// Render writes one config in "uci export" form. With mask set, secret values
// are replaced, so the text is safe to store in a repository or show.
func Render(c uci.Config, mask bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n", c.Name)
	for _, s := range c.Sections {
		b.WriteString("\nconfig " + s.Type)
		if !s.Anonymous && s.Name != "" {
			b.WriteString(" " + quote(s.Name))
		}
		b.WriteString("\n")
		for _, o := range s.Options {
			for _, v := range o.Values {
				if mask && uci.IsSecret(o.Key) {
					v = uci.MaskText
				}
				kind := "option"
				if o.IsList {
					kind = "list"
				}
				fmt.Fprintf(&b, "\t%s %s %s\n", kind, o.Key, quote(v))
			}
		}
	}
	return b.String()
}

// Diff returns the changes that turn the config "from" into "to", for every
// config present in "to". Sections without a name are matched by position, so
// a restore reproduces the snapshot exactly; sections that exist only in the
// snapshot are re-created with generated names.
func Diff(from, to []uci.Config) []changes.Change {
	var out []changes.Change
	cur := map[string]uci.Config{}
	for _, c := range from {
		cur[c.Name] = c
	}
	for _, want := range to {
		have := cur[want.Name]
		used := map[string]bool{}
		for i, ws := range want.Sections {
			hs, ok := have.Section(ws.Ref())
			if !ok {
				name := ws.Name
				if ws.Anonymous || name == "" {
					name = fmt.Sprintf("restored_%s_%d", ws.Type, i)
				}
				out = append(out, changes.Change{Op: changes.OpAddSect, Config: want.Name, Section: name, Type: ws.Type})
				for _, o := range ws.Options {
					out = append(out, setOption(want.Name, name, nil, o)...)
				}
				continue
			}
			used[hs.Ref()] = true
			ref := hs.Ref()
			old := map[string]uci.Option{}
			for _, o := range hs.Options {
				old[o.Key] = o
			}
			seen := map[string]bool{}
			for _, o := range ws.Options {
				seen[o.Key] = true
				if h, ok := old[o.Key]; ok && sameOption(h, o) {
					continue
				}
				h, has := old[o.Key]
				var hp *uci.Option
				if has {
					hp = &h
				}
				out = append(out, setOption(want.Name, ref, hp, o)...)
			}
			for _, h := range hs.Options {
				if !seen[h.Key] {
					out = append(out, changes.Change{Op: changes.OpDelete, Config: want.Name, Section: ref, Option: h.Key, Old: h.Value()})
				}
			}
		}
		for _, hs := range have.Sections {
			if !used[hs.Ref()] && !hasRef(want, hs.Ref()) {
				out = append(out, changes.Change{Op: changes.OpDelete, Config: want.Name, Section: hs.Ref(), Old: hs.Type})
			}
		}
	}
	return out
}

func hasRef(c uci.Config, ref string) bool { _, ok := c.Section(ref); return ok }

func sameOption(a, b uci.Option) bool {
	if a.IsList != b.IsList || len(a.Values) != len(b.Values) {
		return false
	}
	for i := range a.Values {
		if a.Values[i] != b.Values[i] {
			return false
		}
	}
	return true
}

// setOption makes option o exist with its values. Lists are rebuilt (delete,
// then add each value) because "uci set" on a list would flatten it.
func setOption(cfg, sec string, old *uci.Option, o uci.Option) []changes.Change {
	var out []changes.Change
	if o.IsList {
		if old != nil {
			out = append(out, changes.Change{Op: changes.OpDelete, Config: cfg, Section: sec, Option: o.Key, Old: old.Value()})
		}
		for _, v := range o.Values {
			out = append(out, changes.Change{Op: changes.OpAddList, Config: cfg, Section: sec, Option: o.Key, Value: v})
		}
		return out
	}
	c := changes.Change{Op: changes.OpSet, Config: cfg, Section: sec, Option: o.Key, Value: o.Value()}
	if old != nil && !uci.IsSecret(o.Key) {
		c.Old = old.Value()
	}
	return []changes.Change{c}
}

// Hash returns a stable fingerprint of one config, for detecting that it was
// changed by something other than owrt-tui. Sections are compared in order.
func Hash(c uci.Config) string {
	return fingerprint(Render(c, false))
}

// Hashes fingerprints every config.
func Hashes(cs []uci.Config) map[string]string {
	out := map[string]string{}
	for _, c := range cs {
		out[c.Name] = Hash(c)
	}
	return out
}

// Changed lists the configs whose fingerprint differs from the baseline,
// including ones that appeared or vanished.
func Changed(baseline, now map[string]string) []string {
	var out []string
	for k, v := range now {
		if b, ok := baseline[k]; !ok || b != v {
			out = append(out, k)
		}
	}
	for k := range baseline {
		if _, ok := now[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
