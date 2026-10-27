package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/madushakv/owrt-tui/internal/discover"
	"github.com/madushakv/owrt-tui/internal/session"
	"github.com/madushakv/owrt-tui/internal/sshcheck"
	"github.com/madushakv/owrt-tui/internal/transport"
)

// Sys is everything the connect flow needs from the outside world, so tests
// can replace the network, the disk and the interactive ssh tools.
type Sys struct {
	Gateway    func() (string, error)
	Recent     func() []string
	AddRecent  func(target string)
	Hosts      func() []string
	Keys       func() []discover.Key
	NewKeyPath func() string
	ReadPubKey func(path string) (discover.Key, error)
	Dial       func(target string, extra []string) (transport.Transport, error)
	Scan       func(ctx context.Context, target string, extra []string) (sshcheck.HostKey, error)
	Pin        func(h sshcheck.HostKey) error
	KeygenCmd  func(path string) *exec.Cmd
	InstallCmd func(target string, extra []string, pub string) (*exec.Cmd, error)
	SaveHost   func(h discover.HostEntry) error
}

func defaultSys() *Sys {
	sshDir := discover.SSHDir()
	return &Sys{
		Gateway:    discover.Gateway,
		Recent:     func() []string { return discover.Recent(discover.ConfigDir()) },
		AddRecent:  func(t string) { _ = discover.AddRecent(discover.ConfigDir(), t) },
		Hosts:      func() []string { return discover.SSHConfigHosts(filepath.Join(sshDir, "config")) },
		Keys:       func() []discover.Key { return discover.PubKeys(sshDir) },
		NewKeyPath: func() string { return discover.NewKeyPath(sshDir) },
		ReadPubKey: discover.ReadPubKey,
		Dial: func(target string, extra []string) (transport.Transport, error) {
			return transport.NewSSH(target, true, extra...)
		},
		Scan:       sshcheck.Scan,
		Pin:        func(h sshcheck.HostKey) error { return h.Pin() },
		KeygenCmd:  sshcheck.KeygenCmd,
		InstallCmd: sshcheck.InstallCmd,
		SaveHost:   func(h discover.HostEntry) error { return discover.AppendHost(filepath.Join(sshDir, "config"), h) },
	}
}

// ConnectOpts configures RunConnect.
type ConnectOpts struct {
	Target, Identity, Port string
	Write                  bool
	Timeout                time.Duration
	Sys                    *Sys // nil means the real system
}

type cstate int

const (
	stPick cstate = iota
	stManual
	stTrying
	stScanning
	stHostKey
	stFail
	stNotOpenWrt
	stWizKey
	stWizInstall
	stWizBusy
	stSave
)

type cand struct{ label, target, note, origin string }

type connect struct {
	ctx  context.Context
	sys  *Sys
	opts ConnectOpts

	st      cstate
	cands   []cand
	cur     int
	input   textinput.Model
	inputEr string

	target, origin string
	identity       string // private key chosen by the wizard
	gen            int

	failKind sshcheck.Kind
	failMsg  string
	hk       sshcheck.HostKey

	keys        []discover.Key
	pub         discover.Key
	installed   bool
	pendingS    *session.Session
	pendingT    transport.Transport
	saveEntry   discover.HostEntry
	hosts       []string
	w, h        int
	gatewayNote string
	newKey      string // path of the key being generated
	notOpenWrt  bool   // connected, but the probe found no OpenWrt board info
}

type (
	candsMsg struct {
		cands []cand
		gwErr error
	}
	tryDoneMsg struct {
		gen    int
		target string
		s      *session.Session
		t      transport.Transport
		err    error
	}
	scanMsg struct {
		gen int
		hk  sshcheck.HostKey
		err error
	}
	pinDoneMsg     struct{ err error }
	keygenDoneMsg  struct{ err error }
	installDoneMsg struct{ err error }
	connectedMsg   struct {
		s *session.Session
		t transport.Transport
	}
)

func newConnect(ctx context.Context, o ConnectOpts) *connect {
	sys := o.Sys
	if sys == nil {
		sys = defaultSys()
	}
	in := textinput.New()
	in.Prompt = "router: "
	in.Placeholder = "192.168.1.1 or user@host or ssh alias"
	return &connect{ctx: ctx, sys: sys, opts: o, input: in, w: 80, h: 24}
}

func (c *connect) extraArgs() []string {
	var a []string
	if c.opts.Port != "" {
		a = append(a, "-p", c.opts.Port)
	}
	id := c.opts.Identity
	if c.identity != "" {
		id = c.identity
	}
	if id != "" {
		a = append(a, "-i", id)
	}
	return a
}

// portOnly is what the key-install ssh needs (not -i: the key is not accepted yet).
func (c *connect) portOnly() []string {
	if c.opts.Port != "" {
		return []string{"-p", c.opts.Port}
	}
	return nil
}

func (c *connect) Init() tea.Cmd {
	if c.opts.Target != "" {
		t, err := discover.ExplicitTarget(c.opts.Target)
		if err != nil {
			c.target = c.opts.Target
			c.st, c.failKind, c.failMsg = stFail, sshcheck.Other, err.Error()
			return nil
		}
		c.origin = "explicit"
		return c.try(t)
	}
	return func() tea.Msg {
		var cs []cand
		for _, r := range c.sys.Recent() {
			cs = append(cs, cand{label: r, target: r, note: "last used", origin: "recent"})
		}
		var gwErr error
		if gw, err := c.sys.Gateway(); err == nil {
			t := "root@" + gw
			dup := false
			for _, x := range cs {
				dup = dup || x.target == t
			}
			if !dup {
				cs = append(cs, cand{label: t, target: t, note: "your default gateway (a guess: it may not be the OpenWrt router)", origin: "gateway"})
			}
		} else {
			gwErr = err
		}
		for _, h := range c.sys.Hosts() {
			cs = append(cs, cand{label: h, target: h, note: "from ~/.ssh/config", origin: "config"})
		}
		cs = append(cs, cand{label: "Type an address...", origin: "manual"})
		return candsMsg{cs, gwErr}
	}
}

func (c *connect) try(target string) tea.Cmd {
	c.target = target
	c.st = stTrying
	c.gen++
	gen, extra, write, timeout := c.gen, c.extraArgs(), c.opts.Write, c.opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return func() tea.Msg {
		t, err := c.sys.Dial(target, extra)
		if err != nil {
			return tryDoneMsg{gen: gen, target: target, err: err}
		}
		// Retrying is only safe when nothing can change on the router.
		var tt transport.Transport = t
		if !write {
			tt = transport.Retry{T: t, Attempts: 1}
		}
		ctx, cancel := context.WithTimeout(c.ctx, timeout)
		defer cancel()
		s, err := session.Connect(ctx, tt, !write)
		if err != nil {
			tt.Close()
			return tryDoneMsg{gen: gen, target: target, err: err}
		}
		return tryDoneMsg{gen: gen, target: target, s: s, t: tt}
	}
}

func looksLikeOpenWrt(s *session.Session) bool {
	return s.Info.Tools["ubus"] || s.Info.Tools["uci"] || s.Info.Distro != ""
}

func (c *connect) fail(kind sshcheck.Kind, msg string) {
	c.st, c.failKind, c.failMsg = stFail, kind, msg
}

// Update handles a message and returns a command. It returns a connectedMsg
// command when a session is ready.
func (c *connect) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case candsMsg:
		c.cands = m.cands
		if m.gwErr != nil {
			c.gatewayNote = "could not find the default gateway: " + m.gwErr.Error()
		}
		return nil
	case tryDoneMsg:
		return c.tryDone(m)
	case scanMsg:
		if m.gen != c.gen {
			return nil
		}
		if m.err != nil {
			c.fail(sshcheck.HostKeyUnknown, "could not read the host key: "+m.err.Error())
			return nil
		}
		c.hk, c.st = m.hk, stHostKey
		return nil
	case pinDoneMsg:
		if m.err != nil {
			c.fail(sshcheck.Other, "could not save the host key: "+m.err.Error())
			return nil
		}
		return c.try(c.target)
	case keygenDoneMsg:
		if m.err != nil {
			c.fail(sshcheck.Other, "key creation failed: "+m.err.Error())
			return nil
		}
		k, err := c.sys.ReadPubKey(c.genPath() + ".pub")
		if err != nil {
			c.fail(sshcheck.Other, err.Error())
			return nil
		}
		c.setKey(k)
		return nil
	case installDoneMsg:
		if m.err != nil {
			c.fail(sshcheck.AuthDenied, "installing the key failed: "+m.err.Error()+
				". Common causes: wrong password, the router has no root password yet (set one in LuCI or on the console), or password login is turned off.")
			c.installed = false
			return nil
		}
		c.installed = true
		return c.try(c.target)
	case tea.KeyPressMsg:
		return c.key(m)
	}
	return nil
}

func (c *connect) genPath() string { return c.newKey }

func (c *connect) setKey(k discover.Key) {
	c.pub = k
	if !discover.IsDefaultIdentity(k.Private) {
		c.identity = k.Private
	} else {
		c.identity = ""
	}
	c.st = stWizInstall
}

func (c *connect) tryDone(m tryDoneMsg) tea.Cmd {
	if m.gen != c.gen {
		if m.t != nil {
			m.t.Close() // a stale attempt the user already left
		}
		return nil
	}
	if m.err != nil {
		kind := sshcheck.Classify(m.err.Error())
		if kind == sshcheck.HostKeyUnknown {
			c.st = stScanning
			gen, target, extra := c.gen, c.target, c.extraArgs()
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
				defer cancel()
				hk, err := c.sys.Scan(ctx, target, extra)
				return scanMsg{gen, hk, err}
			}
		}
		msg := m.err.Error()
		c.notOpenWrt = strings.Contains(msg, "is this an OpenWrt device")
		if kind == sshcheck.AuthDenied && c.installed {
			msg += ". The key was installed but is still refused. If it has a passphrase, load it with: ssh-add " + c.identityOrPub() + " (owrt-tui cannot ask for passphrases)."
		}
		c.fail(kind, msg)
		return nil
	}
	if !looksLikeOpenWrt(m.s) {
		c.pendingS, c.pendingT, c.st = m.s, m.t, stNotOpenWrt
		return nil
	}
	return c.succeed(m.s, m.t)
}

func (c *connect) identityOrPub() string {
	if c.identity != "" {
		return c.identity
	}
	if c.pub.Private != "" {
		return c.pub.Private
	}
	return "<your key>"
}

func (c *connect) succeed(s *session.Session, t transport.Transport) tea.Cmd {
	c.sys.AddRecent(c.target)
	c.pendingS, c.pendingT = s, t
	if c.origin == "gateway" || c.origin == "manual" {
		alias := discover.SafeAlias(s.Info.Hostname)
		exists := false
		for _, h := range c.sys.Hosts() {
			exists = exists || h == alias
		}
		if !exists {
			user, host, _ := strings.Cut(c.target, "@")
			e := discover.HostEntry{Alias: alias, HostName: host, User: user, Port: c.opts.Port, IdentityFile: c.identity}
			if _, err := e.Render(); err == nil {
				c.saveEntry, c.st = e, stSave
				return nil
			}
		}
	}
	return c.done()
}

func (c *connect) done() tea.Cmd {
	s, t := c.pendingS, c.pendingT
	c.pendingS, c.pendingT = nil, nil
	return func() tea.Msg { return connectedMsg{s, t} }
}

func (c *connect) closePending() {
	if c.pendingT != nil {
		c.pendingT.Close()
		c.pendingT, c.pendingS = nil, nil
	}
}

// Capturing is true while the address field has focus.
func (c *connect) Capturing() bool { return c.st == stManual }

func (c *connect) backToPick() {
	c.closePending()
	c.st, c.inputEr = stPick, ""
	c.installed = false
}

func (c *connect) startManual(prefill string) tea.Cmd {
	c.st, c.inputEr = stManual, ""
	c.input.SetValue(prefill)
	return c.input.Focus()
}

func (c *connect) key(m tea.KeyPressMsg) tea.Cmd {
	k := m.String()
	switch c.st {
	case stPick:
		switch k {
		case "up", "k":
			c.cur = clampInt(c.cur-1, 0, len(c.cands)-1)
		case "down", "j":
			c.cur = clampInt(c.cur+1, 0, len(c.cands)-1)
		case "e", "/":
			return c.startManual("")
		case "enter":
			if len(c.cands) == 0 {
				return nil
			}
			cd := c.cands[c.cur]
			if cd.origin == "manual" {
				return c.startManual("")
			}
			c.origin = cd.origin
			return c.try(cd.target)
		}
	case stManual:
		switch k {
		case "esc":
			c.input.Blur()
			c.backToPick()
		case "enter":
			t, err := discover.NormalizeTarget(c.input.Value())
			if err != nil {
				c.inputEr = err.Error()
				return nil
			}
			c.input.Blur()
			c.origin = "manual"
			return c.try(t)
		default:
			var cmd tea.Cmd
			c.input, cmd = c.input.Update(m)
			return cmd
		}
	case stTrying, stScanning:
		if k == "esc" {
			c.gen++ // abandon the attempt; its result is ignored
			c.backToPick()
		}
	case stHostKey:
		switch k {
		case "y", "Y":
			hk := c.hk
			return func() tea.Msg { return pinDoneMsg{c.sys.Pin(hk)} }
		case "n", "N", "esc":
			c.backToPick()
		}
	case stFail:
		switch k {
		case "r":
			return c.try(c.target)
		case "e", "esc":
			return c.startManual(c.target)
		case "a":
			c.backToPick()
		case "w":
			if c.failKind == sshcheck.AuthDenied {
				c.keys = c.sys.Keys()
				c.cur, c.st = 0, stWizKey
			}
		}
	case stNotOpenWrt:
		switch k {
		case "y", "Y":
			return c.succeed(c.pendingS, c.pendingT)
		case "n", "N", "esc":
			c.backToPick()
		}
	case stWizKey:
		n := len(c.keys) + 1
		switch k {
		case "up", "k":
			c.cur = clampInt(c.cur-1, 0, n-1)
		case "down", "j":
			c.cur = clampInt(c.cur+1, 0, n-1)
		case "esc":
			c.st = stFail
		case "enter":
			if c.cur < len(c.keys) {
				c.setKey(c.keys[c.cur])
				return nil
			}
			path := c.sys.NewKeyPath()
			c.newKey = path
			c.st = stWizBusy
			return tea.ExecProcess(c.sys.KeygenCmd(path), func(err error) tea.Msg { return keygenDoneMsg{err} })
		}
	case stWizInstall:
		switch k {
		case "esc":
			c.st = stWizKey
		case "enter":
			cmd, err := c.sys.InstallCmd(c.target, c.portOnly(), c.pub.Line)
			if err != nil {
				c.fail(sshcheck.Other, err.Error())
				return nil
			}
			c.st = stWizBusy
			return tea.ExecProcess(cmd, func(err error) tea.Msg { return installDoneMsg{err} })
		}
	case stSave:
		switch k {
		case "y", "Y":
			if err := c.sys.SaveHost(c.saveEntry); err != nil {
				return tea.Batch(toastErr(err), c.done())
			}
			return c.done()
		case "n", "N", "esc":
			return c.done()
		}
	}
	return nil
}

// ---- view ----

func (c *connect) View(w, h int) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(" " + s + "\n") }
	switch c.st {
	case stPick:
		line("Which router do you want to manage?")
		b.WriteString("\n")
		for i, cd := range c.cands {
			row := fmt.Sprintf("  %-26s %s", cd.label, sDim.Render(cd.note))
			if i == c.cur {
				row = sSel.Render(clip(stripPad(fmt.Sprintf("  %-26s %s", cd.label, cd.note)), w))
			}
			b.WriteString(row + "\n")
		}
		if c.gatewayNote != "" {
			b.WriteString("\n " + sDim.Render(c.gatewayNote) + "\n")
		}
		b.WriteString("\n " + sDim.Render("Nothing is contacted until you press enter. Connection uses your normal ssh setup (keys or agent; no password prompts here).") + "\n")
	case stManual:
		line("Type the router's address.")
		b.WriteString(" " + c.input.View() + "\n")
		if c.inputEr != "" {
			line(sBad.Render(c.inputEr))
		}
		line(sDim.Render("A bare address gets root@ in front."))
	case stTrying:
		line("Connecting to " + sBold.Render(c.target) + " ...")
		line(sDim.Render("esc cancels"))
	case stScanning:
		line("Reading the host key of " + sBold.Render(c.target) + " ...")
	case stHostKey:
		line(sWarn.Render("First connection to " + c.target))
		b.WriteString("\n")
		line("The router presented this host key:")
		line("  " + sBold.Render(c.hk.Fingerprint) + "  (" + c.hk.Type + ")")
		b.WriteString("\n")
		line("Compare it with the router if you can (on the router: dropbearkey -y -f /etc/dropbear/dropbear_ed25519_host_key).")
		line("If it matches, or you trust this network, save it and continue.")
		line(sDim.Render("Saves exactly this key to " + c.hk.KnownHosts))
	case stFail:
		line(sBad.Render("Could not connect to " + c.target + ": " + c.failKind.String()))
		b.WriteString("\n")
		hint := sshcheck.Hint(c.failKind, c.target)
		if c.notOpenWrt {
			hint = c.target + " answered over SSH, but it does not look like OpenWrt. The default gateway is often a different device (an ISP modem, a switch). Try another address."
		}
		for _, l := range wrap(hint, w-3) {
			line(l)
		}
		b.WriteString("\n")
		if c.failKind == sshcheck.HostKeyChanged {
			if o := sshcheck.OffendingKey(c.failMsg); o != "" {
				line(sDim.Render("Saved key location: " + o))
			}
			line("If you know why it changed, remove the old entry yourself with:")
			line("  " + sBold.Render("ssh-keygen -R "+discover.HostOf(c.target)))
		} else {
			for _, l := range wrap("ssh said: "+c.failMsg, w-3) {
				line(sDim.Render(l))
			}
		}
	case stNotOpenWrt:
		line(sWarn.Render(c.target + " answered, but it does not look like OpenWrt."))
		line("The default gateway is often a different device (an ISP modem, a switch).")
		line("Continue anyway?")
	case stWizKey:
		line(sAccent.Render("Set up key login") + " for " + c.target)
		b.WriteString("\n")
		line("Pick the key to install on the router:")
		for i, k := range c.keys {
			row := fmt.Sprintf("  %-34s %s", filepath.Base(k.Path), k.Type+" "+k.Comment)
			if i == c.cur {
				row = sSel.Render(clip(row, w))
			}
			b.WriteString(row + "\n")
		}
		row := "  Create a new ed25519 key"
		if c.cur == len(c.keys) {
			row = sSel.Render(clip(row, w))
		}
		b.WriteString(row + "\n")
	case stWizInstall:
		line(sAccent.Render("Install the key on ") + c.target)
		b.WriteString("\n")
		line("Key: " + filepath.Base(c.pub.Path) + " (" + c.pub.Type + ")")
		b.WriteString("\n")
		for _, l := range wrap("owrt-tui will pause and run ssh in your terminal. ssh asks for the router's root password itself; this program never sees it. The key is appended to /etc/dropbear/authorized_keys. A fresh OpenWrt install has no root password; set one first in LuCI (System > Administration) or on the console.", w-3) {
			line(l)
		}
		b.WriteString("\n")
		line("enter to continue, esc to pick another key")
	case stWizBusy:
		line("Working in your terminal...")
	case stSave:
		line(sGood.Render("Connected to "+c.target) + ".")
		b.WriteString("\n")
		line("Add this to ~/.ssh/config so you can use the short name next time?")
		for _, l := range strings.Split(strings.TrimSpace(mustRender(c.saveEntry)), "\n") {
			line(sDim.Render("  " + l))
		}
	}
	return b.String()
}

func mustRender(e discover.HostEntry) string {
	s, _ := e.Render()
	return s
}

func (c *connect) Keys() string {
	switch c.st {
	case stPick:
		return "up/down choose  enter connect  e type an address"
	case stManual:
		return "enter connect  esc back"
	case stTrying, stScanning:
		return "esc cancel"
	case stHostKey:
		return "y save and continue  n cancel"
	case stFail:
		if c.failKind == sshcheck.AuthDenied {
			return "w set up key login  r retry  e other address  a address list"
		}
		if c.failKind == sshcheck.HostKeyChanged {
			return "e other address  a address list"
		}
		return "r retry  e other address  a address list"
	case stNotOpenWrt:
		return "y continue anyway  n back"
	case stWizKey:
		return "up/down choose  enter select  esc back"
	case stWizInstall:
		return "enter run ssh  esc back"
	case stSave:
		return "y add to config  n skip"
	}
	return ""
}

// wrap breaks s into lines of at most w cells, on spaces.
func wrap(s string, w int) []string {
	if w < 20 {
		w = 20
	}
	var out []string
	cur := ""
	for _, word := range strings.Fields(s) {
		if cur != "" && lipgloss.Width(cur)+1+lipgloss.Width(word) > w {
			out = append(out, cur)
			cur = word
		} else if cur == "" {
			cur = word
		} else {
			cur += " " + word
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
