package accepted

import (
	"os"
	"testing"

	"github.com/madushakv/owrt-tui/internal/health"
)

func TestAcceptSplitAndResurface(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	f := health.Finding{ID: "multi-zone:direct", Title: "t", Detail: "d"}
	other := health.Finding{ID: "x", Title: "t", Detail: "d"}
	if err := s.Accept("192.168.50.1", f); err != nil {
		t.Fatal(err)
	}
	act, done := Split([]health.Finding{f, other}, s.Load("192.168.50.1"))
	if len(act) != 1 || act[0].ID != "x" || len(done) != 1 {
		t.Fatalf("split wrong: %v %v", act, done)
	}
	f.Detail = "now different" // the situation changed
	if act, _ := Split([]health.Finding{f}, s.Load("192.168.50.1")); len(act) != 1 {
		t.Fatal("a changed finding must come back")
	}
	if a, _ := Split([]health.Finding{f}, Store{Dir: s.Dir}.Load("other-router")); len(a) != 1 {
		t.Fatal("acceptance leaked to another router")
	}
	if err := s.Forget("192.168.50.1", "multi-zone:direct"); err != nil {
		t.Fatal(err)
	}
	if len(s.Load("192.168.50.1")) != 0 {
		t.Fatal("forget failed")
	}
}

func TestDamagedFileIsEmpty(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	_ = s.Accept("r", health.Finding{ID: "a"})
	if err := writeBad(s, "r"); err != nil {
		t.Fatal(err)
	}
	if len(s.Load("r")) != 0 {
		t.Fatal("damaged file must read as empty")
	}
}

func writeBad(s Store, r string) error { return osWrite(s.path(r), "{not json") }

func osWrite(p, c string) error { return os.WriteFile(p, []byte(c), 0o600) }
