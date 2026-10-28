package changes

import "testing"

func TestAnonymousSectionDeletesRunLastHighestFirst(t *testing.T) {
	var s Set
	for _, sec := range []string{"@host[0]", "@host[2]", "@host[1]"} {
		if err := s.Add(Change{Op: OpDelete, Config: "dhcp", Section: sec}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Add(Change{Op: OpSet, Config: "dhcp", Section: "@host[3]", Option: "ip", Value: "10.0.0.9"}); err != nil {
		t.Fatal(err)
	}
	got := s.ordered()
	want := []string{"@host[3]", "@host[2]", "@host[1]", "@host[0]"}
	for i, c := range got {
		if c.Section != want[i] {
			t.Fatalf("order %d = %s, want %s", i, c.Section, want[i])
		}
	}
}
