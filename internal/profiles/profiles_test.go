package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "sub", "p.json")}
	if ps, err := s.List(); err != nil || len(ps) != 0 {
		t.Fatal(ps, err)
	}
	for _, p := range []Profile{{Name: "office", Target: "root@192.168.1.1"}, {Name: "home", Target: "gw", Identity: "~/.ssh/id", Port: "2222", Note: "fibre firewall"}} {
		if err := s.Put(p); err != nil {
			t.Fatal(err)
		}
	}
	ps, _ := s.List()
	if len(ps) != 2 || ps[0].Name != "home" || ps[0].Port != "2222" {
		t.Fatalf("%+v", ps)
	}
	if err := s.Put(Profile{Name: "home", Target: "gw2"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get("home"); p.Target != "gw2" || p.Port != "" {
		t.Fatalf("replace: %+v", p)
	}
	if err := s.Remove("office"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("office"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Remove("office"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(s.Path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %v", st.Mode().Perm())
		}
		sd, _ := os.Stat(filepath.Dir(s.Path))
		if sd.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", sd.Mode().Perm())
		}
	}
}

func TestValidate(t *testing.T) {
	for _, p := range []Profile{
		{Name: "", Target: "x"}, {Name: "a b", Target: "x"}, {Name: "../x", Target: "x"},
		{Name: "ok", Target: ""}, {Name: "ok", Target: "-oProxyCommand=evil"}, {Name: "ok", Target: "a\nb"},
	} {
		if Validate(p) == nil {
			t.Errorf("accepted %+v", p)
		}
	}
	if Validate(Profile{Name: "ok-1.x", Target: "root@host"}) != nil {
		t.Fatal("rejected good profile")
	}
}

func TestCorruptFileIsAnErrorNotAnEmptyList(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(p, []byte("{not json"), 0o600)
	s := &Store{Path: p}
	if _, err := s.List(); err == nil {
		t.Fatal("corrupt file read as empty; a Put would then overwrite it")
	}
	if err := s.Put(Profile{Name: "a", Target: "b"}); err == nil {
		t.Fatal("Put overwrote a corrupt file")
	}
}
