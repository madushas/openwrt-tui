package tui

import (
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/events"
)

func TestEventsScreenCollapsesAndWarnsAboutReconnects(t *testing.T) {
	a := writeApp(t)
	openTab(t, a, "Events")
	s := scr(a, "Events").(*eventsScreen)
	var evs []events.Event
	for i := 0; i < 7; i++ {
		evs = append(evs,
			events.Event{Time: "Oct 31 10:00:0" + string(rune('0'+i)), Kind: events.WifiJoin, Group: events.GClients, Subject: "aa:bb:cc:dd:ee:01", Name: "phone", Detail: "on phy0-ap0"},
			events.Event{Time: "Oct 31 10:00:0" + string(rune('0'+i)), Kind: events.SSHOK, Group: events.GSecurity, Subject: "root", Detail: "from 192.168.50.199"})
	}
	s.loading, s.all = false, evs
	v := view(a)
	if !strings.Contains(v, "Reconnecting often: phone (7)") {
		t.Errorf("no reconnect note:\n%s", v)
	}
	if !strings.Contains(v, "(x7)") {
		t.Errorf("repeated logins not collapsed:\n%s", v)
	}
	if n := strings.Count(v, "SSH login for root"); n != 1 {
		t.Errorf("SSH login shown %d times", n)
	}
}
