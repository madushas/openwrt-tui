// Package traffic estimates per-client traffic from the connection tracking
// table. It counts bytes of connections that are still open, so it is a live
// view of who is using the link, not a billing-grade total.
package traffic

import (
	"strconv"
	"strings"
	"time"
)

// Script reads the accounting switch and the table. Read-only.
const Script = `echo "##acct"; cat /proc/sys/net/netfilter/nf_conntrack_acct 2>/dev/null
echo "##ct"; head -n 30000 /proc/net/nf_conntrack 2>/dev/null
`

// EnableHint is the command that turns byte counting on (until reboot).
const EnableHint = "sysctl -w net.netfilter.nf_conntrack_acct=1"

// Usage is what one source address has moved over its open connections.
type Usage struct {
	Up, Down int64 // bytes sent by the client / received by it
	Conns    int
}

// Sample is one reading of the table.
type Sample struct {
	At        time.Time
	Acct      bool // byte accounting enabled, so counters are meaningful
	Available bool // the table could be read at all
	By        map[string]Usage
}

// Parse reads the output of Script.
func Parse(out string, at time.Time) Sample {
	s := Sample{At: at, By: map[string]Usage{}}
	sec := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "##") {
			sec = l[2:]
			continue
		}
		switch sec {
		case "acct":
			if strings.TrimSpace(l) == "1" {
				s.Acct = true
			}
		case "ct":
			if strings.TrimSpace(l) == "" {
				continue
			}
			s.Available = true
			src, up, down, ok := parseLine(l)
			if !ok {
				continue
			}
			u := s.By[src]
			u.Up += up
			u.Down += down
			u.Conns++
			s.By[src] = u
		}
	}
	return s
}

// parseLine takes the original-direction source and both byte counters.
func parseLine(l string) (src string, up, down int64, ok bool) {
	nb := 0
	for _, f := range strings.Fields(l) {
		k, v, found := strings.Cut(f, "=")
		if !found {
			continue
		}
		switch k {
		case "src":
			if src == "" {
				src = v
			}
		case "bytes":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				continue
			}
			if nb == 0 {
				up = n
			} else if nb == 1 {
				down = n
			}
			nb++
		}
	}
	return src, up, down, src != ""
}

// Rate is a bytes-per-second pair.
type Rate struct{ Up, Down float64 }

// Rates compares two samples. Connections that ended between samples make the
// counters drop; those deltas are clamped to zero rather than shown negative.
func Rates(prev, cur Sample) map[string]Rate {
	out := map[string]Rate{}
	dt := cur.At.Sub(prev.At).Seconds()
	if dt <= 0 || !prev.Acct || !cur.Acct {
		return out
	}
	for ip, c := range cur.By {
		p := prev.By[ip]
		du, dd := float64(c.Up-p.Up), float64(c.Down-p.Down)
		if du < 0 {
			du = 0
		}
		if dd < 0 {
			dd = 0
		}
		out[ip] = Rate{du / dt, dd / dt}
	}
	return out
}
