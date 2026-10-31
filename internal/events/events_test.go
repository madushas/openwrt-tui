package events

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/logs"
)

const sample = `Sun Oct 25 09:13:01 2026 daemon.info hostapd: phy0-ap0: AP-STA-CONNECTED AA:BB:CC:00:00:01 auth_alg=open
Sun Oct 25 09:13:02 2026 daemon.info dnsmasq-dhcp[1234]: DHCPACK(br-lan) 192.168.1.50 aa:bb:cc:00:00:01 pixel-8
Sun Oct 25 09:13:03 2026 daemon.err dropbear[999]: Bad password attempt for 'root' from 203.0.113.9:5555
Sun Oct 25 09:13:04 2026 daemon.err dropbear[999]: Bad password attempt for 'root' from 203.0.113.9:5556
Sun Oct 25 09:13:05 2026 daemon.warn netifd: Interface 'wan' has link connectivity loss
Sun Oct 25 09:13:06 2026 daemon.notice netifd: Interface 'wan' is now up
Sun Oct 25 09:13:07 2026 daemon.notice netifd: Interface 'wan' is setting up now
Sun Oct 25 09:13:08 2026 daemon.notice netifd: Network device 'eth1' link is down
Sun Oct 25 09:13:09 2026 daemon.info hostapd: phy0-ap0: AP-STA-DISCONNECTED aa:bb:cc:00:00:01
Sun Oct 25 09:13:10 2026 daemon.info dropbear[1000]: Pubkey auth succeeded for 'root' with key sha1!! from 192.168.1.7:44332
Sun Oct 25 09:13:11 2026 cron.notice crond[321]: USER root pid 3039 cmd /usr/bin/backup.sh
Sun Oct 25 09:13:12 2026 daemon.info hostapd: phy0-ap0: STA aa:bb:cc:00:00:01 IEEE 802.11: associated
Sun Oct 25 09:13:13 2026 daemon.notice syslogd: started: BusyBox v1.37
Sun Oct 25 09:13:14 2026 daemon.info dnsmasq-dhcp[1234]: DHCPACK(br-lan) 192.168.1.51 aa:bb:cc:00:00:02 
`

func entries() []logs.Entry {
	var out []logs.Entry
	for _, l := range strings.Split(strings.TrimSpace(sample), "\n") {
		out = append(out, logs.ParseLine(l))
	}
	return out
}

func TestBuild(t *testing.T) {
	ev := Build(entries(), nil)
	if len(ev) != 12 {
		t.Fatalf("got %d events", len(ev))
	}
	if ev[0].Kind != DHCP || ev[len(ev)-1].Kind != WifiJoin {
		t.Fatalf("order: %v ... %v", ev[0].Kind, ev[len(ev)-1].Kind)
	}
	var join, leave Event
	for _, e := range ev {
		switch e.Kind {
		case WifiJoin:
			join = e
		case WifiLeave:
			leave = e
		}
	}
	if join.Subject != "aa:bb:cc:00:00:01" || join.Name != "pixel-8" || leave.Name != "pixel-8" {
		t.Fatalf("names: %+v %+v", join, leave)
	}
	if !strings.Contains(join.Label(), "pixel-8") || !strings.Contains(join.Label(), "phy0-ap0") || strings.Contains(join.Label(), "aa:bb") {
		t.Fatalf("label %q", join.Label())
	}
}

func TestExtraNamesWin(t *testing.T) {
	ev := Build(entries(), map[string]string{"aa:bb:cc:00:00:01": "Dad's phone"})
	for _, e := range ev {
		if e.Kind == WifiJoin && e.Name != "Dad's phone" {
			t.Fatalf("name %q", e.Name)
		}
	}
}

func TestSecurityAndNetwork(t *testing.T) {
	ev := Build(entries(), nil)
	sec := Filter(ev, GSecurity)
	if len(sec) != 3 {
		t.Fatalf("security %d", len(sec))
	}
	bad := 0
	for _, e := range sec {
		if e.Kind == SSHFail {
			bad++
			if e.Sev != Bad || e.Subject != "root" || e.Detail != "from 203.0.113.9" {
				t.Fatalf("%+v", e)
			}
		}
	}
	if bad != 2 {
		t.Fatal("failed logins", bad)
	}
	if bf := BruteForce(ev, 2); bf["203.0.113.9"] != 2 || len(bf) != 1 {
		t.Fatalf("%v", bf)
	}
	if bf := BruteForce(ev, 3); len(bf) != 0 {
		t.Fatalf("%v", bf)
	}
	net := Filter(ev, GNetwork)
	if len(net) != 3 { // loss, up, link down; "setting up" ignored
		t.Fatalf("network %d", len(net))
	}
	if len(Filter(ev, "")) != len(ev) {
		t.Fatal("empty filter")
	}
}

func TestNoiseIgnored(t *testing.T) {
	for _, l := range []string{
		"Sun Oct 25 09:13:01 2026 kern.info kernel: [    1.2] mt7915e 0000:00:00.0: HW/SW Version",
		"garbage",
		"Sun Oct 25 09:13:01 2026 daemon.info hostapd: phy0-ap0: STA aa:bb:cc:00:00:01 WPA: pairwise key handshake",
	} {
		if _, ok := Parse(logs.ParseLine(l)); ok {
			t.Errorf("parsed %q", l)
		}
	}
}

func TestRealDropbearPubkeyLine(t *testing.T) {
	m := "Pubkey auth succeeded for 'root' with ssh-ed25519 key SHA256:abc123 from 192.168.10.199:39836"
	if !reOK.MatchString(m) {
		t.Fatalf("real 25.12 dropbear line not matched: %q", m)
	}
	if !reOK.MatchString("Password auth succeeded for 'root' from 10.0.0.2:5555") {
		t.Fatal("password form regressed")
	}
}
