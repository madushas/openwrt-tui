// Package model derives what the router's configuration will look like once the
// staged changes are applied, so checks and screens can reason about the result
// without touching the router.
package model

import (
	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
	"strconv"
)

// Overlay returns a copy of cfgs with the staged changes applied in the order
// the apply script runs them. The input is never modified.
func Overlay(cfgs []uci.Config, set *changes.Set) []uci.Config {
	out := clone(cfgs)
	for _, c := range set.Ordered() {
		out = apply(out, c)
	}
	return out
}

func clone(in []uci.Config) []uci.Config {
	out := make([]uci.Config, len(in))
	for i, c := range in {
		out[i] = uci.Config{Name: c.Name, Sections: make([]uci.Section, len(c.Sections))}
		for j, s := range c.Sections {
			ns := s
			ns.Options = make([]uci.Option, len(s.Options))
			for k, o := range s.Options {
				ns.Options[k] = uci.Option{Key: o.Key, IsList: o.IsList, Values: append([]string(nil), o.Values...)}
			}
			out[i].Sections[j] = ns
		}
	}
	return out
}

func reindex(c *uci.Config) {
	n := map[string]int{}
	for i := range c.Sections {
		c.Sections[i].Index = n[c.Sections[i].Type]
		n[c.Sections[i].Type]++
	}
}

func apply(cfgs []uci.Config, ch changes.Change) []uci.Config {
	ci := -1
	for i := range cfgs {
		if cfgs[i].Name == ch.Config {
			ci = i
		}
	}
	if ci < 0 {
		if ch.Op != changes.OpAddSect {
			return cfgs
		}
		cfgs = append(cfgs, uci.Config{Name: ch.Config})
		ci = len(cfgs) - 1
	}
	c := &cfgs[ci]
	si := -1
	for i, s := range c.Sections {
		if s.Ref() == ch.Section {
			si = i
		}
	}
	if ch.Op == changes.OpAddSect {
		if si < 0 {
			c.Sections = append(c.Sections, uci.Section{Type: ch.Type, Name: ch.Section})
			reindex(c)
		}
		return cfgs
	}
	if si < 0 {
		return cfgs
	}
	if ch.Op == changes.OpReorder {
		pos, err := strconv.Atoi(ch.Value)
		if err != nil {
			return cfgs
		}
		if pos >= len(c.Sections) {
			pos = len(c.Sections) - 1
		}
		sec := c.Sections[si]
		rest := append(append([]uci.Section{}, c.Sections[:si]...), c.Sections[si+1:]...)
		c.Sections = append(append(append([]uci.Section{}, rest[:pos]...), sec), rest[pos:]...)
		reindex(c)
		return cfgs
	}
	s := &c.Sections[si]
	oi := -1
	for i, o := range s.Options {
		if o.Key == ch.Option {
			oi = i
		}
	}
	switch ch.Op {
	case changes.OpSet:
		if oi >= 0 {
			s.Options[oi] = uci.Option{Key: ch.Option, Values: []string{ch.Value}}
		} else {
			s.Options = append(s.Options, uci.Option{Key: ch.Option, Values: []string{ch.Value}})
		}
	case changes.OpAddList:
		if oi >= 0 {
			s.Options[oi].IsList = true
			s.Options[oi].Values = append(s.Options[oi].Values, ch.Value)
		} else {
			s.Options = append(s.Options, uci.Option{Key: ch.Option, IsList: true, Values: []string{ch.Value}})
		}
	case changes.OpDelList:
		if oi >= 0 {
			var keep []string
			for _, v := range s.Options[oi].Values {
				if v != ch.Value {
					keep = append(keep, v)
				}
			}
			if len(keep) == 0 {
				s.Options = append(s.Options[:oi], s.Options[oi+1:]...)
			} else {
				s.Options[oi].Values = keep
			}
		}
	case changes.OpDelete:
		if ch.Option == "" {
			c.Sections = append(c.Sections[:si], c.Sections[si+1:]...)
			reindex(c)
		} else if oi >= 0 {
			s.Options = append(s.Options[:oi], s.Options[oi+1:]...)
		}
	}
	return cfgs
}
