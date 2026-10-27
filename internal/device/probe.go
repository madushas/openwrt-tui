// Package device probes a router once at connect time and records what it can
// do, so screens and actions can adapt (apk vs opkg, fw4 vs fw3, and so on).
package device

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/transport"
)

// Info is the result of the capability probe.
type Info struct {
	Hostname   string
	Model      string
	BoardName  string
	Kernel     string
	Distro     string
	Version    string // e.g. "25.12.5", "SNAPSHOT", "24.10.4"
	Revision   string
	Target     string
	RootFS     string
	Arch       string // from uname -m
	PkgMgr     string // "apk", "opkg" or ""
	Firewall   string // "fw4", "fw3" or ""
	Swconfig   bool   // legacy swconfig switch present
	Tools      map[string]bool
	OverlayKB  Space
	TmpKB      Space
	UptimeSecs int
}

// Space is a df summary in KiB.
type Space struct{ Total, Used, Avail int64 }

// ProbeScript is run in one round trip.
const ProbeScript = `
echo '##board'; ubus call system board 2>/dev/null
echo '##uname'; uname -m 2>/dev/null
echo '##tools'
for t in apk opkg nft fw4 fw3 iptables wg tcpdump conntrack iw iwinfo jsonfilter owut logread ubus uci ethtool curl service sysupgrade ip; do
  command -v $t >/dev/null 2>&1 && echo $t
done
echo '##swconfig'; [ -x /sbin/swconfig ] && echo yes
echo '##df'; df -k /overlay /tmp 2>/dev/null
echo '##uptime'; cat /proc/uptime 2>/dev/null
`

// Probe runs the probe script and parses it.
func Probe(ctx context.Context, t transport.Transport) (*Info, error) {
	res, err := transport.RunScript(ctx, t, ProbeScript)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 && strings.TrimSpace(res.Stdout) == "" {
		return nil, fmt.Errorf("probe failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return ParseProbe(res.Stdout)
}

// ParseProbe parses ProbeScript output.
func ParseProbe(out string) (*Info, error) {
	sections := splitSections(out)
	info := &Info{Tools: map[string]bool{}}

	if b := strings.TrimSpace(sections["board"]); b != "" {
		var board struct {
			Kernel    string `json:"kernel"`
			Hostname  string `json:"hostname"`
			Model     string `json:"model"`
			BoardName string `json:"board_name"`
			RootFS    string `json:"rootfs_type"`
			Release   struct {
				Distribution string `json:"distribution"`
				Version      string `json:"version"`
				Revision     string `json:"revision"`
				Target       string `json:"target"`
			} `json:"release"`
		}
		if err := json.Unmarshal([]byte(b), &board); err != nil {
			return nil, fmt.Errorf("parse board info: %w", err)
		}
		info.Kernel, info.Hostname, info.Model = board.Kernel, board.Hostname, board.Model
		info.BoardName, info.RootFS = board.BoardName, board.RootFS
		info.Distro, info.Version = board.Release.Distribution, board.Release.Version
		info.Revision, info.Target = board.Release.Revision, board.Release.Target
	} else {
		return nil, fmt.Errorf("no board info returned; is this an OpenWrt device?")
	}

	info.Arch = strings.TrimSpace(sections["uname"])
	for _, l := range lines(sections["tools"]) {
		info.Tools[l] = true
	}
	switch {
	case info.Tools["apk"]:
		info.PkgMgr = "apk"
	case info.Tools["opkg"]:
		info.PkgMgr = "opkg"
	}
	switch {
	case info.Tools["fw4"]:
		info.Firewall = "fw4"
	case info.Tools["fw3"]:
		info.Firewall = "fw3"
	}
	info.Swconfig = strings.TrimSpace(sections["swconfig"]) == "yes"
	info.OverlayKB, info.TmpKB = parseDF(sections["df"])
	if f := strings.Fields(sections["uptime"]); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil {
			info.UptimeSecs = int(v)
		}
	}
	return info, nil
}

func splitSections(out string) map[string]string {
	m := map[string]string{}
	cur := ""
	var b strings.Builder
	flush := func() {
		if cur != "" {
			m[cur] = b.String()
		}
		b.Reset()
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "##") {
			flush()
			cur = strings.TrimSpace(strings.TrimPrefix(l, "##"))
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	flush()
	return m
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// parseDF reads BusyBox "df -k" output and returns overlay and /tmp space.
func parseDF(s string) (overlay, tmp Space) {
	for _, l := range lines(s) {
		f := strings.Fields(l)
		if len(f) < 6 || f[0] == "Filesystem" {
			continue
		}
		sp := Space{Total: atoi64(f[1]), Used: atoi64(f[2]), Avail: atoi64(f[3])}
		switch f[len(f)-1] {
		case "/overlay":
			overlay = sp
		case "/tmp":
			tmp = sp
		}
	}
	return
}

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// Series returns "25.12" for "25.12.5"; "" for snapshots or unparsable values.
func (i *Info) Series() string {
	p := strings.Split(strings.TrimSuffix(i.Version, "-SNAPSHOT"), ".")
	if len(p) < 2 {
		return ""
	}
	if _, err := strconv.Atoi(p[0]); err != nil {
		return ""
	}
	return p[0] + "." + p[1]
}

// IsSnapshot reports a development build.
func (i *Info) IsSnapshot() bool { return strings.Contains(strings.ToUpper(i.Version), "SNAPSHOT") }

// Advisory is a banner-worthy fact about the installed release.
type Advisory struct {
	Level string // "warn" or "info"
	Text  string
}

// Advisories returns release-related warnings. The facts come from the OpenWrt
// 25.12 release notes: 24.10 stopped getting security updates in September
// 2026, and 25.12.5 fixed remotely triggerable vulnerabilities.
func (i *Info) Advisories() []Advisory {
	var out []Advisory
	if i.IsSnapshot() {
		out = append(out, Advisory{"info", "snapshot build: behavior can change between builds"})
	}
	maj, min := seriesNums(i.Series())
	switch {
	case maj == 0:
	case maj < 24 || (maj == 24 && min <= 10):
		out = append(out, Advisory{"warn", fmt.Sprintf("OpenWrt %s no longer receives security updates; plan an upgrade to 25.12", i.Series())})
	case maj == 25 && min == 12:
		if patch := patchNum(i.Version); patch >= 0 && patch < 5 {
			out = append(out, Advisory{"warn", fmt.Sprintf("%s is older than 25.12.5, which fixed remotely triggerable vulnerabilities in default services", i.Version)})
		}
	}
	return out
}

func seriesNums(s string) (int, int) {
	p := strings.Split(s, ".")
	if len(p) != 2 {
		return 0, 0
	}
	a, _ := strconv.Atoi(p[0])
	b, _ := strconv.Atoi(p[1])
	return a, b
}

func patchNum(v string) int {
	p := strings.Split(strings.TrimSuffix(v, "-SNAPSHOT"), ".")
	if len(p) < 3 {
		return -1
	}
	n, err := strconv.Atoi(strings.SplitN(p[2], "-", 2)[0])
	if err != nil {
		return -1
	}
	return n
}
