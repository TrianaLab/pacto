package tui

import (
	"strings"
	"testing"
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
