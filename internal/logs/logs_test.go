package logs

import (
	"context"
	"regexp"
	"testing"

	"github.com/madushakv/owrt-tui/internal/transport"
)

const sample = `Sun Oct 25 09:13:01 2026 kern.info kernel: [    1.234567] eth0: link up
Sun Oct 25 09:13:02 2026 daemon.info dnsmasq-dhcp[1234]: DHCPACK(br-lan) 192.168.1.50 aa:bb:cc:dd:ee:ff phone
Sun Oct 25 09:13:03 2026 daemon.err dropbear[999]: Bad password attempt for 'root' from 10.0.0.9:5555
Mon Oct  5 09:13:04 2026 daemon.warn netifd: Interface 'wan' has link connectivity loss
this line has no syslog header
`

func TestParseLine(t *testing.T) {
	cases := []struct {
		line, facility, sev, proc string
		pid                       int
		msg                       string
	}{
		{"Sun Oct 25 09:13:02 2026 daemon.info dnsmasq-dhcp[1234]: DHCPACK x", "daemon", "info", "dnsmasq-dhcp", 1234, "DHCPACK x"},
		{"Sun Oct 25 09:13:01 2026 kern.info kernel: [    1.2] eth0: link up", "kern", "info", "kernel", 0, "[    1.2] eth0: link up"},
		{"Mon Oct  5 09:13:04 2026 daemon.warn netifd: Interface 'wan' down", "daemon", "warn", "netifd", 0, "Interface 'wan' down"},
		{"no header at all", "", "", "", 0, "no header at all"},
	}
	for _, c := range cases {
		e := ParseLine(c.line)
		if e.Facility != c.facility || e.Severity != c.sev || e.Proc != c.proc || e.PID != c.pid || e.Msg != c.msg || e.Raw != c.line {
			t.Errorf("ParseLine(%q) = %+v", c.line, e)
		}
	}
}

func TestFilter(t *testing.T) {
	var all []Entry
	for _, l := range []string{
		"Sun Oct 25 09:13:01 2026 kern.info kernel: eth0 up",
		"Sun Oct 25 09:13:03 2026 daemon.err dropbear[999]: Bad password attempt",
		"Sun Oct 25 09:13:04 2026 daemon.warn netifd: wan down",
		"unstructured",
	} {
		all = append(all, ParseLine(l))
	}
	count := func(f Filter) int { return len(f.Apply(all)) }

	if got := count(Filter{}); got != 4 {
		t.Errorf("empty filter: %d", got)
	}
	if got := count(Filter{MinSeverity: "warn"}); got != 2 { // err and warn, not info or unparsed
		t.Errorf("warn and worse: %d", got)
	}
	if got := count(Filter{MinSeverity: "err"}); got != 1 {
		t.Errorf("err and worse: %d", got)
	}
	if got := count(Filter{Proc: "DROP"}); got != 1 {
		t.Errorf("proc is case-insensitive: %d", got)
	}
	if got := count(Filter{Contains: "WAN DOWN"}); got != 1 {
		t.Errorf("contains is case-insensitive: %d", got)
	}
	if got := count(Filter{Pattern: regexp.MustCompile(`password|eth0`)}); got != 2 {
		t.Errorf("pattern: %d", got)
	}
}

func TestFetch(t *testing.T) {
	f := (&transport.Fake{}).On("logread -l 5000", sample)
	got, err := Fetch(context.Background(), f, 999999) // clamped to 5000
	if err != nil || len(got) != 5 {
		t.Fatalf("got %d entries, err %v", len(got), err)
	}
	if got[4].Proc != "" || got[4].Msg != "this line has no syslog header" {
		t.Errorf("unparsed line should be kept verbatim: %+v", got[4])
	}
	f2 := (&transport.Fake{}).OnResult("logread", transport.Result{ExitCode: 1, Stderr: "boom"})
	if _, err := Fetch(context.Background(), f2, 10); err == nil {
		t.Error("expected an error on non-zero exit")
	}
}

func TestFollowFallsBackOnPlainTransport(t *testing.T) {
	f := (&transport.Fake{}).On("logread -l 20 -f", sample)
	var n int
	err := Follow(context.Background(), f, 20, func(Entry) { n++ })
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
