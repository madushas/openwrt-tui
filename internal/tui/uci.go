package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

type uciScreen struct {
	e       *env
	cfgs    []uci.Config
	level   int // 0 configs, 1 sections, 2 options
	cur     [3]int
	reveal  bool
	loading bool
	err     error
	input   textinput.Model
	editing bool
}

func newUCI(e *env) *uciScreen {
	in := textinput.New()
	in.Prompt = "value: "
	return &uciScreen{e: e, loading: true, input: in}
}

func (u *uciScreen) Title() string   { return "Config" }
func (u *uciScreen) Capturing() bool { return u.editing }
func (u *uciScreen) Keys() string {
	return "enter open  esc back  e edit  d delete  v reveal secrets  g reload"
}

type uciMsg struct {
	cfgs []uci.Config
	err  error
}

func (u *uciScreen) Init() tea.Cmd { return u.load() }

func (u *uciScreen) load() tea.Cmd {
	u.loading = true
	return u.e.call(func(ctx context.Context) tea.Msg {
		c, err := u.e.S.LoadUCI(ctx)
		return uciMsg{c, err}
	})
}

func (u *uciScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case uciMsg:
		u.loading, u.err = false, m.err
		if m.err == nil {
			u.cfgs = m.cfgs
			u.fixCursors()
		}
	case uciChangedMsg:
		return u, u.load()
	case tea.KeyPressMsg:
		return u.key(m)
	}
	return u, nil
}

func (u *uciScreen) sections() []uci.Section {
	if u.cur[0] < len(u.cfgs) {
		return u.cfgs[u.cur[0]].Sections
	}
	return nil
}

func (u *uciScreen) options() []uci.Option {
	if s := u.sections(); u.cur[1] < len(s) {
		return s[u.cur[1]].Options
	}
	return nil
}

func (u *uciScreen) count() int {
	switch u.level {
	case 0:
		return len(u.cfgs)
	case 1:
		return len(u.sections())
	}
	return len(u.options())
}

func (u *uciScreen) fixCursors() {
	u.cur[0] = clampInt(u.cur[0], 0, len(u.cfgs)-1)
	u.cur[1] = clampInt(u.cur[1], 0, len(u.sections())-1)
	u.cur[2] = clampInt(u.cur[2], 0, len(u.options())-1)
}

func (u *uciScreen) key(m tea.KeyPressMsg) (screen, tea.Cmd) {
	if u.editing {
		return u.editKey(m)
	}
	switch m.String() {
	case "up", "k":
		u.cur[u.level] = clampInt(u.cur[u.level]-1, 0, u.count()-1)
	case "down", "j":
		u.cur[u.level] = clampInt(u.cur[u.level]+1, 0, u.count()-1)
	case "enter", "right", "l":
		if u.level < 2 && u.count() > 0 {
			u.level++
			u.cur[u.level] = 0
		}
	case "esc", "left", "h", "backspace":
		if u.level > 0 {
			u.level--
		}
	case "v":
		u.reveal = !u.reveal
		if u.reveal {
			return u, toast("secrets are visible; press v to hide them again")
		}
	case "g":
		return u, u.load()
	case "e":
		return u.startEdit()
	case "d":
		return u.stageDelete()
	}
	return u, nil
}

func (u *uciScreen) readOnlyMsg() tea.Cmd {
	return toast("read-only mode: start with -write to stage edits")
}

func (u *uciScreen) startEdit() (screen, tea.Cmd) {
	if u.level != 2 || len(u.options()) == 0 {
		return u, nil
	}
	if u.e.S.ReadOnly {
		return u, u.readOnlyMsg()
	}
	o := u.options()[u.cur[2]]
	if o.IsList {
		return u, toast("list options cannot be edited here yet")
	}
	u.editing = true
	if uci.IsSecret(o.Key) {
		u.input.SetValue("") // never prefill a secret
		u.input.Placeholder = "new secret (leave empty to cancel)"
	} else {
		u.input.SetValue(o.Value())
		u.input.Placeholder = ""
	}
	return u, u.input.Focus()
}

func (u *uciScreen) editKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		u.editing = false
		u.input.Blur()
		return u, nil
	case "enter":
		u.editing = false
		u.input.Blur()
		o := u.options()[u.cur[2]]
		v := u.input.Value()
		if uci.IsSecret(o.Key) && v == "" {
			return u, nil
		}
		cfg := u.cfgs[u.cur[0]]
		sec := cfg.Sections[u.cur[1]]
		err := u.e.Staged.Add(changes.Change{Op: changes.OpSet, Config: cfg.Name, Section: sec.Ref(), Option: o.Key, Value: v, Old: o.Value()})
		if err != nil {
			return u, toastErr(err)
		}
		return u, toast("staged; review and apply on the Changes screen (3)")
	}
	var cmd tea.Cmd
	u.input, cmd = u.input.Update(m)
	return u, cmd
}

func (u *uciScreen) stageDelete() (screen, tea.Cmd) {
	if u.level == 0 || u.count() == 0 {
		return u, nil
	}
	if u.e.S.ReadOnly {
		return u, u.readOnlyMsg()
	}
	cfg := u.cfgs[u.cur[0]]
	sec := cfg.Sections[u.cur[1]]
	c := changes.Change{Op: changes.OpDelete, Config: cfg.Name, Section: sec.Ref()}
	if u.level == 2 {
		o := u.options()[u.cur[2]]
		c.Option, c.Old = o.Key, o.Value()
	}
	if err := u.e.Staged.Add(c); err != nil {
		return u, toastErr(err)
	}
	return u, toast("delete staged; review it on the Changes screen (3)")
}

func (u *uciScreen) View(w, h int) string {
	if u.loading && len(u.cfgs) == 0 {
		return "\n  loading configuration..."
	}
	if u.err != nil {
		return "\n  " + sBad.Render("error: "+u.err.Error())
	}
	if len(u.cfgs) == 0 {
		return "\n  no configuration found"
	}
	cfg := u.cfgs[u.cur[0]]
	crumb := " " + sAccent.Render(cfg.Name)
	var rows []string
	switch u.level {
	case 0:
		crumb = " " + sDim.Render("config files")
		for _, c := range u.cfgs {
			mark := ""
			if n := u.pendingIn(c.Name); n > 0 {
				mark = sWarn.Render(fmt.Sprintf("  * %d staged", n))
			}
			rows = append(rows, fmt.Sprintf("%-20s %s", c.Name, sDim.Render(fmt.Sprintf("%d sections", len(c.Sections))))+mark)
		}
	case 1:
		for _, s := range cfg.Sections {
			name := s.Name
			if s.Anonymous || name == "" {
				name = s.Ref()
			}
			rows = append(rows, fmt.Sprintf("%-22s %s", s.Type, sDim.Render(name)))
		}
	case 2:
		sec := cfg.Sections[u.cur[1]]
		crumb += sDim.Render(" / ") + sAccent.Render(sec.Ref()) + sDim.Render(" ("+sec.Type+")")
		for _, o := range sec.Options {
			rows = append(rows, u.optRow(cfg.Name, sec.Ref(), o))
		}
	}
	var b strings.Builder
	b.WriteString(crumb + "\n")
	if u.editing {
		b.WriteString(" " + u.input.View() + "\n")
	}
	lo, hi := window(len(rows), u.cur[u.level], h-3)
	for i := lo; i < hi; i++ {
		line := "  " + rows[i]
		if i == u.cur[u.level] {
			line = sSel.Render(clip(stripPad(line), w))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (u *uciScreen) pendingIn(cfg string) int {
	n := 0
	for _, c := range u.e.Staged.Items() {
		if c.Config == cfg {
			n++
		}
	}
	return n
}

func (u *uciScreen) optRow(cfg, ref string, o uci.Option) string {
	val := o.Display(u.reveal)
	if o.IsList {
		val = "[" + strings.Join(maskList(o, u.reveal), ", ") + "]"
	}
	row := fmt.Sprintf("%-24s %s", o.Key, val)
	if c, ok := u.e.Staged.Pending(cfg, ref, o.Key); ok {
		nv := c.Value
		if c.Op == changes.OpDelete {
			nv = "(deleted)"
		} else if uci.IsSecret(o.Key) && !u.reveal {
			nv = uci.MaskText
		}
		row += sWarn.Render("  -> " + nv)
	}
	return row
}

func maskList(o uci.Option, reveal bool) []string {
	if reveal || !uci.IsSecret(o.Key) {
		return o.Values
	}
	out := make([]string, len(o.Values))
	for i := range out {
		out[i] = uci.MaskText
	}
	return out
}

// stripPad removes styling so the selected row is one solid reverse bar.
func stripPad(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && (r == 'm'):
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Commands lets the palette jump straight to a config file.
func (u *uciScreen) Commands() []paletteItem {
	var out []paletteItem
	for i, c := range u.cfgs {
		i := i
		out = append(out, paletteItem{Title: "Open config: " + c.Name, Hint: "/etc/config/" + c.Name, Run: func() tea.Cmd {
			u.level, u.cur[0], u.cur[1], u.cur[2] = 1, i, 0, 0
			return nil
		}})
	}
	return out
}

// Detail explains the selected row.
func (u *uciScreen) Detail(w, h int) string {
	if len(u.cfgs) == 0 {
		return ""
	}
	cfg := u.cfgs[u.cur[0]]
	var b strings.Builder
	switch u.level {
	case 0:
		b.WriteString(" " + sBold.Render(cfg.Name) + "\n\n")
		b.WriteString(" " + sDim.Render("File     ") + "/etc/config/" + cfg.Name + "\n")
		b.WriteString(" " + sDim.Render("Sections ") + fmt.Sprint(len(cfg.Sections)) + "\n")
		if n := u.pendingIn(cfg.Name); n > 0 {
			b.WriteString(" " + sWarn.Render(fmt.Sprintf("%d staged change(s)", n)) + "\n")
		}
		b.WriteString("\n " + sDim.Render("enter to open") + "\n")
	case 1:
		if u.cur[1] >= len(cfg.Sections) {
			return ""
		}
		sec := cfg.Sections[u.cur[1]]
		b.WriteString(" " + sBold.Render(sec.Ref()) + "\n\n")
		b.WriteString(" " + sDim.Render("Type    ") + sec.Type + "\n")
		b.WriteString(" " + sDim.Render("Options ") + fmt.Sprint(len(sec.Options)) + "\n")
		b.WriteString(" " + sDim.Render("Edit    ") + "uci show " + cfg.Name + "." + sec.Ref() + "\n")
	case 2:
		opts := u.options()
		if u.cur[2] >= len(opts) {
			return ""
		}
		o := opts[u.cur[2]]
		sec := cfg.Sections[u.cur[1]]
		kind := "option"
		if o.IsList {
			kind = "list"
		}
		b.WriteString(" " + sBold.Render(o.Key) + sDim.Render("  ("+kind+")") + "\n\n")
		for _, l := range wrap(o.Display(u.reveal), w-2) {
			b.WriteString(" " + l + "\n")
		}
		if o.IsList {
			for _, v := range maskList(o, u.reveal) {
				b.WriteString("  • " + v + "\n")
			}
		}
		if c, ok := u.e.Staged.Pending(cfg.Name, sec.Ref(), o.Key); ok {
			b.WriteString("\n " + sWarn.Render("staged: "+describe(c)) + "\n")
		}
		if uci.IsSecret(o.Key) {
			b.WriteString("\n " + sDim.Render("secret: masked; v reveals") + "\n")
		}
		b.WriteString("\n " + sDim.Render("uci get "+cfg.Name+"."+sec.Ref()+"."+o.Key) + "\n")
	}
	return b.String()
}
