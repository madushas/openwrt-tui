package health

import (
	"strconv"
	"strings"
)

// Script collects the runtime facts. Read-only.
const Script = `echo "##overlay"; df -k /overlay 2>/dev/null | tail -n 1
echo "##release"; ( . /etc/openwrt_release 2>/dev/null; echo "$DISTRIB_RELEASE" )
echo "##sshkeys"; if [ -r /etc/dropbear/authorized_keys ]; then grep -c -E '^(ssh-|ecdsa-|sk-)' /etc/dropbear/authorized_keys; else echo 0; fi
echo "##cron"; crontab -l 2>/dev/null
`

// ParseFacts reads the output of Script. Missing parts stay zero (SSHKeys -1).
func ParseFacts(out string) Facts {
	f := Facts{SSHKeys: -1}
	sec := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "##") {
			sec = l[2:]
			continue
		}
		switch sec {
		case "overlay":
			// Filesystem 1K-blocks Used Available Use% Mounted
			p := strings.Fields(l)
			if len(p) >= 5 {
				f.OverlayFreeKB, _ = strconv.Atoi(p[3])
				f.OverlayUsedPct, _ = strconv.Atoi(strings.TrimSuffix(p[4], "%"))
			}
		case "release":
			if t := strings.TrimSpace(l); t != "" {
				f.Release = t
			}
		case "sshkeys":
			if n, err := strconv.Atoi(strings.TrimSpace(l)); err == nil {
				f.SSHKeys = n
			}
		case "cron":
			if strings.TrimSpace(l) != "" {
				f.Cron = append(f.Cron, l)
			}
		}
	}
	return f
}
