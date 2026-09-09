package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPushReturnsAPushMsg(t *testing.T) {
	s := loadingScreen{}
	cmd := push(s)
	if cmd == nil {
		t.Fatal("push() returned nil")
	}
	msg := cmd()
	pm, ok := msg.(pushMsg)
	if !ok {
		t.Fatalf("push() returned %T, want pushMsg", msg)
	}
	if pm.s != s {
		t.Fatal("pushMsg does not carry the screen")
	}
}

func TestPopReturnsAPopMsg(t *testing.T) {
	cmd := pop()
	if cmd == nil {
		t.Fatal("pop() returned nil")
	}
	msg := cmd()
	if _, ok := msg.(popMsg); !ok {
		t.Fatalf("pop() returned %T, want popMsg", msg)
	}
}

func TestLoadingScreenTitle(t *testing.T) {
	l := loadingScreen{}
	if got := l.Title(); got != "Loading" {
		t.Fatalf("Title() = %q, want %q", got, "Loading")
	}
}

func TestLoadingScreenViewWithoutNote(t *testing.T) {
	l := loadingScreen{}
	v := l.View(nil)
	if v != "Building the fleet snapshot..." {
		t.Fatalf("View() = %q, want base message", v)
	}
}

func TestLoadingScreenViewWithNote(t *testing.T) {
	l := loadingScreen{note: "test note"}
	v := l.View(nil)
	expected := "Building the fleet snapshot...\n\n  test note"
	if v != expected {
		t.Fatalf("View() = %q, want %q", v, expected)
	}
}

func TestLoadingScreenUpdateWithDepResolvedMsg(t *testing.T) {
	l := loadingScreen{}
	c := &Context{}
	next, cmd := l.Update(c, depResolvedMsg{})
	if cmd != nil {
		t.Fatal("Update with depResolvedMsg returned a command, want nil")
	}
	nextLoading, ok := next.(loadingScreen)
	if !ok {
		t.Fatalf("Update returned %T, want loadingScreen", next)
	}
	if nextLoading.note != "resolved a dependency" {
		t.Fatalf("note = %q, want %q", nextLoading.note, "resolved a dependency")
	}
}

func TestLoadingScreenUpdateWithOtherMsg(t *testing.T) {
	l := loadingScreen{note: "existing"}
	c := &Context{}
	next, cmd := l.Update(c, tea.WindowSizeMsg{})
	if cmd != nil {
		t.Fatal("Update with other msg returned a command, want nil")
	}
	nextLoading, ok := next.(loadingScreen)
	if !ok {
		t.Fatalf("Update returned %T, want loadingScreen", next)
	}
	if nextLoading.note != "existing" {
		t.Fatal("note was modified on unhandled message")
	}
}
