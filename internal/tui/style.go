package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	cDim    = lipgloss.Color("8")
	cAccent = lipgloss.Color("6")
	cWarn   = lipgloss.Color("3")
	cBad    = lipgloss.Color("1")
	cGood   = lipgloss.Color("2")

	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sBold   = lipgloss.NewStyle().Bold(true)
	sAccent = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sWarn   = lipgloss.NewStyle().Foreground(cWarn)
	sBad    = lipgloss.NewStyle().Foreground(cBad)
	sGood   = lipgloss.NewStyle().Foreground(cGood)
	sSel    = lipgloss.NewStyle().Reverse(true)
	sTab    = lipgloss.NewStyle().Padding(0, 1)
	sTabOn  = lipgloss.NewStyle().Padding(0, 1).Bold(true).Reverse(true)
)

// clip cuts s to at most w display cells.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// fitLines returns exactly h lines, each clipped to w, so a screen never
// pushes the footer off the terminal.
func fitLines(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = clip(lines[i], w)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// window picks the slice of n rows to show so that cursor stays visible.
func window(n, cursor, h int) (start, end int) {
	if h <= 0 || n <= h {
		return 0, n
	}
	start = cursor - h/2
	if start < 0 {
		start = 0
	}
	if start+h > n {
		start = n - h
	}
	return start, start + h
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// padVisible pads a styled string to w display cells.
func padVisible(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
