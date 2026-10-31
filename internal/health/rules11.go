package health

import (
	"fmt"
	"sort"
	"strings"
)

// ruleDanglingForward: a forwarding whose src or dest names no zone is
// silently ignored by fw4, so the traffic it was meant to allow is dropped.
// Seen on a real router where "src direct" pointed at the zone "direct_zone".
func ruleDanglingForward(in Input) []Finding {
	fw, ok := cfg(in, "firewall")
	if !ok {
		return nil
	}
	zones := map[string]bool{}
	for _, s := range fw.Sections {
		if s.Type == "zone" {
			zones[opt(s, "name")] = true
		}
	}
	var out []Finding
	for _, s := range fw.Sections {
		if s.Type != "forwarding" {
			continue
		}
		for _, k := range []string{"src", "dest"} {
			if v := opt(s, k); v != "" && !zones[v] {
				out = append(out, Finding{
					ID: "fwd-dangling:" + s.Ref() + ":" + k, Sev: Warn,
					Title:  fmt.Sprintf("forwarding %s uses unknown zone '%s'", s.Ref(), v),
					Detail: fmt.Sprintf("Its %s is '%s' but no zone has that name, so the rule does nothing. Zones are: %s.", k, v, joinKeys(zones)),
				})
			}
		}
	}
	return out
}

// ruleDupPolicy: two pbr policies with the same match and target are
// redundant, and a later edit to only one of them is confusing.
func ruleDupPolicy(in Input) []Finding {
	c, ok := cfg(in, "pbr")
	if !ok {
		return nil
	}
	seen := map[string]string{}
	var out []Finding
	for _, s := range c.Sections {
		if s.Type != "policy" || opt(s, "enabled") == "0" {
			continue
		}
		key := strings.Join([]string{opt(s, "src_addr"), opt(s, "src_port"), opt(s, "dest_addr"),
			opt(s, "dest_port"), opt(s, "proto"), opt(s, "interface")}, "|")
		label := opt(s, "name")
		if label == "" {
			label = s.Ref()
		}
		if first, dup := seen[key]; dup {
			out = append(out, Finding{
				ID: "pbr-dup:" + s.Ref(), Sev: Info,
				Title:  fmt.Sprintf("pbr policies '%s' and '%s' are identical", first, label),
				Detail: "They match the same traffic and send it to the same interface. One of them can be removed (Config > pbr).",
			})
			continue
		}
		seen[key] = label
	}
	return out
}

func joinKeys(m map[string]bool) string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return strings.Join(k, ", ")
}
