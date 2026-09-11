package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestGlobalKeys(t *testing.T) {
	tests := []struct {
		name        string
		key         tea.KeyPressMsg
		handled     bool
		want        string // "" means no assertion on the produced message
		needsStacks bool   // true to push an extra screen before testing
	}{
		{"ctrl+c quits", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, true, "tea.QuitMsg", false},
		{"q pops", tea.KeyPressMsg{Code: 'q', Text: "q"}, true, "tui.popMsg", true},
		{"question mark pushes help", tea.KeyPressMsg{Code: '?', Text: "?"}, true, "tui.pushMsg", false},
		{"j falls through to the screen", tea.KeyPressMsg{Code: 'j', Text: "j"}, false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(testOptions())
			if tt.needsStacks {
				// Add a second screen so q has something to pop
				m.stack = append(m.stack, helpScreen{})
			}
			cmd, handled := globalKey(m, tt.key)
			if handled != tt.handled {
				t.Fatalf("handled = %v, want %v", handled, tt.handled)
			}
			if tt.want == "" {
				return
			}
			if got := msgTypeName(cmd()); got != tt.want {
				t.Fatalf("produced %s, want %s", got, tt.want)
			}
		})
	}
}

func TestQuitOnTheLastScreenQuitsTheProgram(t *testing.T) {
	// q on the root screen has nothing to pop; it must exit rather than no-op,
	// otherwise the only way out is ctrl+c.
	m := New(testOptions())
	cmd, _ := globalKey(m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q on the root screen produced %T, want tea.QuitMsg", cmd())
	}
}

func TestEveryGlobalBindingHasHelpText(t *testing.T) {
	for _, b := range globalBindings() {
		if b.Key == "" || b.Help == "" || b.Cmd == nil {
			t.Errorf("incomplete binding: %+v", b)
		}
	}
}

func msgTypeName(m tea.Msg) string { return fmt.Sprintf("%T", m) }

func TestGlobalKeyWithTextCapture(t *testing.T) {
	// When a screen is capturing text, only ctrl+c should remain global
	m := New(testOptions())
	m.stack = []screen{&captureScreen{}}

	// ctrl+c should still work
	cmd, handled := globalKey(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("ctrl+c should be handled even during text capture")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c should quit")
	}

	// q should NOT be handled during text capture
	cmd, handled = globalKey(m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if handled {
		t.Fatal("q should not be handled during text capture")
	}
	if cmd != nil {
		t.Error("q returned a command during text capture")
	}

	// ? should NOT be handled during text capture
	cmd, handled = globalKey(m, tea.KeyPressMsg{Code: '?', Text: "?"})
	if handled {
		t.Fatal("? should not be handled during text capture")
	}
	if cmd != nil {
		t.Error("? returned a command during text capture")
	}
}

func TestGlobalKeyEscPops(t *testing.T) {
	m := New(testOptions())
	m.stack = append(m.stack, helpScreen{})
	// Use a KeyPressMsg that produces "esc" from String()
	cmd, handled := globalKey(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled {
		t.Fatal("esc should be handled")
	}
	if got := msgTypeName(cmd()); got != "tui.popMsg" {
		t.Fatalf("esc produced %s, want tui.popMsg", got)
	}
}

// captureScreen is a test screen that captures text
type captureScreen struct{}

func (c *captureScreen) Title() string                              { return "Capture" }
func (c *captureScreen) Update(*Context, tea.Msg) (screen, tea.Cmd) { return c, nil }
func (c *captureScreen) View(*Context) string                       { return "capturing" }
func (c *captureScreen) capturesText() bool                         { return true }

// listWithAppliedFilter returns a root model showing the list with a filter
// already applied — the state three surfaces describe as "esc clears".
func listWithAppliedFilter(t *testing.T) *Model {
	t.Helper()
	m := New(testOptions())
	m.Update(snapshotMsg{snap: testSnapshot(t)})
	l, ok := m.top().(*listScreen)
	if !ok {
		t.Fatalf("the snapshot left %T on top, want the list", m.top())
	}
	l.filterText = testServiceName
	l.refresh(m.ctx)
	return m
}

// quits reports whether cmd is the one that ends the program.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// TestEscClearsAnAppliedFilterRatherThanQuitting drives the key through
// Model.Update, which is where the bug lived: globalKey runs before the top
// screen sees the key, so esc reached quitOrPop with a stack of one and ended
// the session while the filter line said "(esc clears)".
func TestEscClearsAnAppliedFilterRatherThanQuitting(t *testing.T) {
	m := listWithAppliedFilter(t)

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if quits(cmd) {
		t.Fatal("esc quit the session instead of clearing the applied filter")
	}
	if got := m.top().(*listScreen).filterText; got != "" {
		t.Fatalf("filterText = %q, want esc to have cleared it", got)
	}
}

// TestEscStillLeavesWhenNothingOwnsIt keeps the stand-down narrow: with no
// filter applied there is nothing to clear, so esc means what the help says.
func TestEscStillLeavesWhenNothingOwnsIt(t *testing.T) {
	m := New(testOptions())
	m.Update(snapshotMsg{snap: testSnapshot(t)})

	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !quits(cmd) {
		t.Fatal("esc on an unfiltered root list did not quit")
	}
}

// TestQIsUnconditional is the escape hatch: whatever a screen claims about esc,
// q always leaves, so nobody can be trapped in the TUI.
func TestQIsUnconditional(t *testing.T) {
	m := listWithAppliedFilter(t)

	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); !quits(cmd) {
		t.Fatal("q did not quit from the root screen while a filter was applied")
	}
}
