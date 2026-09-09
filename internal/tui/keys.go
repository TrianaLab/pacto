package tui

import tea "charm.land/bubbletea/v2"

// globalKey handles the keys that mean the same thing on every screen. It
// returns handled=true when the key was consumed and must not reach the top
// screen.
func globalKey(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "ctrl+c":
		return tea.Quit, true
	}
	return nil, false
}
