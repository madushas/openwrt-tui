package transport

import (
	"context"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text\twith tab\nand newline\n", "plain text\twith tab\nand newline\n"},
		{"日本語 café ✓ 😀", "日本語 café ✓ 😀"},
		{"\x1b[31mred\x1b[0m", "[31mred[0m"},             // CSI introducer removed
		{"title\x1b]0;pwned\x07end", "title]0;pwnedend"}, // OSC and BEL removed
		{"a\u009b2Jb", "a2Jb"},                           // C1 CSI as a character
		{"a\x9b2Jb", "a�2Jb"},                            // the same byte alone is invalid UTF-8
		{"line1\r\nline2\rX", "line1\nline2X"},           // CR cannot overwrite earlier text
		{"user ‮evil.txt", "user evil.txt"},              // bidi override
		{"bad\xffbyte", "bad�byte"},                      // invalid UTF-8
		{"del\x7fchar\x00nul", "delcharnul"},
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Valid UTF-8 whose bytes include 0x80-0x9f as continuation bytes must survive.
	if s := "é€😀"; Sanitize(s) != s {
		t.Fatal("multi-byte characters were damaged")
	}
}

func TestFakeAppliesTheSameBoundary(t *testing.T) {
	f := (&Fake{Name: "x"}).On("hello", "ok\x1b]0;owned\x07\n")
	res, err := f.Run(context.Background(), "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(res.Stdout, "\x1b\x07") {
		t.Fatalf("escape survived: %q", res.Stdout)
	}
}
