// Package uci parses the output of "uci export" into a typed tree.
package uci

import (
	"fmt"
	"strings"
)

// Option is one option or list in a section. Lists keep every value.
type Option struct {
	Key    string
	Values []string
	IsList bool
}

// Value returns the scalar value (or values joined by space for lists).
func (o Option) Value() string { return strings.Join(o.Values, " ") }

// Section is a "config" block.
type Section struct {
	Type      string
	Name      string // empty for anonymous sections
	Anonymous bool
	Index     int // position among sections of the same type in this file
	Options   []Option
}

// Ref returns the uci reference for the section: its name, or @type[index].
func (s Section) Ref() string {
	if !s.Anonymous && s.Name != "" {
		return s.Name
	}
	return fmt.Sprintf("@%s[%d]", s.Type, s.Index)
}

// Option looks up an option by key.
func (s Section) Option(key string) (Option, bool) {
	for _, o := range s.Options {
		if o.Key == key {
			return o, true
		}
	}
	return Option{}, false
}

// Config is one /etc/config file.
type Config struct {
	Name     string
	Sections []Section
}

// Section finds a section by Ref().
func (c Config) Section(ref string) (Section, bool) {
	for _, s := range c.Sections {
		if s.Ref() == ref {
			return s, true
		}
	}
	return Section{}, false
}

// ParseExport parses "uci export" output containing one or more packages.
func ParseExport(text string) ([]Config, error) {
	var cfgs []Config
	var cur *Config
	typeCount := map[string]int{}

	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		toks, err := tokenize(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		if len(toks) == 0 {
			continue
		}
		switch toks[0] {
		case "package":
			if len(toks) != 2 {
				return nil, fmt.Errorf("line %d: bad package line", n+1)
			}
			cfgs = append(cfgs, Config{Name: toks[1]})
			cur = &cfgs[len(cfgs)-1]
			typeCount = map[string]int{}
		case "config":
			if cur == nil {
				return nil, fmt.Errorf("line %d: config before package", n+1)
			}
			if len(toks) < 2 {
				return nil, fmt.Errorf("line %d: bad config line", n+1)
			}
			s := Section{Type: toks[1], Index: typeCount[toks[1]]}
			typeCount[toks[1]]++
			if len(toks) >= 3 {
				s.Name = toks[2]
			} else {
				s.Anonymous = true
			}
			cur.Sections = append(cur.Sections, s)
		case "option", "list":
			if cur == nil || len(cur.Sections) == 0 {
				return nil, fmt.Errorf("line %d: %s outside a section", n+1, toks[0])
			}
			if len(toks) < 2 {
				return nil, fmt.Errorf("line %d: missing option name", n+1)
			}
			val := ""
			if len(toks) >= 3 {
				val = toks[2]
			}
			sec := &cur.Sections[len(cur.Sections)-1]
			if toks[0] == "list" {
				if i := indexOpt(sec.Options, toks[1]); i >= 0 {
					sec.Options[i].Values = append(sec.Options[i].Values, val)
				} else {
					sec.Options = append(sec.Options, Option{Key: toks[1], Values: []string{val}, IsList: true})
				}
			} else {
				sec.Options = append(sec.Options, Option{Key: toks[1], Values: []string{val}})
			}
		default:
			return nil, fmt.Errorf("line %d: unknown keyword %q", n+1, toks[0])
		}
	}
	return cfgs, nil
}

func indexOpt(opts []Option, key string) int {
	for i, o := range opts {
		if o.Key == key {
			return i
		}
	}
	return -1
}

// tokenize splits one line using the quoting that uci export emits: bare
// words, 'single quoted' text where a quote is written '\” , and "double".
func tokenize(line string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	have := false
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			if have {
				toks = append(toks, cur.String())
				cur.Reset()
				have = false
			}
			i++
		case c == '\'':
			have = true
			i++
			for i < len(line) && line[i] != '\'' {
				cur.WriteByte(line[i])
				i++
			}
			if i >= len(line) {
				return nil, fmt.Errorf("unterminated single quote")
			}
			i++
		case c == '"':
			have = true
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				cur.WriteByte(line[i])
				i++
			}
			if i >= len(line) {
				return nil, fmt.Errorf("unterminated double quote")
			}
			i++
		case c == '\\' && i+1 < len(line):
			have = true
			cur.WriteByte(line[i+1])
			i += 2
		default:
			have = true
			cur.WriteByte(c)
			i++
		}
	}
	if have {
		toks = append(toks, cur.String())
	}
	return toks, nil
}
