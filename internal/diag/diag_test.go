package diag

import (
	"context"
	"testing"

	"github.com/madushakv/owrt-tui/internal/transport"
)

const busyboxPing = `PING 8.8.8.8 (8.8.8.8): 56 data bytes
64 bytes from 8.8.8.8: seq=0 ttl=117 time=10.5 ms

--- 8.8.8.8 ping statistics ---
4 packets transmitted, 4 packets received, 0% packet loss
round-trip min/avg/max = 9.8/10.5/12.1 ms
`

const iputilsPing = `--- 1.1.1.1 ping statistics ---
5 packets transmitted, 4 received, 20% packet loss, time 4005ms
rtt min/avg/max/mdev = 8.1/9.0/10.2/0.7 ms
`

func TestParsePing(t *testing.T) {
	r, err := ParsePing("8.8.8.8", busyboxPing)
	if err != nil || r.Sent != 4 || r.Received != 4 || r.LossPct != 0 || r.Min != 9.8 || r.Avg != 10.5 || r.Max != 12.1 {
		t.Errorf("busybox: %+v %v", r, err)
	}
	r, err = ParsePing("1.1.1.1", iputilsPing)
	if err != nil || r.Sent != 5 || r.Received != 4 || r.LossPct != 20 || r.Avg != 9.0 {
		t.Errorf("iputils: %+v %v", r, err)
	}
}

func TestParsePingTotalLossIsAResultNotAnError(t *testing.T) {
	r, err := ParsePing("10.9.9.9", "--- 10.9.9.9 ping statistics ---\n3 packets transmitted, 0 packets received, 100% packet loss\n")
	if err != nil || r.Received != 0 || r.LossPct != 100 || r.Avg != 0 {
		t.Errorf("%+v %v", r, err)
	}
}

func TestParsePingUnresolvable(t *testing.T) {
	_, err := ParsePing("nope.invalid", "ping: bad address 'nope.invalid'\n")
	if err == nil || err.Error() != "ping: bad address 'nope.invalid'" {
		t.Errorf("err = %v", err)
	}
}

func TestHostValidation(t *testing.T) {
	bad := []string{"", "8.8.8.8; reboot", "$(id)", "a b", "x`id`", "-c1", "a\nb", "host|cat"}
	for _, h := range bad {
		if _, err := PingCommand(h, 3); err == nil {
			t.Errorf("PingCommand accepted %q", h)
		}
		if _, err := TracerouteCommand(h); err == nil {
			t.Errorf("TracerouteCommand accepted %q", h)
		}
		if _, err := NslookupCommand(h, ""); err == nil {
			t.Errorf("NslookupCommand accepted %q", h)
		}
	}
	if _, err := NslookupCommand("example.com", "8.8.8.8; id"); err == nil {
		t.Error("server must be validated")
	}
}

func TestCommands(t *testing.T) {
	if c, _ := PingCommand("example.com", 99); c != "ping -c 20 -W 2 'example.com'" {
		t.Errorf("count should clamp to 20: %q", c)
	}
	if c, _ := PingCommand("example.com", 0); c != "ping -c 1 -W 2 'example.com'" {
		t.Errorf("count should clamp to 1: %q", c)
	}
	if c, _ := NslookupCommand("example.com", "1.1.1.1"); c != "nslookup 'example.com' '1.1.1.1'" {
		t.Errorf("got %q", c)
	}
}

func TestPingAndTracerouteOverTransport(t *testing.T) {
	f := (&transport.Fake{}).
		On("ping -c 4", busyboxPing).
		On("traceroute", "traceroute to 8.8.8.8\n 1  192.168.1.254  1.1 ms\n 2  10.0.0.1  4.0 ms\n")
	r, err := Ping(context.Background(), f, "8.8.8.8", 4)
	if err != nil || r.Avg != 10.5 {
		t.Fatalf("%+v %v", r, err)
	}
	var lines []string
	if err := Traceroute(context.Background(), f, "8.8.8.8", func(l string) { lines = append(lines, l) }); err != nil || len(lines) != 3 {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
}
