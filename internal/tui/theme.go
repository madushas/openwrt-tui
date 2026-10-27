package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/madushakv/owrt-tui/internal/discover"
)

// Theme is a colour palette. The status colours are never the only signal:
// the UI also uses symbols (● ○ ▲ ▼) and words.
type Theme struct {
	Name                               string
	Text, Dim, Accent, Good, Warn, Bad color.Color
	Border, BorderOn, SelBg, Rx, Tx    color.Color
	ChipFg                             color.Color // text on coloured chips
}

var themes = []Theme{
	{
		Name: "dark", Text: lipgloss.Color("#c0caf5"), Dim: lipgloss.Color("#6b7394"), Accent: lipgloss.Color("#7aa2f7"),
		Good: lipgloss.Color("#9ece6a"), Warn: lipgloss.Color("#e0af68"), Bad: lipgloss.Color("#f7768e"),
		Border: lipgloss.Color("#3b4261"), BorderOn: lipgloss.Color("#7aa2f7"), SelBg: lipgloss.Color("#283457"),
		Rx: lipgloss.Color("#7dcfff"), Tx: lipgloss.Color("#bb9af7"), ChipFg: lipgloss.Color("#1a1b26"),
	},
	{
		Name: "light", Text: lipgloss.Color("#1f2335"), Dim: lipgloss.Color("#7a8099"), Accent: lipgloss.Color("#2e5cb8"),
		Good: lipgloss.Color("#2a7d3f"), Warn: lipgloss.Color("#a65e00"), Bad: lipgloss.Color("#c0392b"),
		Border: lipgloss.Color("#b4b8c8"), BorderOn: lipgloss.Color("#2e5cb8"), SelBg: lipgloss.Color("#d5def5"),
		Rx: lipgloss.Color("#0f6f8f"), Tx: lipgloss.Color("#7b3fb5"), ChipFg: lipgloss.Color("#ffffff"),
	},
	{ // Okabe-Ito palette: distinguishable with the common colour-vision deficiencies
		Name: "colour-blind", Text: lipgloss.Color("#e8e8e8"), Dim: lipgloss.Color("#8a8a8a"), Accent: lipgloss.Color("#56b4e9"),
		Good: lipgloss.Color("#009e73"), Warn: lipgloss.Color("#f0e442"), Bad: lipgloss.Color("#d55e00"),
		Border: lipgloss.Color("#4a4a4a"), BorderOn: lipgloss.Color("#56b4e9"), SelBg: lipgloss.Color("#2b3a4a"),
		Rx: lipgloss.Color("#56b4e9"), Tx: lipgloss.Color("#e69f00"), ChipFg: lipgloss.Color("#101010"),
	},
}

var (
	curTheme                             = themes[0]
	sText                                lipgloss.Style
	sBorder                              lipgloss.Style
	sBorderOn                            lipgloss.Style
	sRx, sTx                             lipgloss.Style
	sKey, sKeyDesc                       lipgloss.Style
	chipGood, chipBad, chipWarn, chipDim lipgloss.Style
)

func init() { applyTheme("dark") }

// applyTheme switches the global styles. It returns the theme actually used.
func applyTheme(name string) Theme {
	t := themes[0]
	for _, c := range themes {
		if c.Name == name {
			t = c
		}
	}
	curTheme = t
	sText = lipgloss.NewStyle().Foreground(t.Text)
	sDim = lipgloss.NewStyle().Foreground(t.Dim)
	sBold = lipgloss.NewStyle().Bold(true).Foreground(t.Text)
	sAccent = lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
	sWarn = lipgloss.NewStyle().Foreground(t.Warn)
	sBad = lipgloss.NewStyle().Foreground(t.Bad)
	sGood = lipgloss.NewStyle().Foreground(t.Good)
	sSel = lipgloss.NewStyle().Background(t.SelBg).Foreground(t.Text).Bold(true)
	sTab = lipgloss.NewStyle().Padding(0, 1).Foreground(t.Dim)
	sTabOn = lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(t.ChipFg).Background(t.Accent)
	sBorder = lipgloss.NewStyle().Foreground(t.Border)
	sBorderOn = lipgloss.NewStyle().Foreground(t.BorderOn)
	sRx = lipgloss.NewStyle().Foreground(t.Rx)
	sTx = lipgloss.NewStyle().Foreground(t.Tx)
	sKey = lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
	sKeyDesc = lipgloss.NewStyle().Foreground(t.Dim)
	chip := func(bg color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(t.ChipFg).Background(bg).Bold(true).Padding(0, 1)
	}
	chipGood, chipBad, chipWarn, chipDim = chip(t.Good), chip(t.Bad), chip(t.Warn), chip(t.Dim)
	return t
}

func nextTheme(name string) string {
	for i, t := range themes {
		if t.Name == name {
			return themes[(i+1)%len(themes)].Name
		}
	}
	return themes[0].Name
}

// The chosen theme is remembered in the user's config directory.
func themeFile() string {
	d := discover.ConfigDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "theme")
}

func loadTheme() string {
	f := themeFile()
	if f == "" {
		return "dark"
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return "dark"
	}
	n := strings.TrimSpace(string(b))
	for _, t := range themes {
		if t.Name == n {
			return n
		}
	}
	return "dark"
}

func saveTheme(name string) {
	if f := themeFile(); f != "" {
		if os.MkdirAll(filepath.Dir(f), 0o700) == nil {
			_ = os.WriteFile(f, []byte(name+"\n"), 0o600)
		}
	}
}
