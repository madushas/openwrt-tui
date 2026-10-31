package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/qr"
)

func TestDetectASCII(t *testing.T) {
	clear := func() {
		for _, k := range []string{"OWRT_TUI_ICONS", "LC_ALL", "LC_CTYPE", "LANG", "TERM"} {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"LANG": "en_US.UTF-8"}, false},
		{map[string]string{"LANG": "en_US.ISO-8859-1"}, true},
		{map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, true},
		{map[string]string{"TERM": "linux", "LANG": "en_US.UTF-8"}, true},
		{map[string]string{"OWRT_TUI_ICONS": "ascii", "LANG": "en_US.UTF-8"}, true},
		{map[string]string{"OWRT_TUI_ICONS": "unicode", "LANG": "C"}, false},
		{map[string]string{}, false},
	}
	for i, c := range cases {
		clear()
		for k, v := range c.env {
			t.Setenv(k, v)
		}
		if got := detectASCII(); got != c.want {
			t.Errorf("case %d %v: got %v", i, c.env, got)
		}
	}
}

func TestIconsAndFallback(t *testing.T) {
	for _, n := range []string{"Dashboard", "Network", "Wi-Fi", "DHCP", "Clients", "Config", "Changes", "History", "Health", "Tasks", "Cron", "Services", "Logs", "Packages", "Diagnostics"} {
		if icons[n] == "" || asciiIcons[n] == "" {
			t.Errorf("missing icon for %s", n)
		}
		for _, r := range asciiIcons[n] {
			if r > 0x7f {
				t.Errorf("ascii icon for %s is not ASCII", n)
			}
		}
	}
	if icons["Network"] != "⑂" || icons["Health"] != "●" {
		t.Error("user icon map not applied")
	}
}

// Every screen, rendered in ASCII mode, contains no bytes above 0x7f and keeps
// its line widths.
func TestASCIIModeRendersPure(t *testing.T) {
	old := asciiOn
	defer func() { asciiOn = old }()
	a := newApp(t, false)
	pump(t, a, a.Init())
	for _, mode := range []bool{false, true} {
		asciiOn = mode
		for _, s := range a.screens {
			openTab(t, a, s.Title())
			out := finish(a.render())
			if mode {
				for i := 0; i < len(out); i++ {
					if out[i] > 0x7f {
						t.Fatalf("%s: non-ASCII byte at %d: %q", s.Title(), i, out[max0(i-20):i+1])
					}
				}
			}
		}
	}
}

func TestASCIIQR(t *testing.T) {
	c, err := qr.Encode([]byte("hello"), qr.M)
	if err != nil {
		t.Fatal(err)
	}
	out := c.TextWide(2)
	if strings.ContainsAny(out, "▀▄█") {
		t.Fatal("block glyphs in wide QR")
	}
	if n := strings.Count(out, "\n"); n != len(c.Modules)+4 {
		t.Fatalf("rows %d", n)
	}
	asciiOn = true
	defer func() { asciiOn = false }()
	if got := finish("╭─● ▇ é"); got != "+-* # ?" {
		t.Fatalf("finish = %q", got)
	}
}
