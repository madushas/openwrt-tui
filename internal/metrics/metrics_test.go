package metrics

import (
	"math"
	"strings"
	"testing"
)

const fixture = `##owrt-metrics
##load
0.52 0.40 0.31 2/98 12345
##uptime
1000.50 3800.00
##time
1790000000
##cores
4
##stat
cpu  1000 0 500 8000 100 0 50 0 0 0
##mem
MemTotal:         262144 kB
MemFree:           50000 kB
MemAvailable:     131072 kB
Buffers:            1000 kB
Cached:            30000 kB
SwapTotal:             0 kB
SwapFree:              0 kB
##temp
52000
##net
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  111 1 0 0 0 0 0 0  111 1 0 0 0 0 0 0
  eth1: 5000000 4000 0 0 0 0 0 0  800000 3000 0 0 0 0 0 0
br-lan: 900000 700 0 0 0 0 0 0  2500000 900 0 0 0 0 0 0
##ct
321
16384
##ubus
{
	"interface": [
		{"interface":"loopback","up":true,"proto":"static","device":"lo"},
		{"interface":"lan","up":true,"proto":"static","uptime":900,"l3_device":"br-lan",
		 "ipv4-address":[{"address":"192.168.1.1","mask":24}]},
		{"interface":"wan","up":true,"proto":"dhcp","uptime":4000,"l3_device":"eth1",
		 "ipv4-address":[{"address":"203.0.113.7","mask":24}],
		 "route":[{"target":"0.0.0.0","mask":0,"nexthop":"203.0.113.1"}],
		 "dns-server":["1.1.1.1","9.9.9.9"]}
	]
}
##leases
1790003600 aa:bb:cc:dd:ee:01 192.168.1.50 laptop 01:aa:bb
1789990000 aa:bb:cc:dd:ee:02 192.168.1.51 expired *
0 AA:BB:CC:DD:EE:03 192.168.1.60 printer *
1790003600 aa:bb:cc:dd:ee:04 192.168.1.61 * 01:aa
##wifi
dev wlan0
wlan0     ESSID: "HomeNet"
AA:BB:CC:DD:EE:01  -52 dBm / -95 dBm (SNR 43)  120 ms ago
	RX: 144.4 MBit/s, MCS 15
	TX: 130.0 MBit/s, MCS 14
AA:BB:CC:DD:EE:09  -71 dBm / -95 dBm (SNR 24)  800 ms ago
##end
`

func TestParseFixture(t *testing.T) {
	s, err := Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if s.At != 1000.5 || s.Cores != 4 || s.Load[0] != 0.52 {
		t.Fatalf("basics: %+v", s)
	}
	if s.CPUTotal != 9650 || s.CPUIdle != 8100 {
		t.Fatalf("cpu: total %d idle %d", s.CPUTotal, s.CPUIdle)
	}
	if s.MemTotal != 262144 || s.MemAvail != 131072 || math.Abs(s.MemUsedPct()-50) > 0.01 {
		t.Fatalf("mem: %+v %.2f", s, s.MemUsedPct())
	}
	if !s.HasTemp || s.TempC != 52 {
		t.Fatalf("temp: %v %v", s.HasTemp, s.TempC)
	}
	if len(s.Net) != 2 || s.Net[0].Name != "eth1" || s.Net[0].RxBytes != 5000000 || s.Net[0].TxBytes != 800000 {
		t.Fatalf("net (lo must be skipped): %+v", s.Net)
	}
	if s.CtCount != 321 || s.CtMax != 16384 {
		t.Fatalf("conntrack: %d/%d", s.CtCount, s.CtMax)
	}
	if len(s.Ifaces) != 2 || s.Ifaces[1].Name != "wan" || !s.Ifaces[1].Default || s.Ifaces[1].Gateway != "203.0.113.1" ||
		s.Ifaces[1].Device != "eth1" || s.Ifaces[1].Addrs[0] != "203.0.113.7/24" || len(s.Ifaces[1].DNS) != 2 {
		t.Fatalf("ifaces: %+v", s.Ifaces)
	}
	if s.Ifaces[0].Default {
		t.Fatal("lan must not be the default route")
	}
	active := s.ActiveLeases()
	if len(s.Leases) != 4 || len(active) != 3 || active[1].Host != "printer" || active[1].MAC != "aa:bb:cc:dd:ee:03" {
		t.Fatalf("leases: all %+v active %+v", s.Leases, active)
	}
	if active[2].Host != "" {
		t.Fatalf("a '*' hostname must become empty, got %q", active[2].Host)
	}
	if len(s.Stations) != 2 || s.Stations[0].ESSID != "HomeNet" || s.Stations[0].Signal != -52 || s.Stations[1].MAC != "aa:bb:cc:dd:ee:09" {
		t.Fatalf("stations: %+v", s.Stations)
	}
}

func TestParseSurvivesMissingPieces(t *testing.T) {
	s, err := Parse("##uptime\n50.0 10.0\n##ubus\nnot json\n##net\ngarbage\n")
	if err != nil {
		t.Fatal(err)
	}
	if s.At != 50 || s.Cores != 1 || len(s.Net) != 0 || len(s.Ifaces) != 0 || s.HasTemp || s.CtMax != 0 {
		t.Fatalf("got %+v", s)
	}
	if _, err := Parse(""); err == nil {
		t.Fatal("empty output must be an error")
	}
	if _, err := Parse("##load\n0.1 0.1 0.1 1/1 1\n"); err == nil {
		t.Fatal("no uptime must be an error: rates need the router clock")
	}
}

func TestMemAvailableFallback(t *testing.T) {
	s, _ := Parse("##uptime\n9 9\n##mem\nMemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 150 kB\n")
	if s.MemAvail != 300 {
		t.Fatalf("got %d, want free+buffers+cached = 300", s.MemAvail)
	}
}

func TestDeltaRates(t *testing.T) {
	a, _ := Parse(fixture)
	b := a
	b.At = a.At + 2
	b.CPUTotal = a.CPUTotal + 400
	b.CPUIdle = a.CPUIdle + 100 // 300 of 400 jiffies busy
	b.Net = []Counters{{Name: "eth1", RxBytes: 5000000 + 2000000, TxBytes: 800000 + 400000}, {Name: "br-lan", RxBytes: 100, TxBytes: 100}}
	r, ok := Delta(a, b)
	if !ok {
		t.Fatal("not ok")
	}
	if math.Abs(r.CPUPct-75) > 0.001 {
		t.Fatalf("cpu %.2f want 75", r.CPUPct)
	}
	if r.Net["eth1"].RxBps != 1000000 || r.Net["eth1"].TxBps != 200000 {
		t.Fatalf("eth1 %+v", r.Net["eth1"])
	}
	if r.Net["br-lan"].RxBps != 0 { // counter went backwards: reset, not a huge number
		t.Fatalf("reset counter produced %+v", r.Net["br-lan"])
	}
	if got := SortedNet(r)[0]; got != "eth1" {
		t.Fatalf("busiest first: %v", got)
	}
}

func TestDeltaRefusesRebootAndSameInstant(t *testing.T) {
	a, _ := Parse(fixture)
	b := a
	if _, ok := Delta(a, b); ok {
		t.Fatal("same instant accepted")
	}
	b.At = 5 // uptime went down: the router rebooted
	if _, ok := Delta(a, b); ok {
		t.Fatal("reboot accepted")
	}
}

func TestSeriesRing(t *testing.T) {
	s := Series{Cap: 3}
	for _, v := range []float64{1, 5, 2, 4} {
		s.Push(v)
	}
	if got := s.Values(); len(got) != 3 || got[0] != 5 || got[2] != 4 {
		t.Fatalf("got %v", got)
	}
	if s.Max() != 5 || s.Last() != 4 {
		t.Fatal("max/last wrong")
	}
	v := s.Values()
	v[0] = 99
	if s.Values()[0] == 99 {
		t.Fatal("Values must return a copy")
	}
}

func TestBars(t *testing.T) {
	for dbm, want := range map[int]int{-40: 4, -55: 4, -60: 3, -70: 2, -80: 1, -95: 1} {
		if got := Bars(dbm); got != want {
			t.Errorf("%d dBm -> %d bars, want %d", dbm, got, want)
		}
	}
}

func TestScriptIsReadOnly(t *testing.T) {
	for _, bad := range []string{" rm ", "reboot", ">>", "uci set", "uci commit", "/etc/init.d", "wifi ", "ubus call network reload", "iptables", "nft add"} {
		if strings.Contains(Script, bad) {
			t.Errorf("metrics script contains %q", bad)
		}
	}
}
