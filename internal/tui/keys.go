package tui

import tea "charm.land/bubbletea/v2"

// binding is one key and what it does. For the global keys the same slice
// drives dispatch and the help screen, so help can never drift from behaviour.
// A screen's own bindings() reuses the type to describe keys its Update handles
// directly and leaves Cmd nil: nothing dispatches those from a table.
type binding struct {
	Key  string
	Help string
	Cmd  func(m *Model) tea.Cmd
}

// screenBindings is the optional interface a screen implements to list the keys
// it handles itself. helpScreen renders them for the screen below it, which is
// what "the key table for the current screen" has always promised: tab, /,
// enter, a and the depth keys are exactly what a first-time reader needs, and
// none of them is a global key or a verb.
type screenBindings interface {
	bindings() []binding
}

// globalBindings are the keys that mean the same thing on every screen, in the
// order the help screen lists them.
func globalBindings() []binding {
	return []binding{
		{"?", "toggle this help", func(m *Model) tea.Cmd {
			// Toggle, as the help text says. helpScreen.Update ignores every
			// message and globalKey runs before the top screen, so without this a
			// second ? pushed a second help screen and the reader needed one q per
			// press to get back.
			if _, ok := m.top().(helpScreen); ok {
				return pop()
			}
			return push(helpScreen{under: m.top()})
		}},
		{"r", "reload the fleet snapshot", func(m *Model) tea.Cmd {
			m.ctx.Status = "reloading the snapshot"
			return loadSnapshot(m.ctx)
		}},
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
	// A screen can also claim esc alone, for a state it has to back out of
	// before leaving. The list claims it while a filter is applied, which is
	// what its own filter line has always promised.
	ownsEsc := false
	if e, ok := m.top().(interface{ ownsEscape() bool }); ok {
		ownsEsc = e.ownsEscape()
	}
	s := k.String()
	for _, b := range globalBindings() {
		if b.Key != s {
			continue
		}
		if capturing && b.Key != "ctrl+c" {
			return nil, false
		}
		if ownsEsc && b.Key == "esc" {
			return nil, false
		}
		return b.Cmd(m), true
	}
	return nil, false
}
