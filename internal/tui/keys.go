package tui

import tea "charm.land/bubbletea/v2"

// binding is one key and what it does. The same slice drives dispatch and the
// help screen, so help can never drift from behaviour.
type binding struct {
	Key  string
	Help string
	Cmd  func(m *Model) tea.Cmd
}

// globalBindings are the keys that mean the same thing on every screen, in the
// order the help screen lists them.
func globalBindings() []binding {
	return []binding{
		{"?", "toggle this help", func(m *Model) tea.Cmd { return push(helpScreen{}) }},
		{"q", "back, or quit from the root screen", quitOrPop},
		{"esc", "back, or quit from the root screen", quitOrPop},
		{"ctrl+c", "quit immediately", func(m *Model) tea.Cmd { return tea.Quit }},
	}
}

// quitOrPop pops a screen, or quits when there is nothing left to pop. Without
// the quit fallback, q on the root screen would silently do nothing and the
// only exit would be ctrl+c.
func quitOrPop(m *Model) tea.Cmd {
	if len(m.stack) > 1 {
		return pop()
	}
	return tea.Quit
}

// globalKey dispatches k against globalBindings. handled=true means the key was
// consumed and must not reach the top screen.
func globalKey(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	// A screen in text-entry mode owns every printable key, so only the
	// unambiguous control keys stay global while it is capturing.
	capturing := false
	if t, ok := m.top().(interface{ capturesText() bool }); ok {
		capturing = t.capturesText()
	}
	s := k.String()
	for _, b := range globalBindings() {
		if b.Key != s {
			continue
		}
		if capturing && b.Key != "ctrl+c" {
			return nil, false
		}
		return b.Cmd(m), true
	}
	return nil, false
}
