package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestHelpScreenTitle(t *testing.T) {
	h := helpScreen{}
	if got := h.Title(); got != "Help" {
		t.Fatalf("Title() = %q, want %q", got, "Help")
	}
}

func TestHelpScreenUpdate(t *testing.T) {
	h := helpScreen{}
	c := &Context{}
	next, cmd := h.Update(c, statusMsg{text: "test"})
	if next != h {
		t.Fatal("Update should return the receiver unchanged")
	}
	if cmd != nil {
		t.Fatal("Update should return nil command")
	}
}

func TestHelpScreenView(t *testing.T) {
	h := helpScreen{}
	c := &Context{}
	view := h.View(c)
	if view == "" {
		t.Fatal("View should not be empty")
	}
	if !strings.Contains(view, "Keys") {
		t.Fatal("View should contain 'Keys' header")
	}
	if !strings.Contains(view, "Verbs") {
		t.Fatal("View should contain 'Verbs' header")
	}
	// Check that global bindings are listed
	for _, b := range globalBindings() {
		if !strings.Contains(view, b.Key) {
			t.Fatalf("View should contain key %q", b.Key)
		}
	}
}

// press drives one key through the model and then delivers the message its
// command produced, which is what the bubbletea runtime does. A test that only
// calls Update sees the pushMsg but never the stack it changes.
func press(t *testing.T, m *Model, k tea.KeyPressMsg) {
	t.Helper()
	_, cmd := m.Update(k)
	if cmd == nil {
		return
	}
	m.Update(cmd())
}

// TestHelpTogglesRatherThanStacking pins ? as a toggle, which is what its own
// help text calls it. helpScreen.Update ignores every message and globalKey runs
// first, so before the fix each press pushed another help screen and twelve g
// presses left a stack of fourteen.
func TestHelpTogglesRatherThanStacking(t *testing.T) {
	m := New(testOptions())
	m.Update(snapshotMsg{snap: testSnapshot(t)})
	depth := len(m.stack)

	press(t, m, tea.KeyPressMsg{Code: '?', Text: "?"})
	if _, ok := m.top().(helpScreen); !ok {
		t.Fatalf("? left %T on top, want the help screen", m.top())
	}
	press(t, m, tea.KeyPressMsg{Code: '?', Text: "?"})
	if len(m.stack) != depth {
		t.Fatalf("stack depth = %d after ? twice, want %d", len(m.stack), depth)
	}
	if _, ok := m.top().(helpScreen); ok {
		t.Fatal("the second ? pushed another help screen instead of closing the first")
	}
}

// TestHelpListsTheNavigationKeysOfTheScreenBelow covers the whole promise:
// every key a screen handles in its own Update has to be reachable from ?,
// because a global key table plus a verb table leaves out the entire keymap a
// first-time reader needs.
func TestHelpListsTheNavigationKeysOfTheScreenBelow(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)

	for _, tt := range []struct {
		name string
		s    screen
		want []string
	}{
		{"list", newListScreen(c), []string{"enter", "/", "esc", "a", "tab", "shift+tab"}},
		{"attention", newAttentionScreen(c), []string{"enter", "tab", "shift+tab"}},
		{"graph", newGraphScreen(c, ref), []string{"+ or =", "- or _", "tab"}},
		{"confirm", newConfirmScreen("run it?", []string{"pacto", "push"}, nil), []string{"y", "n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			view := helpScreen{under: tt.s}.View(c)
			if !strings.Contains(view, tt.s.Title()) {
				t.Fatalf("the help does not name the screen it is describing:\n%s", view)
			}
			for _, key := range tt.want {
				if !strings.Contains(view, key) {
					t.Fatalf("the help omits %q:\n%s", key, view)
				}
			}
		})
	}
}

// TestHelpWithoutAScreenBelowStillRenders covers the boot case: nothing on the
// stack below the help has bindings of its own.
func TestHelpWithoutAScreenBelowStillRenders(t *testing.T) {
	c := newLoadedContext(t)
	if got := (helpScreen{under: loadingScreen{}}).View(c); !strings.Contains(got, "Keys") {
		t.Fatalf("help over a screen with no bindings lost the global keys:\n%s", got)
	}
}

func TestPadShortString(t *testing.T) {
	got := pad("hi", 8)
	if len(got) < 8 {
		t.Fatalf("pad(\"hi\", 8) = %q (len %d), want at least 8 chars", got, len(got))
	}
	if !strings.HasPrefix(got, "hi") {
		t.Fatalf("pad(\"hi\", 8) = %q, want it to start with 'hi'", got)
	}
}

func TestPadLongString(t *testing.T) {
	got := pad("verylongstring", 8)
	if !strings.HasPrefix(got, "verylongstring") {
		t.Fatalf("pad should preserve long string: got %q", got)
	}
	if !strings.HasSuffix(got, " ") {
		t.Fatalf("pad should add trailing space to long string: got %q", got)
	}
}

func TestHelpScreenViewWithVerbs(t *testing.T) {
	h := helpScreen{}
	c := newLoadedContext(t)

	// The help screen should show at least one verb when write verbs are included.
	view := h.View(c)
	if !strings.Contains(view, "validate") {
		t.Fatal("View should contain at least the 'validate' verb")
	}

	// writeVerbs should be shown and carry the write marker in normal mode.
	if !strings.Contains(view, "writes; asks first") {
		t.Fatal("View should show write marker for write verbs")
	}

	// With ReadOnly=true, write verbs should not appear and the marker should be absent.
	c.ReadOnly = true
	viewReadOnly := h.View(c)
	if strings.Contains(viewReadOnly, "writes; asks first") {
		t.Fatal("View should not show write marker in read-only mode")
	}
}
