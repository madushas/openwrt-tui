// Package postcheck measures whether the router still works after a change, so
// an apply can be kept or rolled back on evidence instead of on a timer the
// user has to beat. The checks are read-only.
package postcheck

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Script prints one "name=value" line per check.
const Script = `echo '##owrt-postcheck'
r=$(ip route show default 2>/dev/null | head -n 1)
if [ -n "$r" ]; then echo default_route=ok; else echo default_route=fail; fi
gw=$(echo "$r" | awk '{for(i=1;i<NF;i++) if($i=="via"){print $(i+1); exit}}')
if [ -n "$gw" ]; then
  if ping -c 1 -W 2 "$gw" >/dev/null 2>&1; then echo gateway=ok; else echo gateway=fail; fi
fi
if command -v nslookup >/dev/null 2>&1; then
  if nslookup openwrt.org 127.0.0.1 >/dev/null 2>&1; then echo dns=ok; else echo dns=fail; fi
fi
if command -v wg >/dev/null 2>&1; then
  now=$(date +%s)
  wg show all latest-handshakes 2>/dev/null | while read -r i k t; do
    [ -n "$i" ] || continue
    if [ "${t:-0}" -gt 0 ] 2>/dev/null; then echo "wg_$i=$((now - t))"; else echo "wg_$i=never"; fi
  done
fi
for p in dnsmasq dropbear uhttpd; do
  [ -x /etc/init.d/$p ] || continue
  if pidof $p >/dev/null 2>&1; then echo proc_$p=ok; else echo proc_$p=fail; fi
done
`

// Result maps a check name to its value.
type Result map[string]string

var lineRe = regexp.MustCompile(`^([a-z]+(?:_[A-Za-z0-9-]+)?)=([A-Za-z0-9]+)$`)

// Parse reads the script's output; unknown or malformed lines are ignored.
func Parse(out string) Result {
	r := Result{}
	for _, l := range strings.Split(out, "\n") {
		if m := lineRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			r[m[1]] = m[2]
		}
	}
	return r
}

// WireGuardMaxAge is how old a handshake may be and still count as connected.
// WireGuard re-handshakes about every two minutes while traffic flows.
const WireGuardMaxAge = 180

// Healthy reports whether one check passes.
func Healthy(name, val string) bool {
	if strings.HasPrefix(name, "wg_") {
		n, err := strconv.Atoi(val)
		return err == nil && n >= 0 && n <= WireGuardMaxAge
	}
	return val == "ok"
}

// Passing counts the checks that currently pass.
func (r Result) Passing() int {
	n := 0
	for k, v := range r {
		if Healthy(k, v) {
			n++
		}
	}
	return n
}

// Regressions lists checks that passed before the change and fail now, or
// have disappeared, in a stable order.
func Regressions(before, after Result) []string {
	var out []string
	for k, v := range before {
		if !Healthy(k, v) {
			continue
		}
		av, ok := after[k]
		if !ok {
			out = append(out, Describe(k)+": no answer")
		} else if !Healthy(k, av) {
			out = append(out, Describe(k)+": "+Detail(k, av))
		}
	}
	sort.Strings(out)
	return out
}

// Describe is the plain-language name of a check.
func Describe(name string) string {
	switch {
	case name == "default_route":
		return "default route"
	case name == "gateway":
		return "gateway reachable"
	case name == "dns":
		return "DNS answers"
	case strings.HasPrefix(name, "wg_"):
		return "WireGuard " + strings.TrimPrefix(name, "wg_")
	case strings.HasPrefix(name, "proc_"):
		return strings.TrimPrefix(name, "proc_") + " running"
	}
	return name
}

// Detail explains a failing value.
func Detail(name, val string) string {
	if strings.HasPrefix(name, "wg_") {
		if val == "never" {
			return "no handshake"
		}
		return fmt.Sprintf("last handshake %ss ago", val)
	}
	return "failed"
}
