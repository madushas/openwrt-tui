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
echo "##temp"; cat /sys/class/thermal/thermal_zone*/temp 2>/dev/null
echo "##now"; date +%s
echo "##wanip"; d=$(ip -4 route show default 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="dev"){print $(i+1);exit}}'); [ -n "$d" ] && ip -4 addr show dev "$d" 2>/dev/null | awk '/inet /{sub(/\/.*/,"",$2);print $2;exit}'
echo "##updev"; [ -n "$d" ] && { echo "$d"; cat /sys/class/net/$d/statistics/rx_bytes /sys/class/net/$d/statistics/tx_bytes 2>/dev/null; }
echo "##wg"; if command -v wg >/dev/null 2>&1; then echo wgok; wg show all latest-handshakes 2>/dev/null; fi
echo "##lte"
if command -v mmcli >/dev/null 2>&1; then mmcli -m any --signal-get 2>/dev/null; mmcli -m any 2>/dev/null | grep -i -E 'access tech|operator name'; fi
if command -v uqmi >/dev/null 2>&1; then for d in /dev/cdc-wdm*; do [ -e "$d" ] && uqmi -d "$d" --get-signal-info 2>/dev/null && break; done; fi
`

// ParseFacts reads the output of Script. Missing parts stay zero (SSHKeys -1).
func ParseFacts(out string) Facts {
	f := Facts{SSHKeys: -1}
	sec := ""
	var lteLines []string
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
		case "temp":
			if n, err := strconv.Atoi(strings.TrimSpace(l)); err == nil {
				c := float64(n)
				if c > 1000 { // millidegrees
					c /= 1000
				}
				if !f.TempOK || c > f.TempC {
					f.TempC, f.TempOK = c, true
				}
			}
		case "updev":
			t := strings.TrimSpace(l)
			if t == "" {
				break
			}
			if n, err := strconv.ParseInt(t, 10, 64); err == nil && f.UpDev != "" {
				f.UpBytes += n
			} else if f.UpDev == "" {
				f.UpDev = t
			}
		case "wanip":
			if t := strings.TrimSpace(l); t != "" {
				f.WanIP = t
			}
		case "now":
			if n, err := strconv.ParseInt(strings.TrimSpace(l), 10, 64); err == nil {
				f.Now = n
			}
		case "wg":
			p := strings.Fields(l)
			switch {
			case len(p) == 1 && p[0] == "wgok":
				f.WGKnown = true
			case len(p) == 3:
				ts, _ := strconv.ParseInt(p[2], 10, 64)
				f.Handshakes = append(f.Handshakes, Handshake{Iface: p[0], Peer: p[1], At: ts})
			}
		case "lte":
			lteLines = append(lteLines, l)
		case "cron":
			if strings.TrimSpace(l) != "" {
				f.Cron = append(f.Cron, l)
			}
		}
	}
	f.LTE = ParseLTE(strings.Join(lteLines, "\n"))
	return f
}
