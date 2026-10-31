package traffic

import (
	"testing"
	"time"
)

const out = `##acct
1
##ct
ipv4     2 tcp      6 431999 ESTABLISHED src=192.168.1.50 dst=1.2.3.4 sport=5000 dport=443 packets=10 bytes=1200 src=1.2.3.4 dst=203.0.113.2 sport=443 dport=5000 packets=12 bytes=15000 [ASSURED] mark=0 use=2
ipv4     2 udp      17 20 src=192.168.1.50 dst=8.8.8.8 sport=5001 dport=53 packets=1 bytes=80 src=8.8.8.8 dst=203.0.113.2 sport=53 dport=5001 packets=1 bytes=120 mark=0 use=2
ipv6     10 tcp     6 100 ESTABLISHED src=fd00::5 dst=2001:db8::1 sport=1 dport=2 packets=2 bytes=100 src=2001:db8::1 dst=fd00::5 sport=2 dport=1 packets=2 bytes=900 mark=0 use=2
ipv4     2 tcp      6 100 ESTABLISHED src=192.168.1.51 dst=1.1.1.1 sport=1 dport=2 packets=2 bytes=10 src=1.1.1.1 dst=203.0.113.2 sport=2 dport=1 packets=2 bytes=20 mark=0 use=2
`

func TestParse(t *testing.T) {
	s := Parse(out, time.Unix(100, 0))
	if !s.Acct || !s.Available {
		t.Fatal("flags", s.Acct, s.Available)
	}
	if u := s.By["192.168.1.50"]; u.Up != 1280 || u.Down != 15120 || u.Conns != 2 {
		t.Fatalf("%+v", u)
	}
	if u := s.By["fd00::5"]; u.Down != 900 {
		t.Fatalf("%+v", u)
	}
	if len(s.By) != 3 {
		t.Fatal(len(s.By))
	}
}

func TestNoAccounting(t *testing.T) {
	s := Parse("##acct\n0\n##ct\nipv4 2 tcp 6 5 src=10.0.0.2 dst=1.1.1.1 sport=1 dport=2 src=1.1.1.1 dst=1.2.3.4 sport=2 dport=1\n", time.Now())
	if s.Acct || !s.Available || s.By["10.0.0.2"].Conns != 1 || s.By["10.0.0.2"].Down != 0 {
		t.Fatalf("%+v", s)
	}
	if e := Parse("", time.Now()); e.Available {
		t.Fatal("empty is not available")
	}
}

func TestRates(t *testing.T) {
	a := Parse(out, time.Unix(100, 0))
	b := a
	b.At = time.Unix(110, 0)
	b.By = map[string]Usage{"192.168.1.50": {Up: 1280 + 1000, Down: 15120 + 50000, Conns: 2}, "192.168.1.51": {Up: 0, Down: 0}}
	r := Rates(a, b)
	if r["192.168.1.50"].Up != 100 || r["192.168.1.50"].Down != 5000 {
		t.Fatalf("%+v", r)
	}
	if r["192.168.1.51"].Down != 0 || r["192.168.1.51"].Up != 0 { // counters dropped: clamped
		t.Fatalf("%+v", r["192.168.1.51"])
	}
	a.Acct = false
	if len(Rates(a, b)) != 0 {
		t.Fatal("rates without accounting")
	}
}
