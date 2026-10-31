package internal_test

import (
	"testing"
	"time"

	"github.com/madushakv/owrt-tui/internal/clients"
	"github.com/madushakv/owrt-tui/internal/cron"
	"github.com/madushakv/owrt-tui/internal/events"
	"github.com/madushakv/owrt-tui/internal/health"
	"github.com/madushakv/owrt-tui/internal/logs"
	"github.com/madushakv/owrt-tui/internal/metrics"
	"github.com/madushakv/owrt-tui/internal/postcheck"
	"github.com/madushakv/owrt-tui/internal/traffic"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/wificaps"
)

// Everything the program parses comes from a router, which may be broken,
// truncated or hostile. None of these may panic whatever they are given.
func FuzzParsers(f *testing.F) {
	for _, s := range []string{
		"", "##overlay\n##temp\nabc\n##wg\nwgok\nx\t\t\n", "config interface 'lan'\n\toption proto 'static'\n",
		"package network\nconfig\n", "Sun Oct 25 09:13:02 2026 daemon.info hostapd: phy0-ap0: AP-STA-CONNECTED aa:bb:cc:dd:ee:ff",
		"##acct\n1\n##ct\nipv4 2 tcp 6 5 src=1 bytes=x bytes=", "##leases\n1 aa 1.1.1.1\n##neigh\n##sta wlan0\nStation\n",
		"* * * * * cmd\n#x\n@reboot y", "\x00\xff\xfe", "'\"\\`$(", "##owrt-metrics\ncpu 1 2\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		uci.ParseExport(s)
		clients.ParseRaw(s)
		logs.ParseLine(s)
		health.ParseFacts(s)
		health.ParseLTE(s)
		traffic.Parse(s, time.Unix(0, 0))
		events.Build([]logs.Entry{logs.ParseLine(s), {Proc: "hostapd", Msg: s}, {Proc: "dropbear", Msg: s}, {Proc: "netifd", Msg: s}}, nil)
		metrics.Parse(s)
		cron.Parse(s)
		wificaps.Parse(s)
		postcheck.Parse(s)
	})
}
