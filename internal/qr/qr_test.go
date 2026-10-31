package qr

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type golden struct {
	Text    string   `json:"text"`
	Level   int      `json:"level"` // 1 = L, 0 = M
	Mask    int      `json:"mask"`
	Version int      `json:"version"`
	Rows    []string `json:"rows"`
}

// The golden matrices come from an independent implementation (python
// "qrcode"), so these tests prove the symbols are bit-for-bit what a
// reference encoder produces for the same data and mask.
func TestMatchesReferenceEncoder(t *testing.T) {
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var gs []golden
	if err := json.Unmarshal(b, &gs); err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		lvl := M
		if g.Level == 1 {
			lvl = L
		}
		c, err := encode([]byte(g.Text), lvl, g.Mask)
		if err != nil {
			t.Fatal(err)
		}
		if c.Version != g.Version {
			t.Fatalf("%.12q level %d: version %d, want %d", g.Text, g.Level, c.Version, g.Version)
		}
		for r, row := range g.Rows {
			var sb strings.Builder
			for _, d := range c.Modules[r] {
				if d {
					sb.WriteByte('1')
				} else {
					sb.WriteByte('0')
				}
			}
			if sb.String() != row {
				t.Fatalf("%.12q level %d mask %d: row %d differs\n got %s\nwant %s", g.Text, g.Level, g.Mask, r, sb.String(), row)
			}
		}
	}
}

func TestTooLongAndRender(t *testing.T) {
	if _, err := Encode([]byte(strings.Repeat("x", 3000)), L); err == nil {
		t.Fatal("accepted 3000 bytes")
	}
	c, err := Encode([]byte("hello"), M)
	if err != nil {
		t.Fatal(err)
	}
	out := c.Text(2)
	if lines := strings.Count(out, "\n"); lines < (len(c.Modules)+4)/2 {
		t.Fatalf("%d lines", lines)
	}
	if !strings.Contains(out, "48;5;255") || !strings.Contains(out, "38;5;0") {
		t.Fatal("explicit colours missing")
	}
}
