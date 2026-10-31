package tui

import (
	"strings"
	"testing"
)

// No screen may show a secret from the demo router's configuration until the
// user asks to reveal it. The secrets below are in the demo UCI export.
func TestNoScreenShowsSecretsByDefault(t *testing.T) {
	secrets := []string{"DEMOdemoDEMOdemo", "DEMOpsk", "demo-wifi-passphrase"}
	for _, ro := range []bool{true, false} {
		a := newApp(t, ro)
		for _, s := range a.screens {
			openTab(t, a, s.Title())
			for _, sz := range [][2]int{{80, 24}, {120, 40}, {200, 50}} {
				a.w, a.h = sz[0], sz[1]
				v := plain(a.render())
				for _, sec := range secrets {
					if strings.Contains(v, sec) {
						t.Fatalf("%s (%dx%d, readOnly=%v) shows %q", s.Title(), sz[0], sz[1], ro, sec)
					}
				}
			}
		}
	}
}

// The Config screen shows option values, so walk into every file and section.
func TestConfigScreenMasksInsideEverySection(t *testing.T) {
	secrets := []string{"DEMOdemoDEMOdemo", "DEMOpsk", "demo-wifi-passphrase"}
	a := newApp(t, true)
	a.w, a.h = 160, 60
	openTab(t, a, "Config")
	sawValues := false
	for f := 0; f < 25; f++ {
		press(t, a, "enter")
		for sec := 0; sec < 30; sec++ {
			v := plain(a.render())
			for _, s := range secrets {
				if strings.Contains(v, s) {
					t.Fatalf("file %d section %d shows %q:\n%s", f, sec, s, v)
				}
			}
			if strings.Contains(v, "********") {
				sawValues = true
			}
			press(t, a, "enter") // open the section's options, if any
			v = plain(a.render())
			for _, s := range secrets {
				if strings.Contains(v, s) {
					t.Fatalf("file %d section %d options show %q:\n%s", f, sec, s, v)
				}
			}
			if strings.Contains(v, "********") {
				sawValues = true
			}
			press(t, a, "esc", "down")
		}
		press(t, a, "esc", "down")
	}
	if !sawValues {
		t.Fatal("the walk never reached a masked value, so it proves nothing")
	}
}

// The Config screen's reveal key is the only way to see them.
func TestConfigRevealIsExplicit(t *testing.T) {
	a := newApp(t, true)
	openTab(t, a, "Config")
	if v := plain(a.render()); strings.Contains(v, "demo-wifi-passphrase") {
		t.Fatal("secret visible before reveal")
	}
}
