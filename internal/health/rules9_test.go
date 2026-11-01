package health

import "testing"

const wgNet = `package network

config interface 'vpn'
	option proto 'wireguard'

config wireguard_vpn
	option description 'edu server'
	option public_key 'PUBKEY1'
	option endpoint_host 'vpn.example.org'

config wireguard_vpn
	option description 'dial-in phone'
	option public_key 'PUBKEY2'
`

func TestParseFactsPhase9(t *testing.T) {
	out := "##overlay\n##temp\n61000\n73500\n##now\n1790001000\n##wg\nwgok\nvpn\tPUBKEY1\t1790000900\n##lte\n{\"type\":\"lte\",\"rssi\":-70,\"rsrq\":-9,\"rsrp\":-118,\"snr\":1.5}\n"
	f := ParseFacts(out)
	if !f.TempOK || f.TempC != 73.5 {
		t.Fatalf("temp %v", f.TempC)
	}
	if !f.WGKnown || len(f.Handshakes) != 1 || f.Handshakes[0].At != 1790000900 || f.Now != 1790001000 {
		t.Fatalf("%+v", f)
	}
	if f.LTE == nil || f.LTE.RSRP != -118 || f.LTE.SNR != 1.5 {
		t.Fatalf("%+v", f.LTE)
	}
}

func TestParseLTEMMCLI(t *testing.T) {
	l := ParseLTE("  Signal | refresh: 0s\n  LTE | rssi: -75.00 dBm\n      rsrq: -9.00 dB\n      rsrp: -100.00 dBm\n      snr: 12.40 dB\n  5G | rsrp: -80 dBm\n access technologies: lte\n operator name: ExampleNet\n")
	if l == nil || l.RSRP != -100 || l.Tech != "lte" || l.Operator != "ExampleNet" || l.Quality() != "good" {
		t.Fatalf("%+v", l)
	}
	if ParseLTE("nothing here") != nil || ParseLTE("") != nil {
		t.Fatal("no modem should give nil")
	}
}

func TestTempRule(t *testing.T) {
	for _, c := range []struct {
		t    float64
		want string
	}{{60, ""}, {76, "temp-warm"}, {90, "temp-high"}} {
		got := ids(Check(Input{Facts: Facts{TempC: c.t, TempOK: true, SSHKeys: -1}}))
		if got != c.want {
			t.Errorf("%v: %q", c.t, got)
		}
	}
	if got := ids(Check(Input{Facts: Facts{TempC: 99, SSHKeys: -1}})); got != "" {
		t.Fatalf("unknown temp flagged: %q", got)
	}
}

func TestWGStale(t *testing.T) {
	cfgs := parse(t, wgNet)
	base := Facts{SSHKeys: -1, WGKnown: true, Now: 10000}
	run := func(at int64, present bool) string {
		f := base
		if present {
			f.Handshakes = []Handshake{{Iface: "vpn", Peer: "PUBKEY1", At: at}, {Iface: "vpn", Peer: "PUBKEY2", At: 0}}
		}
		return ids(Check(Input{Configs: cfgs, Facts: f}))
	}
	if got := run(9900, true); got != "" {
		t.Errorf("fresh: %q", got)
	}
	if got := run(1000, true); got != "wg-stale-vpn" {
		t.Errorf("stale: %q", got)
	}
	if got := run(0, true); got != "wg-never-vpn" {
		t.Errorf("never (dial-in peer must be ignored): %q", got)
	}
	if got := run(0, false); got != "" {
		t.Errorf("interface down: %q", got)
	}
	f := base
	f.WGKnown = false
	if got := ids(Check(Input{Configs: cfgs, Facts: f})); got != "" {
		t.Errorf("no wg: %q", got)
	}
}

func TestLTERule(t *testing.T) {
	for _, c := range []struct {
		rsrp int
		snr  float64
		want string
	}{{-95, 12, ""}, {-108, 12, "lte-fair"}, {-95, 1, "lte-fair"}, {-120, 5, "lte-weak"}} {
		f := Facts{SSHKeys: -1, LTE: &LTE{RSRP: c.rsrp, HasRSRP: true, SNR: c.snr, HasSNR: true}}
		if got := ids(Check(Input{Facts: f})); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}
