package doctor

import (
	"strings"
	"testing"
)

func TestScriptCoversEverySpecAndOnlyReads(t *testing.T) {
	s := Script()
	for _, sp := range specs {
		if !strings.Contains(s, sp.name) {
			t.Errorf("script lacks %s", sp.name)
		}
	}
	for _, bad := range []string{"rm ", ">/etc", "uci set", "uci commit", "reboot", "apk add", "opkg install", "sysctl -w"} {
		if strings.Contains(s, bad) {
			t.Errorf("probe must only read; found %q", bad)
		}
	}
	for _, sp := range specs {
		if strings.ContainsAny(sp.name, " ;&|$`'\"") {
			t.Errorf("unsafe name in script: %q", sp.name)
		}
	}
}

func TestParseClassifiesAndHints(t *testing.T) {
	out := `##owrt-doctor
ok cmd uci
ok cmd ubus
no cmd jsonfilter
no cmd wg
no cmd apk
ok cmd opkg
ok file /proc/net/nf_conntrack
no file /etc/config/pbr
val uid 0
val acct 0
val nftset 0
`
	r := Parse(out)
	core, feat, opt := r.Problems()
	if core != 1 || feat != 0 {
		t.Errorf("counts core=%d feature=%d optional=%d\n%s", core, feat, opt, r.Text())
	}
	txt := r.Text()
	for _, want := range []string{"MISSING   jsonfilter", "install package wireguard-tools", "nf_conntrack_acct=1", "dnsmasq-full"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "MISSING   apk") {
		t.Error("with opkg present, a missing apk must not be reported")
	}
	if strings.Contains(txt, "logread") {
		t.Error("items the probe never reached must not be reported")
	}
}

func TestNonRootIsRequiredProblem(t *testing.T) {
	r := Parse("val uid 1000\n")
	if c, _, _ := r.Problems(); c != 1 {
		t.Fatalf("%s", r.Text())
	}
}
