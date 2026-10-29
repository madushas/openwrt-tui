package wificaps

import (
	"os"
	"reflect"
	"testing"
)

func real(t *testing.T) Caps {
	t.Helper()
	b, err := os.ReadFile("testdata/mt7603-mt7662.txt")
	if err != nil {
		t.Fatal(err)
	}
	return Parse(string(b))
}

// The capture is from a TOZED ZLT S12 PRO (MT7603E 2.4 GHz, MT7662E 5 GHz).
func TestRealRadios(t *testing.T) {
	c := real(t)
	if len(c.Phys) != 2 || c.Country != "00" {
		t.Fatalf("phys=%d country=%q", len(c.Phys), c.Country)
	}
	p0, ok := c.For("radio0", "1e140000.pcie/pci0000:00/0000:00:01.0/0000:02:00.0")
	if !ok || p0.Name != "phy0" {
		t.Fatalf("radio0 -> %+v %v", p0, ok)
	}
	b2, ok := p0.Band("2g")
	if !ok || !reflect.DeepEqual(b2.HTModes(), []string{"HT20", "HT40"}) {
		t.Fatalf("2g modes %v", b2.HTModes())
	}
	// channels 12-14 are passive-only under the unset world domain
	if got := b2.Allowed(); len(got) != 11 || got[10] != 11 {
		t.Fatalf("2g allowed %v", got)
	}
	if ch, _ := b2.Channel(14); ch.Usable() || !ch.NeedsCountry() {
		t.Fatal("channel 14 is passive-only in the world domain")
	}
	p1, _ := c.For("radio1", "1e140000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0")
	b5, ok := p1.Band("5g")
	if !ok {
		t.Fatal("no 5g band")
	}
	if !reflect.DeepEqual(b5.HTModes(), []string{"VHT20", "VHT40", "VHT80"}) {
		t.Fatalf("5g modes %v (this chip has no 160 MHz)", b5.HTModes())
	}
	al := b5.Allowed()
	// 36-48, plus DFS 52-144; 149-165 are passive-only until a country is set
	if al[0] != 36 || al[len(al)-1] != 144 || len(al) != 20 {
		t.Fatalf("5g allowed %v", al)
	}
	if nc := b5.NeedCountry(); len(nc) != 5 || nc[0] != 149 || nc[4] != 165 {
		t.Fatalf("5g needs country: %v", nc)
	}
	if nc := b2.NeedCountry(); !reflect.DeepEqual(nc, []int{12, 13, 14}) {
		t.Fatalf("2g needs country: %v", nc)
	}
	for _, n := range []int{169, 173, 177} {
		if ch, _ := b5.Channel(n); !ch.Disabled {
			t.Fatalf("channel %d should be disabled", n)
		}
	}
	if ch, _ := b5.Channel(52); !ch.Radar || ch.Disabled {
		t.Fatalf("channel 52 should be DFS: %+v", ch)
	}
}

func TestForFallsBackToRadioNumber(t *testing.T) {
	c := real(t)
	if p, ok := c.For("radio1", ""); !ok || p.Name != "phy1" {
		t.Fatalf("%+v %v", p, ok)
	}
	if _, ok := c.For("wl0", ""); ok {
		t.Fatal("unknown radio matched")
	}
}

func TestEmptyAndGarbage(t *testing.T) {
	if c := Parse(""); len(c.Phys) != 0 {
		t.Fatal("empty input")
	}
	if c := Parse("##phy phy0\nnot iw output\n* 12 MHz [x]\n"); len(c.Phys) != 1 || len(c.Phys[0].Bands) != 0 {
		t.Fatalf("garbage: %+v", c)
	}
}
