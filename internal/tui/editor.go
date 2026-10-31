package tui

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// secEditor edits the sections of one kind (zones, rules, routes, bridges...)
// in one config. It lists them, and creates, edits, switches off and deletes
// them as staged changes; the preflight rules judge the result.
type secEditor struct {
	e    *env
	spec secSpec

	all     []uci.Config // every config, for choices and cross-checks
	live    []string
	cur     int
	loading bool
	err     error
	form    *form
	edit    *uci.Section // nil while creating
}

type col struct {
	Head string
	W    int
	Get  func(s uci.Section) string
}

// secSpec describes one kind of section.
type secSpec struct {
	ID, Title string
	Cfg       string
	Types     []string
	NewType   string
	Cols      []col
	// Fields builds the form; s is nil when creating.
	Fields   func(ed *secEditor, s *uci.Section) []*field
	ListKeys []string
	// NewName picks the section name for a created section.
	NewName func(ed *secEditor, f *form) string
	// Toggle: the option that switches a section off and the value meaning off.
	ToggleOpt, ToggleOff string
	// Ordered: the order of sections matters (firewall rules), so K and J move
	// the selected one up and down.
	Ordered bool
	// Delete may refuse or add clean-up; nil means "delete the section".
	Delete func(ed *secEditor, s uci.Section) ([]changes.Change, []string, error)
	// CanEdit may refuse editing a section (read-only kinds of device).
	CanEdit func(s uci.Section) string
	// Live returns extra lines read from the router (e.g. the routing table).
	Live func(ctx context.Context, e *env) []string
	// Empty is shown above an empty list.
	Empty string
	// Pre may refuse to create a section (returns the reason).
	Pre func(ed *secEditor) string
	// Cross validates the form as a whole.
	Cross func(ed *secEditor, f *form)
}

type edLoadedMsg struct {
	id   string
	all  []uci.Config
	live []string
	err  error
}

func newEditor(e *env, spec secSpec) *secEditor { return &secEditor{e: e, spec: spec, loading: true} }

func (d *secEditor) Title() string   { return d.spec.Title }
func (d *secEditor) Capturing() bool { return d.form != nil }
func (d *secEditor) Keys() string {
	if d.form != nil {
		return "enter edit  s stage  esc cancel"
	}
	k := "enter edit  n new  d delete"
	if d.spec.ToggleOpt != "" {
		k += "  t switch off/on"
	}
	if d.spec.Ordered {
		k += "  K/J move up/down"
	}
	return k + "  g reload"
}

func (d *secEditor) Init() tea.Cmd { return d.load() }

func (d *secEditor) load() tea.Cmd {
	return d.e.call(func(ctx context.Context) tea.Msg {
		all, err := d.e.loadUCI(ctx)
		m := edLoadedMsg{id: d.spec.ID, all: all, err: err}
		if err == nil && d.spec.Live != nil {
			m.live = d.spec.Live(ctx, d.e)
		}
		return m
	})
}

func (d *secEditor) config() uci.Config {
	for _, c := range d.all {
		if c.Name == d.spec.Cfg {
			return c
		}
	}
	return uci.Config{}
}

func (d *secEditor) rows() []uci.Section {
	var out []uci.Section
	for _, s := range d.config().Sections {
		for _, t := range d.spec.Types {
			if s.Type == t {
				out = append(out, s)
			}
		}
	}
	return out
}

// names of sections' option values in other configs, for choices.
func (d *secEditor) zones() []string {
	var out []string
	for _, c := range d.all {
		if c.Name == "firewall" {
			for _, s := range c.Sections {
				if s.Type == "zone" {
					out = append(out, opt(s, "name"))
				}
			}
		}
	}
	return out
}

func (d *secEditor) ifaces() []string {
	var out []string
	for _, c := range d.all {
		if c.Name == "network" {
			for _, s := range c.Sections {
				if s.Type == "interface" && s.Name != "loopback" {
					out = append(out, s.Name)
				}
			}
		}
	}
	return out
}

func (d *secEditor) bridges() []string {
	var out []string
	for _, c := range d.all {
		if c.Name == "network" {
			for _, s := range c.Sections {
				if s.Type == "device" && opt(s, "type") == "bridge" {
					out = append(out, opt(s, "name"))
				}
			}
		}
	}
	return out
}

func (d *secEditor) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case edLoadedMsg:
		if m.id != d.spec.ID {
			return d, nil
		}
		d.loading, d.err = false, m.err
		if m.err == nil {
			d.all, d.live = m.all, m.live
			d.cur = clampInt(d.cur, 0, len(d.rows())-1)
		}
	case uciChangedMsg:
		return d, d.load()
	case tea.KeyPressMsg:
		if d.form != nil {
			return d.formKey(m)
		}
		return d.key(m.String())
	}
	return d, nil
}

func (d *secEditor) guardWrite() tea.Cmd {
	if d.e.S.ReadOnly {
		return toast("read-only mode: start with -write to stage edits")
	}
	return nil
}

func (d *secEditor) selected() (uci.Section, bool) {
	rows := d.rows()
	if d.cur < 0 || d.cur >= len(rows) {
		return uci.Section{}, false
	}
	return rows[d.cur], true
}

func (d *secEditor) key(k string) (screen, tea.Cmd) {
	rows := d.rows()
	switch k {
	case "up", "k":
		d.cur = clampInt(d.cur-1, 0, len(rows)-1)
	case "down", "j":
		d.cur = clampInt(d.cur+1, 0, len(rows)-1)
	case "g":
		d.loading = true
		d.e.invalidate()
		return d, d.load()
	case "enter", "e":
		s, ok := d.selected()
		if !ok {
			return d, nil
		}
		if c := d.guardWrite(); c != nil {
			return d, c
		}
		if d.spec.CanEdit != nil {
			if why := d.spec.CanEdit(s); why != "" {
				return d, toast(why)
			}
		}
		d.edit = &s
		d.form = newForm("Edit "+d.spec.Title+" "+d.describe(s), d.spec.Fields(d, &s)...)
		d.hookCross()
	case "n":
		if c := d.guardWrite(); c != nil {
			return d, c
		}
		if d.spec.Pre != nil {
			if why := d.spec.Pre(d); why != "" {
				return d, toast(why)
			}
		}
		d.edit = nil
		d.form = newForm("New "+strings.ToLower(strings.TrimSuffix(d.spec.Title, "s")), d.spec.Fields(d, nil)...)
		d.hookCross()
	case "d":
		s, ok := d.selected()
		if !ok {
			return d, nil
		}
		if c := d.guardWrite(); c != nil {
			return d, c
		}
		return d, d.askDelete(s)
	case "K", "J":
		if !d.spec.Ordered {
			return d, nil
		}
		return d, d.move(k == "K")
	case "t":
		s, ok := d.selected()
		if !ok || d.spec.ToggleOpt == "" {
			return d, nil
		}
		if c := d.guardWrite(); c != nil {
			return d, c
		}
		var ch changes.Change
		if opt(s, d.spec.ToggleOpt) == d.spec.ToggleOff {
			ch = changes.Change{Op: changes.OpDelete, Config: d.spec.Cfg, Section: s.Ref(), Option: d.spec.ToggleOpt, Old: d.spec.ToggleOff}
		} else {
			ch = changes.Change{Op: changes.OpSet, Config: d.spec.Cfg, Section: s.Ref(), Option: d.spec.ToggleOpt, Value: d.spec.ToggleOff, Old: opt(s, d.spec.ToggleOpt)}
		}
		return d, d.stage([]changes.Change{ch}, nil)
	}
	return d, nil
}

// move stages moving the selected section above its previous sibling (up) or
// below its next one. uci positions count every section in the file, so the
// target is the neighbour's absolute position.
func (d *secEditor) move(up bool) tea.Cmd {
	if c := d.guardWrite(); c != nil {
		return c
	}
	rows := d.rows()
	if len(rows) < 2 {
		return toast("nothing to reorder")
	}
	j := d.cur - 1
	if !up {
		j = d.cur + 1
	}
	if j < 0 || j >= len(rows) {
		return toast("already at the " + map[bool]string{true: "top", false: "bottom"}[up])
	}
	abs := func(s uci.Section) int {
		for i, x := range d.config().Sections {
			if x.Ref() == s.Ref() {
				return i
			}
		}
		return -1
	}
	from, to := rows[d.cur], rows[j]
	pos := abs(to)
	if pos < 0 || abs(from) < 0 {
		return toast("cannot find the section in the configuration")
	}
	ch := changes.Change{Op: changes.OpReorder, Config: d.spec.Cfg, Section: from.Ref(), Value: strconv.Itoa(pos)}
	if err := d.e.Staged.Add(ch); err != nil {
		return toastErr(err)
	}
	return toast("move staged; review and apply on the Changes screen (apply it before moving again)")
}

func (d *secEditor) hookCross() {
	if d.spec.Cross != nil {
		d.form.Cross = func(f *form) { d.spec.Cross(d, f) }
	}
}

func (d *secEditor) describe(s uci.Section) string {
	if n := opt(s, "name"); n != "" {
		return n
	}
	return s.Ref()
}

func (d *secEditor) askDelete(s uci.Section) tea.Cmd {
	del := d.spec.Delete
	if del == nil {
		del = func(ed *secEditor, s uci.Section) ([]changes.Change, []string, error) {
			return []changes.Change{{Op: changes.OpDelete, Config: ed.spec.Cfg, Section: s.Ref(), Old: ed.spec.Title + " " + ed.describe(s)}}, nil, nil
		}
	}
	ch, warn, err := del(d, s)
	if err != nil {
		return toastErr(err)
	}
	prompt := fmt.Sprintf("Delete %s %s?", strings.ToLower(strings.TrimSuffix(d.spec.Title, "s")), d.describe(s))
	if len(ch) > 1 {
		prompt += fmt.Sprintf(" (%d related changes)", len(ch))
	}
	return func() tea.Msg {
		return askMsg{Prompt: prompt, Danger: true, Yes: func() tea.Cmd { return d.stage(ch, warn) }}
	}
}

func (d *secEditor) stage(list []changes.Change, warn []string) tea.Cmd {
	for _, c := range list {
		if err := d.e.Staged.Add(c); err != nil {
			return toastErr(err)
		}
	}
	msg := "staged; review and apply on the Changes screen (c)"
	if len(warn) > 0 {
		msg += ". Note: " + strings.Join(warn, "; ")
	}
	return toast(msg)
}

// labels shown in choice fields for values that are empty or special in UCI.
var labelRaw = map[string]string{
	"(router)": "", "(any zone)": "*", "(any family)": "", "(default)": "", "(none)": "",
}

func rawOf(v string) string {
	if r, ok := labelRaw[v]; ok {
		return r
	}
	return v
}

// unlabel converts choice labels back to UCI values before changes are made.
func unlabel(f *form) {
	for _, fl := range f.fields {
		if fl.Kind == kChoice {
			fl.Value, fl.Orig = rawOf(fl.Value), rawOf(fl.Orig)
		}
	}
}

// label is the inverse for loading: the value to show for a stored one.
func label(raw, emptyLabel string, star string) string {
	switch {
	case raw == "":
		return emptyLabel
	case raw == "*" && star != "":
		return star
	}
	return raw
}

func (d *secEditor) formKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	cmd := d.form.Update(m)
	switch {
	case d.form.cancelled:
		d.form = nil
	case d.form.done:
		f := d.form
		d.form = nil
		return d, d.stageForm(f)
	}
	return d, cmd
}

func (d *secEditor) stageForm(f *form) tea.Cmd {
	if !f.dirty() {
		return toast("nothing changed")
	}
	unlabel(f)
	var list []changes.Change
	if d.edit == nil {
		name := d.spec.NewName(d, f)
		for _, fl := range f.fields { // a new section gets every field that has a value, defaults included
			fl.Orig = ""
		}
		list = append(list, changes.Change{Op: changes.OpAddSect, Config: d.spec.Cfg, Section: name, Type: d.spec.NewType})
		list = append(list, listify(f.Changes(d.spec.Cfg, name), d.spec.ListKeys...)...)
	} else {
		list = listify(f.Changes(d.spec.Cfg, d.edit.Ref()), d.spec.ListKeys...)
	}
	return d.stage(list, nil)
}

var nonName = regexp.MustCompile(`[^a-z0-9_]+`)

// uniqueName makes a section name like "rule_allow_ssh" that does not exist yet.
func (d *secEditor) uniqueName(prefix, hint string) string {
	base := prefix
	if h := strings.Trim(nonName.ReplaceAllString(strings.ToLower(hint), "_"), "_"); h != "" {
		if len(h) > 24 {
			h = h[:24]
		}
		base += "_" + h
	}
	taken := map[string]bool{}
	for _, s := range d.config().Sections {
		taken[s.Name] = true
	}
	for _, c := range d.e.Staged.Items() {
		if c.Op == changes.OpAddSect && c.Config == d.spec.Cfg {
			taken[c.Section] = true
		}
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s_%d", base, i)
	}
	return name
}

func (d *secEditor) View(w, h int) string {
	if d.form != nil {
		return d.form.View(w, h)
	}
	if d.loading && len(d.all) == 0 {
		return "\n  loading..."
	}
	if d.err != nil {
		return "\n  " + sBad.Render("error: "+d.err.Error())
	}
	rows := d.rows()
	var b strings.Builder
	if len(rows) == 0 {
		empty := d.spec.Empty
		if empty == "" {
			empty = "Nothing here yet."
		}
		b.WriteString("\n  " + sDim.Render(empty+" Press n to add one.") + "\n")
	} else {
		var hd strings.Builder
		hd.WriteString("  ")
		for _, c := range d.spec.Cols {
			hd.WriteString(fmt.Sprintf("%-*s ", c.W, c.Head))
		}
		b.WriteString(sDim.Render(clip(hd.String(), w)) + "\n")
		reserve := 1
		if len(d.live) > 0 {
			reserve += len(d.live) + 2
			if reserve > h/2 {
				reserve = h / 2
			}
		}
		lo, hi := window(len(rows), d.cur, h-reserve)
		for i := lo; i < hi; i++ {
			s := rows[i]
			var ln strings.Builder
			ln.WriteString("  ")
			for _, c := range d.spec.Cols {
				ln.WriteString(fmt.Sprintf("%-*s ", c.W, clip(c.Get(s), c.W)))
			}
			line := ln.String()
			if d.spec.ToggleOpt != "" && opt(s, d.spec.ToggleOpt) == d.spec.ToggleOff {
				line += " (off)"
			}
			if _, p := d.e.Staged.Pending(d.spec.Cfg, s.Ref(), ""); p {
				line += "  (change staged)"
			}
			if i == d.cur {
				b.WriteString(sSel.Render(clip(line, w)) + "\n")
			} else {
				b.WriteString(clip(line, w) + "\n")
			}
		}
	}
	if len(d.live) > 0 {
		b.WriteString("\n" + sDim.Render("  live on the router:") + "\n")
		room := h/2 - 2
		for i, l := range d.live {
			if i >= room {
				break
			}
			b.WriteString("  " + sDim.Render(clip(l, w-4)) + "\n")
		}
	}
	return b.String()
}
