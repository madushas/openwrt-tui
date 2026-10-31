// Package health inspects the router's configuration and a few runtime facts
// and reports problems and risky settings. Where a safe fix exists it is
// expressed as staged changes, so applying it goes through preflight,
// rollback and smart confirm like anything else. It never talks to the router.
package health

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/services"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// Severity orders findings.
type Severity int

const (
	Info Severity = iota
	Warn
	Bad
)

func (s Severity) String() string { return [...]string{"note", "warning", "problem"}[s] }

// Finding is one thing worth the user's attention.
type Finding struct {
	ID     string
	Sev    Severity
	Title  string
	Detail string
	// Fix, when present, is the change set that resolves the finding.
	Fix []changes.Change
	// FixNote says what the fix does, shown beside the key.
	FixNote string
}

// Facts are runtime observations; every field is optional.
type Facts struct {
	OverlayFreeKB  int // 0 = unknown
	OverlayUsedPct int
	SSHKeys        int // keys in /etc/dropbear/authorized_keys; -1 = unknown
	Cron           []string
	Services       []services.Service
	Release        string

	// Phase 9 observations.
	TempC      float64 // hottest thermal zone; valid when TempOK
	TempOK     bool
	WGKnown    bool // wg is installed, so Handshakes is meaningful
	Now        int64
	Handshakes []Handshake
	LTE        *LTE
}

// Input is what the rules look at.
type Input struct {
	Configs []uci.Config
	Facts   Facts
}

// Check runs every rule. Findings are most severe first, then by ID.
func Check(in Input) []Finding {
	var out []Finding
	for _, r := range rules {
		out = append(out, r(in)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sev != out[j].Sev {
			return out[i].Sev > out[j].Sev
		}
		return out[i].ID < out[j].ID
	})
	return out
}

var rules = []func(Input) []Finding{
	ruleDupList, ruleMultiZone, ruleOpenZone, ruleWanExposed, ruleCountry,
	ruleSSH, ruleOverlay, ruleCron, ruleServices,
	ruleTemp, ruleWGStale, ruleLTE,
}

func cfg(in Input, name string) (uci.Config, bool) {
	for _, c := range in.Configs {
		if c.Name == name {
			return c, true
		}
	}
	return uci.Config{}, false
}

func opt(s uci.Section, k string) string {
	o, _ := s.Option(k)
	return o.Value()
}

func vals(s uci.Section, k string) []string {
	o, _ := s.Option(k)
	return o.Values
}

// ---- rules ----

// ruleDupList finds list options that repeat a value (seen as hundreds of
// identical entries after a buggy script ran for a while).
func ruleDupList(in Input) []Finding {
	var out []Finding
	for _, c := range in.Configs {
		for _, s := range c.Sections {
			for _, o := range s.Options {
				if !o.IsList {
					continue
				}
				seen := map[string]int{}
				var uniq []string
				for _, v := range o.Values {
					if seen[v]++; seen[v] == 1 {
						uniq = append(uniq, v)
					}
				}
				if len(uniq) == len(o.Values) {
					continue
				}
				worst, wv := 0, ""
				for _, v := range uniq {
					if seen[v] > worst {
						worst, wv = seen[v], v
					}
				}
				fix := []changes.Change{{Op: changes.OpDelete, Config: c.Name, Section: s.Ref(), Option: o.Key, Old: strings.Join(o.Values, " ")}}
				for _, v := range uniq {
					fix = append(fix, changes.Change{Op: changes.OpAddList, Config: c.Name, Section: s.Ref(), Option: o.Key, Value: v})
				}
				out = append(out, Finding{
					ID:  fmt.Sprintf("dup-list:%s.%s.%s", c.Name, s.Ref(), o.Key),
					Sev: Warn, Title: fmt.Sprintf("%s.%s.%s repeats entries", c.Name, s.Ref(), o.Key),
					Detail:  fmt.Sprintf("%d values, only %d distinct; '%s' appears %d times. Usually a script that adds the same entry on every run.", len(o.Values), len(uniq), wv, worst),
					Fix:     fix,
					FixNote: fmt.Sprintf("keep each value once (%d -> %d)", len(o.Values), len(uniq)),
				})
			}
		}
	}
	return out
}

func zoneNets(fw uci.Config) map[string][]string { // network -> zone names
	m := map[string][]string{}
	for _, s := range fw.Sections {
		if s.Type != "zone" {
			continue
		}
		for _, n := range vals(s, "network") {
			m[n] = append(m[n], opt(s, "name"))
		}
	}
	return m
}

func ruleMultiZone(in Input) []Finding {
	fw, ok := cfg(in, "firewall")
	if !ok {
		return nil
	}
	var out []Finding
	nets := zoneNets(fw)
	var names []string
	for n := range nets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if z := nets[n]; len(z) > 1 {
			out = append(out, Finding{
				ID: "multi-zone:" + n, Sev: Bad,
				Title:  fmt.Sprintf("interface '%s' is in %d firewall zones", n, len(z)),
				Detail: fmt.Sprintf("zones %s. Only one zone's rules can apply; traffic policy for it is unpredictable. Remove it from all but one zone (Config > firewall).", strings.Join(z, ", ")),
			})
		}
	}
	return out
}

// ruleOpenZone: a non-lan zone that accepts input lets its clients reach the
// router itself (admin pages, SSH, DNS).
func ruleOpenZone(in Input) []Finding {
	fw, ok := cfg(in, "firewall")
	if !ok {
		return nil
	}
	var out []Finding
	for _, s := range fw.Sections {
		if s.Type != "zone" {
			continue
		}
		name := opt(s, "name")
		if name == "lan" || name == "wan" || name == "" || !strings.EqualFold(opt(s, "input"), "ACCEPT") {
			continue
		}
		hasLan := false
		for _, n := range vals(s, "network") {
			if n == "lan" {
				hasLan = true
			}
		}
		if hasLan {
			continue
		}
		f := Finding{
			ID: "open-zone:" + name, Sev: Warn,
			Title:  fmt.Sprintf("zone '%s' can reach the router (input ACCEPT)", name),
			Detail: "Devices on this network can open the router's admin page and SSH. For a free/guest network the input policy should be REJECT, with rules allowing only DHCP and DNS.",
		}
		if allows(fw, name, "67") && allows(fw, name, "53") {
			f.Fix = []changes.Change{{Op: changes.OpSet, Config: "firewall", Section: s.Ref(), Option: "input", Value: "REJECT", Old: opt(s, "input")}}
			f.FixNote = "set input to REJECT (DHCP and DNS rules for this zone already exist)"
		} else {
			f.Detail += " No rule allowing DHCP (67) and DNS (53) from this zone exists, so it cannot be tightened automatically without breaking the network; add those rules first."
		}
		out = append(out, f)
	}
	return out
}

func allows(fw uci.Config, zone, port string) bool {
	for _, s := range fw.Sections {
		if s.Type != "rule" || opt(s, "src") != zone || opt(s, "target") != "ACCEPT" || opt(s, "enabled") == "0" {
			continue
		}
		for _, p := range strings.Fields(opt(s, "dest_port")) {
			if p == port {
				return true
			}
		}
	}
	return false
}

var adminPorts = map[string]string{"22": "SSH", "23": "telnet", "80": "web admin", "443": "web admin (HTTPS)", "8080": "alternate web"}

func ruleWanExposed(in Input) []Finding {
	fw, ok := cfg(in, "firewall")
	if !ok {
		return nil
	}
	var out []Finding
	for _, s := range fw.Sections {
		if opt(s, "enabled") == "0" || opt(s, "src") != "wan" {
			continue
		}
		switch s.Type {
		case "redirect": // port forward to an inside host
			if p := opt(s, "src_dport"); adminPorts[p] != "" {
				out = append(out, Finding{ID: "wan-forward:" + s.Ref(), Sev: Warn,
					Title:  fmt.Sprintf("port %s (%s) is forwarded from the internet", p, adminPorts[p]),
					Detail: fmt.Sprintf("Redirect '%s' sends it to %s. Exposed management ports are scanned constantly; use a VPN instead.", opt(s, "name"), opt(s, "dest_ip")),
					Fix:    []changes.Change{{Op: changes.OpSet, Config: "firewall", Section: s.Ref(), Option: "enabled", Value: "0"}}, FixNote: "disable this forward"})
			}
		case "rule":
			if opt(s, "target") == "ACCEPT" && opt(s, "dest") == "" {
				for _, p := range strings.Fields(opt(s, "dest_port")) {
					if adminPorts[p] != "" {
						out = append(out, Finding{ID: "wan-rule:" + s.Ref(), Sev: Warn,
							Title:  fmt.Sprintf("router %s (port %s) is open to the internet", adminPorts[p], p),
							Detail: fmt.Sprintf("Rule '%s' accepts it from wan.", opt(s, "name")),
							Fix:    []changes.Change{{Op: changes.OpSet, Config: "firewall", Section: s.Ref(), Option: "enabled", Value: "0"}}, FixNote: "disable this rule"})
					}
				}
			}
		}
	}
	return out
}

func ruleCountry(in Input) []Finding {
	w, ok := cfg(in, "wireless")
	if !ok {
		return nil
	}
	var out []Finding
	for _, s := range w.Sections {
		if s.Type == "wifi-device" && opt(s, "country") == "" {
			out = append(out, Finding{ID: "no-country:" + s.Ref(), Sev: Info,
				Title:  fmt.Sprintf("radio %s has no country code", s.Ref()),
				Detail: "Without one the radio uses the restrictive world regulatory domain: fewer 5 GHz channels and lower power. Set it on the Wi-Fi screen (radio form)."})
		}
	}
	return out
}

func ruleSSH(in Input) []Finding {
	d, ok := cfg(in, "dropbear")
	if !ok || in.Facts.SSHKeys < 0 {
		return nil
	}
	for _, s := range d.Sections {
		if s.Type != "dropbear" || opt(s, "PasswordAuth") == "off" || opt(s, "PasswordAuth") == "0" {
			continue
		}
		f := Finding{ID: "ssh-password", Sev: Warn, Title: "SSH accepts passwords",
			Detail: "Password logins can be guessed; keys cannot. The dropbear log is a good place to look for attempts."}
		if in.Facts.SSHKeys > 0 {
			f.Fix = []changes.Change{
				{Op: changes.OpSet, Config: "dropbear", Section: s.Ref(), Option: "PasswordAuth", Value: "off", Old: opt(s, "PasswordAuth")},
				{Op: changes.OpSet, Config: "dropbear", Section: s.Ref(), Option: "RootPasswordAuth", Value: "off", Old: opt(s, "RootPasswordAuth")},
			}
			f.FixNote = fmt.Sprintf("turn password login off (%d authorized key(s) exist; make sure one is yours)", in.Facts.SSHKeys)
		} else {
			f.Detail += " No authorized keys are installed, so turning passwords off would lock you out; install a key first (the connect wizard can do it)."
		}
		return []Finding{f}
	}
	return nil
}

func ruleOverlay(in Input) []Finding {
	f := in.Facts
	if f.OverlayFreeKB == 0 && f.OverlayUsedPct == 0 {
		return nil
	}
	switch {
	case f.OverlayFreeKB < 512:
		return []Finding{{ID: "overlay-full", Sev: Bad, Title: fmt.Sprintf("flash is almost full (%d KB free)", f.OverlayFreeKB),
			Detail: "Config saves and package installs can fail, and a full overlay can leave the router unable to boot cleanly. Remove packages or logs kept on flash."}}
	case f.OverlayFreeKB < 2048 || f.OverlayUsedPct >= 90:
		return []Finding{{ID: "overlay-low", Sev: Warn, Title: fmt.Sprintf("flash is %d%% full (%d KB free)", f.OverlayUsedPct, f.OverlayFreeKB),
			Detail: "Little room is left for upgrades and packages."}}
	}
	return nil
}

var (
	cronSecret = regexp.MustCompile(`(?i)((?:token|key|pass\w*|secret)=)\S+`)
	cronSched  = regexp.MustCompile(`^\s*(\*|\*/\d+|[\d,\-]+)\s`)
)

// MaskCron hides credentials inside cron command lines.
func MaskCron(l string) string { return cronSecret.ReplaceAllString(l, "${1}***") }

// ruleCron flags frequent jobs that write to flash: they wear it out.
func ruleCron(in Input) []Finding {
	var out []Finding
	for _, l := range in.Facts.Cron {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 6 || !frequent(f[0]) {
			continue
		}
		for i := 5; i < len(f); i++ {
			t := f[i]
			if t != ">" && t != ">>" || i+1 >= len(f) {
				continue
			}
			p := f[i+1]
			if strings.HasPrefix(p, "/tmp") || strings.HasPrefix(p, "/dev/") || strings.HasPrefix(p, "/var/") || strings.HasPrefix(p, "&") {
				continue
			}
			out = append(out, Finding{ID: "cron-flash:" + p, Sev: Warn,
				Title:  "a frequent cron job writes to " + p,
				Detail: fmt.Sprintf("'%s' runs at least every few minutes. Flash memory survives a limited number of writes; log to /tmp instead.", MaskCron(l))})
		}
	}
	return out
}

func frequent(min string) bool {
	if min == "*" {
		return true
	}
	if strings.HasPrefix(min, "*/") {
		n, err := strconv.Atoi(min[2:])
		return err == nil && n <= 15
	}
	return false
}

var watched = map[string]bool{"adblock": true, "pbr": true, "sqm": true, "banip": true, "dnsmasq": true, "firewall": true, "dropbear": true, "network": true}

func ruleServices(in Input) []Finding {
	var out []Finding
	for _, s := range in.Facts.Services {
		if watched[s.Name] && s.Enabled && s.State == services.Stopped {
			out = append(out, Finding{ID: "svc-stopped:" + s.Name, Sev: Warn,
				Title:  fmt.Sprintf("%s is enabled but not running", s.Name),
				Detail: "It starts at boot but is stopped now: it crashed, or failed to start. Check Logs, then start it on the Services screen."})
		}
	}
	return out
}
