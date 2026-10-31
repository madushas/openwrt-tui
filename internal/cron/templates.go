package cron

import (
	"fmt"
	"regexp"

	"github.com/madushakv/owrt-tui/internal/shq"
)

// Job templates. Each returns one or more [schedule, command, tag] triples
// ready for Add. A '%' is written as '\%' (cron treats a bare one as a line
// break), and commands only write to /tmp, never to flash.

var ifaceRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,14}$`)
var targetRe = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)

func checkTime(h, m int) error {
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return fmt.Errorf("time must be 00:00 to 23:59")
	}
	return nil
}

// RestartService restarts or reloads an init script every day at hh:mm.
func RestartService(name, action string, h, m int) (s, c, tag string, err error) {
	if !shq.Unit(name) {
		return "", "", "", fmt.Errorf("invalid service name %q", name)
	}
	if action != "restart" && action != "reload" {
		return "", "", "", fmt.Errorf("action must be restart or reload")
	}
	if err := checkTime(h, m); err != nil {
		return "", "", "", err
	}
	return fmt.Sprintf("%d %d * * *", m, h), "/etc/init.d/" + name + " " + action, action + "-" + name, nil
}

// Reconnect brings a logical interface back up when a ping through it fails
// (for a SIM or a flaky uplink). every is the interval in minutes.
func Reconnect(iface, target string, every int) (s, c, tag string, err error) {
	if !ifaceRe.MatchString(iface) {
		return "", "", "", fmt.Errorf("invalid interface name %q", iface)
	}
	if !targetRe.MatchString(target) {
		return "", "", "", fmt.Errorf("ping target must be an address or host name")
	}
	if every < 2 || every > 59 {
		return "", "", "", fmt.Errorf("interval must be 2 to 59 minutes")
	}
	cmd := fmt.Sprintf("ping -c2 -W5 -I $(ifstatus %s | jsonfilter -e '@.l3_device') %s >/dev/null 2>&1 || ifup %s", iface, target, iface)
	return fmt.Sprintf("*/%d * * * *", every), cmd, "reconnect-" + iface, nil
}

// WGWatchdog restarts a WireGuard interface when it has had no handshake for
// five minutes (and has had one before, so a never-used peer is left alone).
func WGWatchdog(iface string, every int) (s, c, tag string, err error) {
	if !ifaceRe.MatchString(iface) {
		return "", "", "", fmt.Errorf("invalid interface name %q", iface)
	}
	if every < 2 || every > 59 {
		return "", "", "", fmt.Errorf("interval must be 2 to 59 minutes")
	}
	cmd := fmt.Sprintf("h=$(wg show %s latest-handshakes | awk '{print $2; exit}'); [ -n \"$h\" ] && [ \"$h\" -gt 0 ] && [ $(( $(date +\\%%s) - h )) -gt 300 ] && ifup %s", iface, iface)
	return fmt.Sprintf("*/%d * * * *", every), cmd, "wg-watchdog-" + iface, nil
}

// WifiRestart reloads Wi-Fi every day at hh:mm (clears stuck radios).
func WifiRestart(h, m int) (s, c, tag string, err error) {
	if err := checkTime(h, m); err != nil {
		return "", "", "", err
	}
	return fmt.Sprintf("%d %d * * *", m, h), "wifi reload", "wifi-reload", nil
}

// LogSnapshot keeps a copy of the system log in RAM for after a crash
// (/tmp only; nothing is written to flash).
func LogSnapshot(every int) (s, c, tag string, err error) {
	if every < 5 || every > 59 {
		return "", "", "", fmt.Errorf("interval must be 5 to 59 minutes")
	}
	return fmt.Sprintf("*/%d * * * *", every), "logread | tail -n 300 > /tmp/log-snapshot.txt", "log-snapshot", nil
}
