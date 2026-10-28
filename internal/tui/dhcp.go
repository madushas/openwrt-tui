package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/metrics"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
)

type dhcpKind int

const (
	rowPool   dhcpKind = iota // config dhcp: address pool of one interface
	rowStatic                 // config host: reserved address
	rowNew                    // a reservation that is staged but not applied yet
	rowActive                 // a live lease with no reservation
)

type dhcpRow struct {
	kind  dhcpKind
	sec   uci.Section
	lease metrics.Lease
	name  string // for rowNew, the section name
	opts  map[string]string
}

func (r dhcpRow) get(k string) string {
	if r.kind == rowNew {
		return r.opts[k]
	}
	if r.kind == rowActive {
		switch k {
		case "mac":
			return r.lease.MAC
		case "ip":
			return r.lease.IP
		case "name":
			return r.lease.Host
		}
		return ""
	}
	o, _ := r.sec.Option(k)
	return o.Value()
}

type dhcpScreen struct {
	e       *env
	cfg     uci.Config
	leases  []metrics.Lease
	cur     int
	loading bool
	err     error
	form    *form
	// what the open form edits
	edit dhcpRow
	add  bool
}

func newDHCP(e *env) *dhcpScreen { return &dhcpScreen{e: e, loading: true} }

func (d *dhcpScreen) Title() string   { return "DHCP" }
func (d *dhcpScreen) Capturing() bool { return d.form != nil }
func (d *dhcpScreen) Keys() string {
	if d.form != nil {
		return "enter edit  s stage  esc cancel"
	}
	return "enter edit  n new reservation  s make static  d delete  g reload"
}

type dhcpMsg struct {
	cfg    uci.Config
	leases []metrics.Lease
	err    error
}

func (d *dhcpScreen) Init() tea.Cmd { return d.load() }

func (d *dhcpScreen) load() tea.Cmd {
	return d.e.call(func(ctx context.Context) tea.Msg {
		all, err := d.e.S.LoadUCI(ctx)
		if err != nil {
			return dhcpMsg{err: err}
		}
		var m dhcpMsg
		for _, c := range all {
			if c.Name == "dhcp" {
				m.cfg = c
			}
		}
		// Live leases are a nicety; a failure here must not hide the config.
		if s, err := d.e.S.Sample(ctx); err == nil {
			m.leases = s.Leases
		}
		return m
	})
}

func (d *dhcpScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case dhcpMsg:
		d.loading, d.err = false, m.err
		if m.err == nil {
			d.cfg, d.leases = m.cfg, m.leases
			d.cur = clampInt(d.cur, 0, len(d.rows())-1)
		}
	case uciChangedMsg:
		return d, d.load()
	case tea.KeyPressMsg:
		if d.form != nil {
			return d.formKey(m)
		}
		return d.key(m)
	}
	return d, nil
}

// rows lists pools, then reservations (including staged ones), then live
// leases that have no reservation yet.
func (d *dhcpScreen) rows() []dhcpRow {
	var pools, static, news, active []dhcpRow
	have := map[string]bool{}
	for _, s := range d.cfg.Sections {
		switch s.Type {
		case "dhcp":
			pools = append(pools, dhcpRow{kind: rowPool, sec: s})
		case "host":
			r := dhcpRow{kind: rowStatic, sec: s}
			static = append(static, r)
			have[strings.ToLower(r.get("mac"))] = true
		}
	}
	idx := map[string]int{}
	for _, c := range d.e.Staged.Items() {
		if c.Config != "dhcp" {
			continue
		}
		switch {
		case c.Op == changes.OpAddSect && c.Type == "host":
			idx[c.Section] = len(news)
			news = append(news, dhcpRow{kind: rowNew, name: c.Section, opts: map[string]string{}})
		case c.Op == changes.OpSet:
			if i, ok := idx[c.Section]; ok {
				news[i].opts[c.Option] = c.Value
			}
		}
	}
	for _, r := range news {
		have[strings.ToLower(r.opts["mac"])] = true
	}
	for _, l := range d.leases {
		if !have[strings.ToLower(l.MAC)] {
			active = append(active, dhcpRow{kind: rowActive, lease: l})
		}
	}
	return append(append(append(pools, static...), news...), active...)
}

func (d *dhcpScreen) selected() (dhcpRow, bool) {
	r := d.rows()
	if d.cur < 0 || d.cur >= len(r) {
		return dhcpRow{}, false
	}
	return r[d.cur], true
}

func (d *dhcpScreen) key(m tea.KeyPressMsg) (screen, tea.Cmd) {
	switch m.String() {
	case "up", "k":
		d.cur = clampInt(d.cur-1, 0, len(d.rows())-1)
	case "down", "j":
		d.cur = clampInt(d.cur+1, 0, len(d.rows())-1)
	case "g":
		return d, d.load()
	case "enter", "e":
		r, ok := d.selected()
		if !ok {
			return d, nil
		}
		switch r.kind {
		case rowPool, rowStatic:
			return d.open(r, false)
		case rowActive:
			return d.open(r, true) // an active lease is edited as a new reservation
		case rowNew:
			return d, toast("not applied yet: change or remove it on the Changes screen")
		}
	case "s":
		if r, ok := d.selected(); ok && r.kind == rowActive {
			return d.open(r, true)
		}
	case "n":
		return d.open(dhcpRow{kind: rowActive}, true)
	case "d":
		return d.stageDelete()
	}
	return d, nil
}

func (d *dhcpScreen) guardWrite() tea.Cmd {
	if d.e.S.ReadOnly {
		return toast("read-only mode: start with -write to stage edits")
	}
	return nil
}

// leaseName turns a DHCP hostname into a valid reservation name; the router
// supplies it, so it is never trusted as it is.
func leaseName(h string) string {
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.' || r == ' ':
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func (d *dhcpScreen) open(r dhcpRow, asNew bool) (screen, tea.Cmd) {
	if c := d.guardWrite(); c != nil {
		return d, c
	}
	d.edit, d.add = r, asNew
	if r.kind == rowPool {
		d.form = newForm("DHCP pool: "+r.get("interface"),
			&field{Label: "First address", Key: "start", Value: r.get("start"), Hint: "offset from the network address, e.g. 100", Validate: validate.IntRange(1, 65535)},
			&field{Label: "Pool size", Key: "limit", Value: r.get("limit"), Hint: "how many addresses to hand out, e.g. 150", Validate: validate.IntRange(1, 65535)},
			&field{Label: "Lease time", Key: "leasetime", Value: r.get("leasetime"), Hint: "e.g. 12h, 30m, 7d, or infinite", Validate: validate.Duration},
			&field{Label: "DHCP off", Key: "ignore", Kind: kBool, Value: r.get("ignore"), Hint: "stop answering DHCP on this interface"},
		)
		return d, nil
	}
	taken := func(key, val string) bool {
		for _, o := range d.rows() {
			if (o.kind == rowStatic || o.kind == rowNew) && !(!asNew && o.sec.Ref() == r.sec.Ref() && o.kind == rowStatic) &&
				strings.EqualFold(o.get(key), val) {
				return true
			}
		}
		return false
	}
	name := r.get("name")
	if asNew {
		name = leaseName(name)
	}
	title := "Static lease"
	if asNew {
		title = "New static lease"
	}
	d.form = newForm(title,
		&field{Label: "Name", Key: "name", Value: name, Required: true, Hint: "a label for this device", Validate: validate.Hostname},
		&field{Label: "MAC address", Key: "mac", Value: r.get("mac"), Required: true, Hint: "aa:bb:cc:dd:ee:ff",
			Validate: func(s string) error {
				if err := validate.MAC(s); err != nil {
					return err
				}
				if taken("mac", s) {
					return fmt.Errorf("already has a reservation")
				}
				return nil
			}},
		&field{Label: "IP address", Key: "ip", Value: r.get("ip"), Required: true, Hint: "must be on the LAN subnet, outside the dynamic pool",
			Validate: func(s string) error {
				if err := validate.IPv4(s); err != nil {
					return err
				}
				if taken("ip", s) {
					return fmt.Errorf("already reserved for another device")
				}
				return nil
			}},
	)
	if asNew { // everything counts as a change for a new section
		for _, f := range d.form.fields {
			f.Orig = ""
		}
	}
	return d, nil
}

func (d *dhcpScreen) formKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	cmd := d.form.Update(m)
	switch {
	case d.form.cancelled:
		d.form = nil
	case d.form.done:
		f := d.form
		d.form = nil
		return d, d.stage(f)
	}
	return d, cmd
}

func (d *dhcpScreen) stage(f *form) tea.Cmd {
	if !f.dirty() {
		return toast("nothing changed")
	}
	var list []changes.Change
	sec := d.edit.sec.Ref()
	if d.add {
		sec = "lease_" + strings.ReplaceAll(strings.ToLower(f.fields[1].Value), ":", "")
		list = append(list, changes.Change{Op: changes.OpAddSect, Config: "dhcp", Section: sec, Type: "host"})
		for _, fl := range f.fields {
			fl.Value = func(s string) string {
				if fl.Key == "mac" {
					return strings.ToLower(s)
				}
				return s
			}(fl.Value)
		}
	}
	list = append(list, f.Changes("dhcp", sec)...)
	for _, c := range list {
		if err := d.e.Staged.Add(c); err != nil {
			return toastErr(err)
		}
	}
	return toast("staged; review and apply on the Changes screen (c)")
}

func (d *dhcpScreen) stageDelete() (screen, tea.Cmd) {
	r, ok := d.selected()
	if !ok || r.kind != rowStatic {
		return d, toast("only static leases can be deleted here")
	}
	if c := d.guardWrite(); c != nil {
		return d, c
	}
	c := changes.Change{Op: changes.OpDelete, Config: "dhcp", Section: r.sec.Ref(),
		Old: fmt.Sprintf("static lease %s %s %s", r.get("name"), r.get("mac"), r.get("ip"))}
	if err := d.e.Staged.Add(c); err != nil {
		return d, toastErr(err)
	}
	return d, toast("delete staged; review it on the Changes screen (c)")
}

func (d *dhcpScreen) View(w, h int) string {
	if d.form != nil {
		return d.form.View(w, h)
	}
	if d.loading && len(d.cfg.Sections) == 0 {
		return "\n  loading DHCP settings..."
	}
	if d.err != nil {
		return "\n  " + sBad.Render("error: "+d.err.Error())
	}
	rows := d.rows()
	if len(rows) == 0 {
		return "\n  No DHCP pools or leases found. Press n to add a static lease."
	}
	var b strings.Builder
	b.WriteString(sDim.Render(fmt.Sprintf("  %-7s %-20s %-17s %-15s %s", "", "NAME", "MAC", "ADDRESS", "DETAIL")) + "\n")
	lo, hi := window(len(rows), d.cur, h-1)
	for i := lo; i < hi; i++ {
		r := rows[i]
		tag, name, mac, ip, det := "", "", "", "", ""
		switch r.kind {
		case rowPool:
			tag, name = "POOL", r.get("interface")
			ip = "start " + r.get("start")
			det = fmt.Sprintf("%s addresses, lease %s", orDash(r.get("limit")), orDash(r.get("leasetime")))
			if r.get("ignore") == "1" {
				det = "DHCP off"
			}
		case rowStatic, rowNew:
			tag, name, mac, ip = "STATIC", r.get("name"), r.get("mac"), r.get("ip")
			if r.kind == rowNew {
				name = r.opts["name"]
				det = "staged"
			}
		case rowActive:
			tag, name, mac, ip = "ACTIVE", r.lease.Host, r.lease.MAC, r.lease.IP
			if name == "" || name == "*" {
				name = "(unnamed)"
			}
			det = "s: make static"
		}
		if _, p := d.e.Staged.Pending("dhcp", r.sec.Ref(), ""); p && r.kind != rowActive && r.kind != rowNew {
			det = "change staged"
		}
		line := fmt.Sprintf("  %-7s %-20s %-17s %-15s %s", tag, name, mac, ip, det)
		if i == d.cur {
			b.WriteString(sSel.Render(clip(line, w)) + "\n")
			continue
		}
		b.WriteString(strings.Replace(line, tag, tagStyle(tag), 1) + "\n")
	}
	return b.String()
}

func tagStyle(tag string) string {
	switch tag {
	case "POOL":
		return sAccent.Render(tag)
	case "STATIC":
		return sGood.Render(tag)
	}
	return sDim.Render(tag)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
