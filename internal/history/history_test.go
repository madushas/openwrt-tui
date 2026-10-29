package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	s, err := Open(t.TempDir(), "root@router.lan")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveLoadListNewestFirst(t *testing.T) {
	s := open(t)
	base := time.Now().UTC()
	for i, id := range []string{"chg-20260101-000001", "chg-20260101-000002", "chg-20260101-000003"} {
		if err := s.Save(Entry{ID: id, Time: base.Add(time.Duration(i) * time.Minute), Summary: []string{"x"}, Configs: map[string]string{"dhcp": "package dhcp\n"}}); err != nil {
			t.Fatal(err)
		}
	}
	l, err := s.List()
	if err != nil || len(l) != 3 || l[0].ID != "chg-20260101-000003" {
		t.Fatalf("%v %+v", err, l)
	}
	if l[0].Configs != nil {
		t.Fatal("List must not return the unmasked snapshots")
	}
	e, err := s.Load("chg-20260101-000002")
	if err != nil || e.Configs["dhcp"] != "package dhcp\n" {
		t.Fatalf("%v %+v", err, e)
	}
}

func TestFilesAreSeverelyPrivate(t *testing.T) {
	s := open(t)
	s.Save(Entry{ID: "chg-20260101-000001", Configs: map[string]string{"wireless": "secret"}})
	s.SaveBaseline(map[string]string{"a": "b"})
	if st, _ := os.Stat(s.Dir()); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode().Perm())
	}
	files, _ := os.ReadDir(s.Dir())
	if len(files) != 2 {
		t.Fatalf("%d files (temp files left behind?)", len(files))
	}
	for _, f := range files {
		st, _ := os.Stat(filepath.Join(s.Dir(), f.Name()))
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", f.Name(), st.Mode().Perm())
		}
	}
}

func TestIDsCannotEscapeTheDirectory(t *testing.T) {
	s := open(t)
	for _, id := range []string{"../evil", "a/b", "", "CHG-1", "a"} {
		if err := s.Save(Entry{ID: id}); err == nil {
			t.Errorf("Save accepted %q", id)
		}
		if _, err := s.Load(id); err == nil {
			t.Errorf("Load accepted %q", id)
		}
	}
	// a hostile router name stays inside the root
	root := t.TempDir()
	st, err := Open(root, "../../etc/x y")
	if err != nil || !strings.HasPrefix(st.Dir(), root) {
		t.Fatalf("%v %v", err, st)
	}
}

func TestPruneAndDamagedFiles(t *testing.T) {
	s := open(t)
	for i := 0; i < 5; i++ {
		s.Save(Entry{ID: "chg-20260101-00000" + string(rune('1'+i)), Time: time.Now().Add(time.Duration(i) * time.Second)})
	}
	os.WriteFile(filepath.Join(s.Dir(), "chg-20260101-000099.json"), []byte("{not json"), 0o600)
	if err := s.Prune(2); err != nil {
		t.Fatal(err)
	}
	l, _ := s.List()
	if len(l) != 2 {
		t.Fatalf("%d entries after prune", len(l))
	}
}

func TestBaseline(t *testing.T) {
	s := open(t)
	if _, ok := s.LoadBaseline(); ok {
		t.Fatal("baseline from nowhere")
	}
	s.SaveBaseline(map[string]string{"dhcp": "h1"})
	b, ok := s.LoadBaseline()
	if !ok || b.Hashes["dhcp"] != "h1" || b.Time.IsZero() {
		t.Fatalf("%+v %v", b, ok)
	}
}
