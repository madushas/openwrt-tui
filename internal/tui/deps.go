//go:build tui

// Package tui will hold the Bubble Tea screens. For now this file only pins the
// Charm v2 packages the UI will use, so "go mod tidy" and "go mod vendor" keep
// them (tidy drops requirements nothing imports). The "tui" build tag keeps
// the default build free of Charm dependencies until the screens exist.
package tui

import (
	_ "charm.land/bubbles/v2/help"
	_ "charm.land/bubbles/v2/key"
	_ "charm.land/bubbles/v2/spinner"
	_ "charm.land/bubbles/v2/table"
	_ "charm.land/bubbles/v2/textinput"
	_ "charm.land/bubbles/v2/viewport"
	_ "charm.land/bubbletea/v2"
	_ "charm.land/lipgloss/v2"
)
