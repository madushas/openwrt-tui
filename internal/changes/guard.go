package changes

// Risk is how likely a staged change is to cut off the management session.
type Risk int

const (
	RiskNone Risk = iota
	RiskLow
	RiskMedium
	RiskHigh
)

func (r Risk) String() string {
	return [...]string{"none", "low", "medium", "high"}[r]
}

// Finding explains one risky change.
type Finding struct {
	Risk   Risk
	Change Change
	Why    string
}

// networkCritical lists network options that decide whether we stay reachable.
var networkCritical = map[string]bool{
	"proto": true, "ipaddr": true, "netmask": true, "gateway": true,
	"device": true, "ports": true, "disabled": true, "auto": true, "type": true,
}

// Guard inspects the staged set for edits that could lock the admin out. It is
// deliberately conservative: it cannot know which interface the SSH session
// uses, so any network, firewall or ssh daemon edit counts.
func (s *Set) Guard() (Risk, []Finding) {
	var out []Finding
	worst := RiskNone
	add := func(r Risk, c Change, why string) {
		out = append(out, Finding{r, c, why})
		if r > worst {
			worst = r
		}
	}
	for _, c := range s.items {
		switch c.Config {
		case "network":
			if c.Op == OpDelete && c.Option == "" {
				add(RiskHigh, c, "deleting a network section can remove the interface you are connected through")
			} else if networkCritical[c.Option] {
				add(RiskHigh, c, "changes how the router is addressed or bridged; your session may drop")
			} else {
				add(RiskMedium, c, "network edit; the network service restarts on apply")
			}
		case "dropbear":
			add(RiskHigh, c, "SSH daemon setting; a bad value can lock you out")
		case "firewall":
			add(RiskMedium, c, "firewall edit; a wrong rule can block management traffic")
		case "uhttpd", "wireless", "dhcp":
			add(RiskLow, c, "service restarts on apply")
		}
	}
	return worst, out
}

// RecommendedMode returns the apply mode the UI should preselect.
func (s *Set) RecommendedMode() Mode {
	r, _ := s.Guard()
	if r >= RiskMedium {
		return WithRollback
	}
	return CommitOnly
}
