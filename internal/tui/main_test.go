package tui

// Tests must never read or write the user's real config directory.
func init() {
	loadThemeFn = func() string { return "dark" }
	saveThemeFn = func(string) {}
}
