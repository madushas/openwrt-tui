package tui

import (
	"errors"

	"github.com/madushakv/owrt-tui/internal/history"
)

// Tests must never read or write the user's real config directory.
func init() {
	loadThemeFn = func() string { return "dark" }
	saveThemeFn = func(string) {}
	historyOpenFn = func(string) (*history.Store, error) { return nil, errors.New("no history in tests") }
}
