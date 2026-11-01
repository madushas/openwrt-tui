package tui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/madushakv/owrt-tui/internal/metrics"
	"github.com/madushakv/owrt-tui/internal/session"
	"github.com/madushakv/owrt-tui/internal/transport"
)

const realProbe = `##board
{"kernel":"6.12.74","hostname":"router","model":"Real Router","board_name":"r","rootfs_type":"squashfs","release":{"distribution":"OpenWrt","version":"25.12.2","revision":"r1","target":"mediatek/filogic"}}
##uname
aarch64
##tools
apk
nft
fw4
ubus
uci
iwinfo
##swconfig
##df
Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/ubi0_1              51200     48600      2600  95% /overlay
tmpfs                   124000      9000    115000   7% /tmp
##uptime
55182.62 213816.70
`

var counterRe = regexp.MustCompile(`(?m)^(\s*(?:wan|br-lan|phy0-ap0|vpn|eth0):)\s*(\d+)\s+(\d+)(\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+\s+\d+)\s+(\d+)\s+(\d+)`)

// realRouter replays the synthetic router fixture, advancing the
// uptime, CPU jiffies and interface counters on every call so rates appear.
func realRouter(t *testing.T) *transport.Fake {
	b, err := os.ReadFile("../metrics/testdata/real-router.txt")
	if err != nil {
		t.Fatal(err)
	}
	base := string(b)
	n := 0
	return (&transport.Fake{Name: "real"}).
		On("ubus call system board", realProbe).
		OnFunc("##owrt-metrics", func(cmd, stdin string) transport.Result {
			n++
			out := strings.Replace(base, "55182.62 213816.70", fmt.Sprintf("%.2f 213816.70", 55182.62+float64(n)*2), 1)
			out = strings.Replace(out, "cpu  28999 0 243638 21381675 191 0 149161", fmt.Sprintf("cpu  %d 0 243638 %d 191 0 149161", 28999+n*60, 21381675+n*740), 1)
			out = counterRe.ReplaceAllStringFunc(out, func(m string) string {
				f := counterRe.FindStringSubmatch(m)
				rx, _ := strconv.Atoi(f[2])
				tx, _ := strconv.Atoi(f[5])
				return fmt.Sprintf("%s %d %s%s %d %s", f[1], rx+n*1_500_000, f[3], f[4], tx+n*300_000, f[6])
			})
			return transport.Result{Stdout: out}
		})
}

func realApp(t *testing.T) *App {
	t.Helper()
	ctx := context.Background()
	s, err := session.Connect(ctx, realRouter(t), true)
	if err != nil {
		t.Fatal(err)
	}
	a := New(ctx, s, 5*time.Second)
	a.Update(tea.WindowSizeMsg{Width: 130, Height: 38})
	return a
}

func TestDashboardWithRealRouterData(t *testing.T) {
	a := realApp(t)
	d := feed(t, a, 6)
	for _, sz := range [][2]int{{130, 38}, {100, 26}, {80, 24}, {60, 18}} {
		a.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		v := plain(a.render())
		lines := strings.Split(v, "\n")
		if len(lines) != sz[1] {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for _, l := range lines {
			if w := lipgloss.Width(l); w > sz[0] {
				t.Errorf("%dx%d: line wider than the terminal (%d): %q", sz[0], sz[1], w, l)
			}
		}
		if os.Getenv("OWRT_LOOK") != "" {
			fmt.Printf("\n===== real data %dx%d =====\n%s\n", sz[0], sz[1], v)
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 130, Height: 38})
	v := plain(a.render())
	for _, want := range []string{"192.168.1.2/24", "192.168.50.1/24", "wireguard", "HomeNet", "phone-a", "tv-b", "-43 dBm", "Traffic", "wan"} {
		if !strings.Contains(v, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if dev := d.graphDev(); dev != "wan" {
		t.Errorf("graph device %q, want the default-route device wan", dev)
	}
}

func TestClientStatusComesFromARPAndWifi(t *testing.T) {
	mk := func(neigh ...metrics.Neigh) *dashboard {
		d := &dashboard{}
		d.cur = metrics.Sample{
			Epoch: 100,
			Leases: []metrics.Lease{
				{Expiry: 200, MAC: "aa:00:00:00:00:01", IP: "10.0.0.2", Host: "wifi"},
				{Expiry: 200, MAC: "aa:00:00:00:00:02", IP: "10.0.0.3", Host: "wired"},
				{Expiry: 200, MAC: "aa:00:00:00:00:03", IP: "10.0.0.4", Host: "gone"},
			},
			Stations: []metrics.Station{{Dev: "wlan0", ESSID: "Net", MAC: "aa:00:00:00:00:01", Signal: -50}},
			Neigh:    neigh,
		}
		return d
	}
	by := func(d *dashboard) map[string]clientRow {
		m := map[string]clientRow{}
		for _, c := range d.clients() {
			m[c.name] = c
		}
		return m
	}
	// With an ARP table: present-and-answering is active, absent is offline.
	m := by(mk(metrics.Neigh{IP: "10.0.0.3", MAC: "aa:00:00:00:00:02", State: "REACHABLE"}))
	if !m["wifi"].wifi || m["wifi"].essid != "Net" || m["wired"].state != "REACHABLE" || m["gone"].state != "" || !m["gone"].haveARP {
		t.Fatalf("with ARP: %+v", m)
	}
	// Without an ARP table nothing may be called offline.
	m = by(mk())
	if m["gone"].haveARP {
		t.Fatalf("no ARP table, yet the row claims one: %+v", m["gone"])
	}
}

// Names that strangers control must not be able to drive the terminal.
func TestHostileRouterTextCannotReachTheTerminal(t *testing.T) {
	ctx := context.Background()
	r := realRouter(t)
	// A DHCP client that names itself with an escape sequence, in the capture.
	base := r
	hostile := (&transport.Fake{Name: "hostile"}).
		On("ubus call system board", realProbe).
		OnFunc("##owrt-metrics", func(cmd, stdin string) transport.Result {
			res, _ := base.Run(ctx, "##owrt-metrics", nil)
			res.Stdout = strings.Replace(res.Stdout, "phone-a", "evil\x1b]0;pwned\x07\x1b[2J", 1)
			return res
		}).
		On("logread", "Oct 27 09:13:01 2026 auth.info dropbear[1]: Bad password attempt for 'a\x1b[2Jb' from 1.2.3.4\n")
	s, err := session.Connect(ctx, hostile, true)
	if err != nil {
		t.Fatal(err)
	}
	a := New(ctx, s, 5*time.Second)
	a.Update(tea.WindowSizeMsg{Width: 130, Height: 38})
	feed(t, a, 3)
	if v := a.render(); strings.Contains(plain2(v), "\x1b]") || strings.Contains(v, "pwned\x07") || strings.Contains(v, "\x07") {
		t.Fatalf("hostile DHCP name reached the screen")
	}
	openTab(t, a, "Logs") // logs
	if v := a.render(); strings.Contains(v, "\x07") || strings.Contains(v, "[2Jb") && strings.Contains(v, "\x1b[2Jb") {
		t.Fatalf("hostile log line reached the screen")
	}
}

// plain2 strips only styling sequences (colours and cursor styling) so a leaked
// OSC or bell remains visible to the assertion.
func plain2(s string) string { return regexp.MustCompile(`\x1b\[[0-9;:]*m`).ReplaceAllString(s, "") }
