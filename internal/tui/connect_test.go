package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/demo"
	"github.com/madushakv/owrt-tui/internal/discover"
	"github.com/madushakv/owrt-tui/internal/sshcheck"
	"github.com/madushakv/owrt-tui/internal/transport"
)

// errTransport fails every command the way a failed ssh does.
type errTransport struct{ msg string }

func (e errTransport) Run(context.Context, string, io.Reader) (transport.Result, error) {
	return transport.Result{}, fmt.Errorf("%w: %s", transport.ErrConn, e.msg)
}
func (e errTransport) Describe() string { return "err" }
func (e errTransport) Close() error     { return nil }

type closeSpy struct {
	transport.Transport
	mu     sync.Mutex
	closed int
}

func (c *closeSpy) Close() error { c.mu.Lock(); c.closed++; c.mu.Unlock(); return nil }

type harness struct {
	t       *testing.T
	root    *Root
	sys     *Sys
	dials   []dialCall
	next    func(target string, extra []string) transport.Transport
	pinned  []sshcheck.HostKey
	saved   []discover.HostEntry
	recent  []string
	spies   []*closeSpy
	install struct {
		target string
		extra  []string
		pub    string
	}
}

type dialCall struct {
	target string
	extra  []string
}

func newHarness(t *testing.T, o ConnectOpts) *harness {
	h := &harness{t: t}
	h.sys = &Sys{
		Gateway:    func() (string, error) { return "192.0.2.1", nil },
		Recent:     func() []string { return nil },
		AddRecent:  func(x string) { h.recent = append(h.recent, x) },
		Hosts:      func() []string { return []string{"gw-lab"} },
		Keys:       func() []discover.Key { return nil },
		NewKeyPath: func() string { return "/tmp/none/id_ed25519" },
		ReadPubKey: func(string) (discover.Key, error) { return discover.Key{}, errors.New("unused") },
		Dial: func(target string, extra []string) (transport.Transport, error) {
			h.dials = append(h.dials, dialCall{target, append([]string(nil), extra...)})
			tr := h.next(target, extra)
			spy := &closeSpy{Transport: tr}
			h.spies = append(h.spies, spy)
			return spy, nil
		},
		Scan: func(context.Context, string, []string) (sshcheck.HostKey, error) {
			return sshcheck.HostKey{Line: "192.0.2.1 ssh-ed25519 AAAAed", Type: "ssh-ed25519", Fingerprint: "SHA256:abc", KnownHosts: "/tmp/kh"}, nil
		},
		Pin:       func(k sshcheck.HostKey) error { h.pinned = append(h.pinned, k); return nil },
		KeygenCmd: func(p string) *exec.Cmd { return exec.Command("true") },
		InstallCmd: func(target string, extra []string, pub string) (*exec.Cmd, error) {
			h.install.target, h.install.extra, h.install.pub = target, extra, pub
			return exec.Command("true"), nil
		},
		SaveHost: func(e discover.HostEntry) error { h.saved = append(h.saved, e); return nil },
	}
	o.Sys = h.sys
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	ctx := context.Background()
	h.root = &Root{ctx: ctx, opts: o, c: newConnect(ctx, o), w: 100, h: 30}
	h.next = func(string, []string) transport.Transport { return demo.Router() }
	return h
}

// run feeds cmd's messages back into the root. ExecProcess commands (which
// need a real terminal) are not run; the tests inject their results.
func (h *harness) run(cmd tea.Cmd) {
	// Once the main app exists it polls the router forever; the connect tests
	// only need to know that it started.
	if cmd == nil || h.root.app != nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				h.run(c)
			}
			return
		}
		if msg == nil {
			return
		}
		if fmt.Sprintf("%T", msg) == "tea.execMsg" {
			return
		}
		_, next := h.root.Update(msg)
		h.run(next)
	case <-time.After(5 * time.Second): // read-only sessions wait out the retry delay
	}
}

func (h *harness) start() { h.run(h.root.Init()) }

func (h *harness) send(m tea.Msg) { _, c := h.root.Update(m); h.run(c) }

func (h *harness) key(keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter":
			h.send(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			h.send(tea.KeyPressMsg{Code: tea.KeyEscape})
		case "down":
			h.send(tea.KeyPressMsg{Code: tea.KeyDown})
		default:
			r := []rune(k)
			h.send(tea.KeyPressMsg{Code: r[0], Text: k})
		}
	}
}

func (h *harness) view() string { return h.root.render() }

func (h *harness) mustContain(s string) {
	h.t.Helper()
	if v := h.view(); !strings.Contains(v, s) {
		h.t.Fatalf("view missing %q:\n%s", s, v)
	}
}

func TestPickListNeverDialsUntilEnter(t *testing.T) {
	h := newHarness(t, ConnectOpts{})
	h.start()
	if len(h.dials) != 0 {
		t.Fatal("dialed before the user pressed enter")
	}
	h.mustContain("root@192.0.2.1")
	h.mustContain("default gateway (a guess")
	h.mustContain("gw-lab")
	h.mustContain("Type an address")
}

func TestGatewayConnectOffersToSaveHostThenOpensApp(t *testing.T) {
	h := newHarness(t, ConnectOpts{})
	h.start()
	h.key("enter") // the gateway is the first candidate
	if len(h.dials) != 1 || h.dials[0].target != "root@192.0.2.1" {
		t.Fatalf("dials: %+v", h.dials)
	}
	h.mustContain("Add this to ~/.ssh/config")
	h.mustContain("HostName 192.0.2.1")
	h.key("y")
	if len(h.saved) != 1 || h.saved[0].Alias != "gw-demo" || h.saved[0].HostName != "192.0.2.1" || h.saved[0].User != "root" {
		t.Fatalf("saved: %+v", h.saved)
	}
	if h.root.app == nil {
		t.Fatal("main app did not start after connecting")
	}
	if len(h.recent) != 1 || h.recent[0] != "root@192.0.2.1" {
		t.Fatalf("recent: %v", h.recent)
	}
	if _, ok := h.root.app.env.S.T.(transport.Retry); !ok {
		t.Fatal("read-only session should retry once on dropped connections")
	}
}

func TestDecliningSaveWritesNothing(t *testing.T) {
	h := newHarness(t, ConnectOpts{})
	h.start()
	h.key("enter", "n")
	if len(h.saved) != 0 || h.root.app == nil {
		t.Fatalf("saved=%v app=%v", h.saved, h.root.app != nil)
	}
}

func TestWriteSessionIsNeverRetried(t *testing.T) {
	h := newHarness(t, ConnectOpts{Write: true})
	h.start()
	h.key("enter", "n")
	if h.root.app == nil {
		t.Fatal("no app")
	}
	if _, ok := h.root.app.env.S.T.(transport.Retry); ok {
		t.Fatal("a write session must not replay commands after a dropped connection")
	}
	if h.root.app.env.S.ReadOnly {
		t.Fatal("-write session came up read-only")
	}
}

func TestExplicitTargetConnectsWithoutAsking(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "admin@10.0.0.1", Port: "2222"})
	h.start()
	if len(h.dials) != 1 || h.dials[0].target != "admin@10.0.0.1" || strings.Join(h.dials[0].extra, " ") != "-p 2222" {
		t.Fatalf("dials: %+v", h.dials)
	}
	if h.root.app == nil {
		t.Fatal("did not connect")
	}
}

func TestExplicitTargetThatLooksLikeAnOptionIsRejected(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "-oProxyCommand=evil"})
	h.start()
	if len(h.dials) != 0 {
		t.Fatal("dialed an option-looking target")
	}
	h.mustContain("not a valid")
}

func TestRefusedExplainsAndOffersRetry(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1"})
	h.next = func(string, []string) transport.Transport {
		return errTransport{"ssh: connect to host 192.0.2.1 port 22: Connection refused"}
	}
	h.start()
	h.mustContain("connection refused")
	h.mustContain("dropbear")
	h.key("w") // the key wizard is for authentication failures only
	if h.root.c.st != stFail {
		t.Fatal("wizard opened for a non-auth failure")
	}
	if len(h.spies) == 0 || h.spies[0].closed == 0 {
		t.Fatal("failed attempt left its transport open")
	}
}

func TestUnknownHostKeyShowsFingerprintAndPinsOnlyAfterYes(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1"})
	first := true
	h.next = func(string, []string) transport.Transport {
		if first {
			first = false
			return errTransport{"Host key verification failed."}
		}
		return demo.Router()
	}
	h.start()
	h.mustContain("SHA256:abc")
	if len(h.pinned) != 0 {
		t.Fatal("pinned before the user agreed")
	}
	h.key("y")
	if len(h.pinned) != 1 || h.pinned[0].Fingerprint != "SHA256:abc" {
		t.Fatalf("pinned: %+v", h.pinned)
	}
	if len(h.dials) != 2 || h.root.app == nil {
		t.Fatalf("expected a second, successful dial; dials=%d app=%v", len(h.dials), h.root.app != nil)
	}
}

func TestDecliningHostKeyPinsNothing(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1"})
	h.next = func(string, []string) transport.Transport { return errTransport{"Host key verification failed."} }
	h.start()
	h.key("n")
	if len(h.pinned) != 0 || h.root.c.st != stPick {
		t.Fatalf("pinned=%v state=%v", h.pinned, h.root.c.st)
	}
}

func TestChangedHostKeyOffersNoWayThrough(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1"})
	h.next = func(string, []string) transport.Transport {
		return errTransport{"@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@; Offending ED25519 key in /home/u/.ssh/known_hosts:7; Host key verification failed."}
	}
	h.start()
	h.mustContain("host key changed")
	h.mustContain("ssh-keygen -R 192.0.2.1")
	h.mustContain("known_hosts:7")
	h.key("y", "w", "r") // none of these may bypass the warning
	if len(h.pinned) != 0 || h.root.app != nil || h.root.c.st == stHostKey || h.root.c.st == stWizKey {
		t.Fatalf("state=%v pinned=%v", h.root.c.st, h.pinned)
	}
	if len(h.dials) != 2 { // "r" retries the same strict connection; that is allowed
		t.Fatalf("dials=%d", len(h.dials))
	}
}

func TestAuthFailureLeadsToKeyWizardAndInstall(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1", Port: "2222"})
	h.sys.Keys = func() []discover.Key {
		return []discover.Key{{Path: "/home/u/.ssh/id_ed25519_owrt.pub", Private: "/home/u/.ssh/id_ed25519_owrt", Type: "ssh-ed25519", Comment: "me", Line: "ssh-ed25519 AAAAkey me"}}
	}
	denied := true
	h.next = func(string, []string) transport.Transport {
		if denied {
			return errTransport{"root@192.0.2.1: Permission denied (publickey,password)."}
		}
		return demo.Router()
	}
	h.start()
	h.mustContain("did not accept any of your keys")
	h.key("w")
	h.mustContain("id_ed25519_owrt.pub")
	h.mustContain("Create a new ed25519 key")
	h.key("enter") // choose the existing key
	h.mustContain("never sees it")
	h.key("enter") // run the install command
	if h.install.target != "root@192.0.2.1" || strings.Join(h.install.extra, " ") != "-p 2222" || h.install.pub != "ssh-ed25519 AAAAkey me" {
		t.Fatalf("install called with %+v", h.install)
	}
	denied = false
	h.send(installDoneMsg{nil})
	last := h.dials[len(h.dials)-1]
	if strings.Join(last.extra, " ") != "-p 2222 -i /home/u/.ssh/id_ed25519_owrt" {
		t.Fatalf("retry did not use the installed key: %+v", last)
	}
	if h.root.app == nil {
		t.Fatal("did not connect after installing the key")
	}
}

func TestInstallFailureExplainsWhy(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1"})
	h.sys.Keys = func() []discover.Key {
		return []discover.Key{{Path: "/k/id_ed25519.pub", Private: "/k/id_ed25519", Type: "ssh-ed25519", Line: "ssh-ed25519 AAAAk"}}
	}
	h.next = func(string, []string) transport.Transport { return errTransport{"Permission denied (publickey)."} }
	h.start()
	h.key("w", "enter", "enter")
	h.send(installDoneMsg{errors.New("exit status 255")})
	h.mustContain("no root password")
}

func TestStillDeniedAfterInstallMentionsSSHAdd(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1"})
	h.sys.Keys = func() []discover.Key {
		return []discover.Key{{Path: "/k/id_ed25519_x.pub", Private: "/k/id_ed25519_x", Type: "ssh-ed25519", Line: "ssh-ed25519 AAAAk"}}
	}
	h.next = func(string, []string) transport.Transport { return errTransport{"Permission denied (publickey)."} }
	h.start()
	h.key("w", "enter", "enter")
	h.send(installDoneMsg{nil})
	h.mustContain("ssh-add /k/id_ed25519_x")
}

func TestNotOpenWrtAsksBeforeContinuing(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1"})
	h.next = func(string, []string) transport.Transport {
		return (&transport.Fake{Name: "odd"}).On("##board", "##board\n{\"hostname\":\"odd\",\"board_name\":\"x\"}\n##uname\narmv7l\n##tools\nip\n##swconfig\n##df\n##uptime\n12.0 1.0\n")
	}
	h.start()
	h.mustContain("does not look like OpenWrt")
	if h.root.app != nil {
		t.Fatal("went straight in")
	}
	h.key("n")
	if h.root.c.st != stPick || h.spies[0].closed == 0 {
		t.Fatalf("declining should close the connection and go back (state %v, closed %d)", h.root.c.st, h.spies[0].closed)
	}
}

func TestNoBoardInfoSaysItIsNotOpenWrt(t *testing.T) {
	h := newHarness(t, ConnectOpts{Target: "root@192.0.2.1", Write: true})
	h.next = func(string, []string) transport.Transport {
		return (&transport.Fake{Name: "modem"}).On("##board", "##board\n##uname\narmv7l\n##tools\nip\n##swconfig\n##df\n##uptime\n12.0 1.0\n")
	}
	h.start()
	h.mustContain("does not look like OpenWrt")
	if h.root.app != nil {
		t.Fatal("went straight in")
	}
}

func TestEscapeAbandonsAttemptAndLateResultIsDropped(t *testing.T) {
	h := newHarness(t, ConnectOpts{})
	h.start()
	h.root.c.origin = "gateway"
	cmd := h.root.c.try("root@192.0.2.1") // starts attempt, but do not run it yet
	h.key("esc")
	msg := cmd() // the attempt finishes after the user left
	h.send(msg)
	if h.root.app != nil || h.root.c.st != stPick {
		t.Fatalf("late result changed the screen: state=%v app=%v", h.root.c.st, h.root.app != nil)
	}
	if h.spies[0].closed == 0 {
		t.Fatal("abandoned connection was not closed")
	}
}

func TestManualAddressIsNormalisedAndValidated(t *testing.T) {
	h := newHarness(t, ConnectOpts{})
	h.start()
	h.key("e")
	if !h.root.c.Capturing() {
		t.Fatal("address field not focused")
	}
	for _, r := range "-oBad" {
		h.key(string(r))
	}
	h.key("enter")
	if len(h.dials) != 0 {
		t.Fatal("bad address was dialed")
	}
	h.mustContain("cannot start with")
	h.root.c.input.SetValue("10.1.1.1")
	h.key("enter")
	if len(h.dials) != 1 || h.dials[0].target != "root@10.1.1.1" {
		t.Fatalf("dials: %+v", h.dials)
	}
}

func TestQKeyTypesIntoAddressFieldInsteadOfQuitting(t *testing.T) {
	h := newHarness(t, ConnectOpts{})
	h.start()
	h.key("e")
	_, cmd := h.root.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q quit while typing an address")
		}
	}
}

func TestConnectScreenFitsSmallTerminals(t *testing.T) {
	h := newHarness(t, ConnectOpts{})
	h.start()
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {41, 9}} {
		h.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		if n := len(strings.Split(h.view(), "\n")); n != sz[1] {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], n)
		}
	}
}
