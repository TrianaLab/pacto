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

	// ? should NOT be handled during text capture
	cmd, handled = globalKey(m, tea.KeyPressMsg{Code: '?', Text: "?"})
	if handled {
		t.Fatal("? should not be handled during text capture")
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

func (c *captureScreen) Title() string                                  { return "Capture" }
func (c *captureScreen) Update(*Context, tea.Msg) (screen, tea.Cmd)     { return c, nil }
func (c *captureScreen) View(*Context) string                           { return "capturing" }
func (c *captureScreen) capturesText() bool                             { return true }
