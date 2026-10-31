package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// ---- panels ----

// panel draws a rounded box with a title in the top border. The result is
// exactly w columns by h rows. right is shown at the right end of the border.
func panel(title, right, body string, w, h int, active bool) string {
	if w < 4 || h < 3 {
		return fitLines(body, w, h)
	}
	bs := sBorder
	ts := sDim.Bold(true)
	if active {
		bs, ts = sBorderOn, sAccent
	}
	inner := w - 2
	t := ""
	if title != "" {
		t = " " + title + " "
	}
	r := ""
	if right != "" {
		r = " " + right + " "
	}
	fill := inner - 1 - lipgloss.Width(t) - lipgloss.Width(r)
	if fill < 0 { // title too long: shorten it
		t = clip(t, inner-1-lipgloss.Width(r))
		fill = inner - 1 - lipgloss.Width(t) - lipgloss.Width(r)
		if fill < 0 {
			r, fill = "", inner-1-lipgloss.Width(t)
		}
	}
	var b strings.Builder
	b.WriteString(bs.Render("╭─") + ts.Render(t) + bs.Render(strings.Repeat("─", max0(fill))) + sDim.Render(r) + bs.Render("╮") + "\n")
	lines := strings.Split(body, "\n")
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		l = padVisible(clip(l, inner), inner)
		b.WriteString(bs.Render("│") + l + bs.Render("│") + "\n")
	}
	b.WriteString(bs.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// ---- charts ----

var blocks = []rune(" ▁▂▃▄▅▆▇█")

// spark renders the last w values as one row of block characters, scaled to
// maxv (or to the largest value when maxv <= 0). Short series are right-aligned.
func spark(vals []float64, w int, maxv float64) string {
	if w <= 0 {
		return ""
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	if maxv <= 0 {
		for _, v := range vals {
			if v > maxv {
				maxv = v
			}
		}
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", w-len(vals)))
	for _, v := range vals {
		b.WriteRune(blocks[level(v, maxv, 8)])
	}
	return b.String()
}

// level maps v in [0,maxv] to 0..steps.
func level(v, maxv float64, steps int) int {
	if maxv <= 0 || v <= 0 {
		return 0
	}
	l := int(v/maxv*float64(steps) + 0.5)
	if l < 1 {
		l = 1 // anything above zero stays visible
	}
	if l > steps {
		l = steps
	}
	return l
}

// graph renders vals as h rows of bars, w columns wide, scaled to maxv. The
// newest value is the rightmost column.
func graph(vals []float64, w, h int, maxv float64, st lipgloss.Style) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	pad := w - len(vals)
	rows := make([]string, h)
	for r := 0; r < h; r++ { // r = 0 is the top row
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", pad))
		for _, v := range vals {
			eighths := level(v, maxv, h*8)
			cell := eighths - (h-1-r)*8 // eighths that fall into this row
			switch {
			case cell >= 8:
				b.WriteRune('█')
			case cell <= 0:
				b.WriteRune(' ')
			default:
				b.WriteRune(blocks[cell])
			}
		}
		rows[r] = st.Render(b.String())
	}
	return strings.Join(rows, "\n")
}

// bar draws a w-cell gauge for pct (0..100), coloured by how full it is.
func bar(pct float64, w int) string {
	if w < 1 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(w) + 0.5)
	st := sGood
	switch {
	case pct >= 90:
		st = sBad
	case pct >= 75:
		st = sWarn
	}
	return st.Render(strings.Repeat("█", filled)) + sDim.Render(strings.Repeat("░", w-filled))
}

// bars draws 1..4 signal bars.
func bars(n int) string {
	glyphs := []string{"▂", "▄", "▆", "█"}
	var b strings.Builder
	for i, g := range glyphs {
		if i < n {
			b.WriteString(sGood.Render(g))
		} else {
			b.WriteString(sDim.Render(g))
		}
	}
	return b.String()
}

// ---- number formats ----

func kib(k int64) string {
	switch {
	case k >= 1024*1024:
		return fmt.Sprintf("%.1f GiB", float64(k)/1024/1024)
	case k >= 1024:
		return fmt.Sprintf("%.1f MiB", float64(k)/1024)
	}
	return fmt.Sprintf("%d KiB", k)
}

func uptime(secs int) string {
	d, h, m := secs/86400, secs/3600%24, secs/60%60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm %ds", m, secs%60)
}

// rate formats bytes per second, as bits per second when bits is true (the way
// internet speeds are quoted).
func rate(bps float64, bits bool) string {
	v, unit := bps, "B/s"
	if bits {
		v, unit = bps*8, "b/s"
	}
	for _, p := range []string{"", "K", "M", "G"} {
		if v < 1000 || p == "G" {
			if p == "" {
				return fmt.Sprintf("%.0f %s", v, unit)
			}
			if v < 10 {
				return fmt.Sprintf("%.2f %s%s", v, p, unit)
			}
			return fmt.Sprintf("%.1f %s%s", v, p, unit)
		}
		v /= 1000
	}
	return ""
}

// ---- footer hints ----

// hints turns "key desc  key desc" into styled key chips, cut to w cells.
func hints(keys string, w int) string {
	return clip(renderHints(hintItems(keys)), w)
}

func hintItems(keys string) []string {
	var parts []string
	for _, item := range strings.Split(keys, "  ") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		k, d, _ := strings.Cut(item, " ")
		parts = append(parts, sKey.Render(k)+" "+sKeyDesc.Render(d))
	}
	return parts
}

func renderHints(parts []string) string { return strings.Join(parts, sDim.Render("  ")) }

// footerHints lays out the screen's key hints beside the global ones. When
// they do not fit, whole hints are dropped from the end of the screen's list
// (never cut mid-word), and "? help" is always kept so the rest stays
// discoverable.
func footerHints(keys string, w int) string {
	left := hintItems(keys)
	full := renderHints(hintItems(": palette  ? help  q quit"))
	short := renderHints(hintItems("? help"))
	for _, right := range []string{full, short} {
		for n := len(left); n >= 0; n-- {
			l := renderHints(left[:n])
			if n < len(left) {
				l += sDim.Render(" …")
			}
			if n == 0 && len(left) > 0 {
				l = ""
			}
			if lipgloss.Width(l)+lipgloss.Width(right)+3 <= w {
				return " " + padVisible(l, w-lipgloss.Width(right)-3) + right + " "
			}
		}
	}
	return clip(" "+short, w)
}
