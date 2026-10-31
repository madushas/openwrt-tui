package tasks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// Every wizard must produce stageable changes on every fixture router: a real
// capture (498 repeated zone entries, a backslash SSID) and typical setups.
func TestWizardsStageCleanlyOnEveryFixtureRouter(t *testing.T) {
	dirs, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "routers", "*"))
	if len(dirs) < 3 {
		t.Fatalf("fixtures missing: %v", dirs)
	}
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "uci.txt"))
		if err != nil {
			t.Fatal(err)
		}
		cfgs, err := uci.ParseExport(string(b))
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		name := filepath.Base(d)
		run := func(what string, ch []changes.Change, err error) {
			if err != nil {
				return // refusing with a clear error is fine (e.g. no wan zone on an AP)
			}
			var s changes.Set
			for _, c := range ch {
				if e := s.Add(c); e != nil {
					t.Errorf("%s/%s: %v", name, what, e)
				}
			}
		}
		g, err := GuestNetwork(Guest{Name: "guest", SSID: "G", Key: "password123", Octet: 77, Radio: "radio0", Isolate: true}, cfgs)
		run("guest", g, err)
		p, _, err := PortForward(Forward{Name: "web", Proto: "tcp", ExtPort: "8081", IP: "192.168.1.50", IntPort: "80"}, cfgs)
		run("forward", p, err)
		for _, m := range SIMModes {
			s, _, err := SIMRoute(SIM{Name: "sim", Proto: "qmi", Device: "/dev/cdc-wdm0", APN: "internet", Mode: m, Domains: "a.example"}, cfgs)
			run("sim-"+m, s, err)
		}
	}
}
