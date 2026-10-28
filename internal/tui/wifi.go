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
	"github.com/madushakv/owrt-tui/internal/validate"
	"github.com/madushakv/owrt-tui/internal/wificaps"
)

type wifiScreen struct {
	e       *env
	cfg     uci.Config
	cur     int
	loading bool
	err     error
	form    *form
	edit    uci.Section
	caps    wificaps.Caps // what the radios can do; empty when the router has no iw
}

func newWifi(e *env) *wifiScreen { return &wifiScreen{e: e, loading: true} }

func (w *wifiScreen) Title() string   { return "Wi-Fi" }
func (w *wifiScreen) Capturing() bool { return w.form != nil }
func (w *wifiScreen) Keys() string {
	if w.form != nil {
		return "enter edit  s stage  esc cancel"
	}
	return "enter edit  t enable/disable  g reload"
}

type wifiMsg struct {
	cfg  uci.Config
	caps wificaps.Caps
	err  error
}

func (w *wifiScreen) Init() tea.Cmd { return w.load() }

func (w *wifiScreen) load() tea.Cmd {
	return w.e.call(func(ctx context.Context) tea.Msg {
		all, err := w.e.S.LoadUCI(ctx)
		if err != nil {
			return wifiMsg{err: err}
		}
		// Capabilities are a nicety: without them the forms fall back to generic checks.
		caps, _ := w.e.S.WifiCaps(ctx)
		for _, c := range all {
			if c.Name == "wireless" {
				return wifiMsg{cfg: c, caps: caps}
			}
		}
		return wifiMsg{caps: caps} // no wireless config: a router without Wi-Fi
	})
}

func (w *wifiScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case wifiMsg:
		w.loading, w.err = false, m.err
		if m.err == nil {
			w.cfg, w.caps = m.cfg, m.caps
			w.cur = clampInt(w.cur, 0, len(w.rows())-1)
		}
	case uciChangedMsg:
		return w, w.load()
	case tea.KeyPressMsg:
		if w.form != nil {
			return w.formKey(m)
		}
		return w.key(m)
	}
	return w, nil
}

// rows lists each radio followed by the networks that run on it.
func (w *wifiScreen) rows() []uci.Section {
	var out []uci.Section
	for _, d := range w.cfg.Sections {
		if d.Type != "wifi-device" {
			continue
		}
		out = append(out, d)
		for _, i := range w.cfg.Sections {
			if i.Type == "wifi-iface" && opt(i, "device") == d.Name {
				out = append(out, i)
			}
		}
	}
	for _, i := range w.cfg.Sections { // networks whose radio is missing
		if i.Type != "wifi-iface" {
			continue
		}
		found := false
		for _, d := range w.cfg.Sections {
			if d.Type == "wifi-device" && d.Name == opt(i, "device") {
				found = true
			}
		}
		if !found {
			out = append(out, i)
		}
	}
	return out
}

func opt(s uci.Section, k string) string {
	o, _ := s.Option(k)
	return o.Value()
}

func (w *wifiScreen) selected() (uci.Section, bool) {
	r := w.rows()
	if w.cur < 0 || w.cur >= len(r) {
		return uci.Section{}, false
	}
	return r[w.cur], true
}

func (w *wifiScreen) guardWrite() tea.Cmd {
	if w.e.S.ReadOnly {
		return toast("read-only mode: start with -write to stage edits")
	}
	return nil
}

func (w *wifiScreen) key(m tea.KeyPressMsg) (screen, tea.Cmd) {
	switch m.String() {
	case "up", "k":
		w.cur = clampInt(w.cur-1, 0, len(w.rows())-1)
	case "down", "j":
		w.cur = clampInt(w.cur+1, 0, len(w.rows())-1)
	case "g":
		return w, w.load()
	case "enter", "e":
		if s, ok := w.selected(); ok {
			return w.open(s)
		}
	case "t":
		return w.toggle()
	}
	return w, nil
}

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

func choices(cur string, base ...string) []string {
	for _, b := range base {
		if b == cur || cur == "" {
			return base
		}
	}
	return append(append([]string{}, base...), cur) // keep an unusual value the router already has
}

var encChoices = []string{"none", "psk2", "psk-mixed", "sae", "sae-mixed"}

func (w *wifiScreen) open(s uci.Section) (screen, tea.Cmd) {
	if c := w.guardWrite(); c != nil {
		return w, c
	}
	w.edit = s
	if s.Type == "wifi-device" {
		return w.openRadio(s)
	}
	_, hasKey := s.Option("key")
	f := newForm("Network "+opt(s, "ssid"),
		&field{Label: "Name (SSID)", Key: "ssid", Value: opt(s, "ssid"), Required: true, Validate: validate.SSID, Hint: "changing it disconnects every client until they rejoin"},
		&field{Label: "Security", Key: "encryption", Kind: kChoice, Value: opt(s, "encryption"), Choices: choices(opt(s, "encryption"), encChoices...), Hint: "psk2 = WPA2, sae = WPA3, mixed = both"},
		&field{Label: "Password", Key: "key", Secret: true, Validate: validate.WPAKey, Hint: "8 to 63 characters; leave empty to keep the current one"},
		&field{Label: "Hidden", Key: "hidden", Kind: kBool, Value: opt(s, "hidden"), Hint: "do not broadcast the name"},
		&field{Label: "Isolate clients", Key: "isolate", Kind: kBool, Value: opt(s, "isolate"), Hint: "clients cannot talk to each other"},
		&field{Label: "Off", Key: "disabled", Kind: kBool, Value: opt(s, "disabled")},
	)
	f.Cross = func(f *form) {
		enc, key := f.fields[1].Value, f.fields[2]
		if key.err == "" && enc != "none" && enc != "" && !hasKey && key.Value == "" {
			key.err = "required: this network has no password yet"
		}
	}
	w.form = f
	return w, nil
}

func (w *wifiScreen) formKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	cmd := w.form.Update(m)
	switch {
	case w.form.cancelled:
		w.form = nil
	case w.form.done:
		f := w.form
		w.form = nil
		return w, w.stage(f)
	}
	return w, cmd
}

func (w *wifiScreen) stage(f *form) tea.Cmd {
	list := f.Changes("wireless", w.edit.Ref())
	if len(list) == 0 {
		return toast("nothing changed")
	}
	for _, c := range list {
		if err := w.e.Staged.Add(c); err != nil {
			return toastErr(err)
		}
	}
	return toast("staged; review and apply on the Changes screen (c)")
}

func (w *wifiScreen) toggle() (screen, tea.Cmd) {
	s, ok := w.selected()
	if !ok {
		return w, nil
	}
	if c := w.guardWrite(); c != nil {
		return w, c
	}
	c := changes.Change{Config: "wireless", Section: s.Ref(), Option: "disabled"}
	if opt(s, "disabled") == "1" {
		c.Op, c.Old = changes.OpDelete, "1"
	} else {
		c.Op, c.Value = changes.OpSet, "1"
	}
	if err := w.e.Staged.Add(c); err != nil {
		return w, toastErr(err)
	}
	return w, toast("staged; review and apply on the Changes screen (c)")
}

func (w *wifiScreen) View(width, h int) string {
	if w.form != nil {
		return w.form.View(width, h)
	}
	if w.loading && len(w.cfg.Sections) == 0 {
		return "\n  loading Wi-Fi settings..."
	}
	if w.err != nil {
		return "\n  " + sBad.Render("error: "+w.err.Error())
	}
	rows := w.rows()
	if len(rows) == 0 {
		return "\n  No Wi-Fi radios found in /etc/config/wireless."
	}
	var b strings.Builder
	lo, hi := window(len(rows), w.cur, h)
	for i := lo; i < hi; i++ {
		s := rows[i]
		var line string
		off := opt(s, "disabled") == "1"
		state := "on"
		if off {
			state = "OFF"
		}
		if s.Type == "wifi-device" {
			line = fmt.Sprintf("  %-6s %-9s %-4s ch %-5s %-6s %s", "RADIO", s.Name, opt(s, "band"), orDash(opt(s, "channel")), orDash(opt(s, "htmode")), state)
		} else {
			line = fmt.Sprintf("    %-4s %-28s %-10s on %-8s %s", "SSID", opt(s, "ssid"), orDash(opt(s, "encryption")), orDash(opt(s, "network")), state)
		}
		if _, p := w.e.Staged.Pending("wireless", s.Ref(), ""); p {
			line += "  (change staged)"
		}
		if i == w.cur {
			b.WriteString(sSel.Render(clip(line, width)) + "\n")
		} else if off {
			b.WriteString(sDim.Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// openRadio builds the radio form from what the hardware reports.
func (w *wifiScreen) openRadio(s uci.Section) (screen, tea.Cmd) {
	var band wificaps.Band
	known := false
	if phy, ok := w.caps.For(s.Name, opt(s, "path")); ok {
		band, known = phy.Band(opt(s, "band"))
	}
	ht := []string{"HT20", "HT40"}
	if opt(s, "band") == "5g" || opt(s, "band") == "6g" {
		ht = []string{"VHT20", "VHT40", "VHT80"}
	}
	chHint := "a channel number, or auto"
	if known {
		ht = band.HTModes()
		step := 1
		if band.Name != "2g" {
			step = 4
		}
		chHint = "auto, or " + compactChannels(band.Allowed(), step)
		if nc := band.NeedCountry(); len(nc) > 0 {
			chHint += "; " + compactChannels(nc, step) + " need a country code"
		}
	}
	ctryHint := "two letters, e.g. LK; sets the legal channels and power"
	if c := w.caps.Country; c == "00" {
		ctryHint = "not set: the router uses the restrictive world region. " + ctryHint
	} else if c != "" {
		ctryHint = "in force now: " + c + ". " + ctryHint
	}
	w.form = newForm("Radio "+s.Name+" ("+opt(s, "band")+")",
		&field{Label: "Channel", Key: "channel", Value: opt(s, "channel"), Required: true, Hint: chHint,
			Validate: func(v string) error { return w.checkChannel(s, band, known, v) }},
		&field{Label: "Width / mode", Key: "htmode", Kind: kChoice, Value: opt(s, "htmode"), Choices: choices(opt(s, "htmode"), ht...), Hint: "wider is faster but needs a quiet band"},
		&field{Label: "Country", Key: "country", Value: opt(s, "country"), Hint: ctryHint,
			Validate: func(v string) error {
				if !countryRe.MatchString(v) {
					return fmt.Errorf("two capital letters, e.g. LK")
				}
				return nil
			}},
		&field{Label: "Radio off", Key: "disabled", Kind: kBool, Value: opt(s, "disabled"), Hint: "turns off every network on this radio"},
	)
	return w, nil
}

func (w *wifiScreen) checkChannel(s uci.Section, band wificaps.Band, known bool, v string) error {
	if v == "auto" {
		return nil
	}
	if err := validate.Channel(v); err != nil {
		return err
	}
	n, _ := strconv.Atoi(v)
	if !known { // no capability data: generic limits per band
		switch b := opt(s, "band"); {
		case b == "2g" && n > 14:
			return fmt.Errorf("2.4 GHz channels are 1 to 14")
		case b == "5g" && (n < 32 || n > 177):
			return fmt.Errorf("5 GHz channels are 32 to 177")
		}
		return nil
	}
	c, ok := band.Channel(n)
	switch {
	case !ok:
		return fmt.Errorf("this radio has no channel %d", n)
	case c.Disabled:
		return fmt.Errorf("channel %d is disabled in this region", n)
	case c.NeedsCountry():
		return fmt.Errorf("channel %d is locked until a country is set (apply the country first)", n)
	}
	return nil
}

// compactChannels writes channel numbers as ranges: "36-48, 100-144".
func compactChannels(nums []int, step int) string {
	if len(nums) == 0 {
		return "none"
	}
	var parts []string
	start, prev := nums[0], nums[0]
	flush := func() {
		if start == prev {
			parts = append(parts, strconv.Itoa(start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
		}
	}
	for _, n := range nums[1:] {
		if n-prev > step {
			flush()
			start = n
		}
		prev = n
	}
	flush()
	return strings.Join(parts, ", ")
}
