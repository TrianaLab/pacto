package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

func TestLoadingScreenViewWithoutAnimation(t *testing.T) {
	l := loadingScreen{}
	v := l.View(&Context{Width: 80})
	if !strings.Contains(v, "Building the fleet snapshot") {
		t.Fatalf("View() = %q, want the base message", v)
	}
}

// TestLoadingScreenViewAnimates covers the spinner and the sweeping bar, which
// only appear with motion on, and asserts the frame actually changes: a spinner
// that renders the same glyph on every frame is not a spinner.
func TestLoadingScreenViewAnimates(t *testing.T) {
	l := loadingScreen{}
	c := &Context{Width: 80, Anim: true}
	first := l.View(c)
	if !strings.Contains(first, spinnerAt(0)) {
		t.Fatalf("View() = %q, want the spinner glyph", first)
	}
	if !strings.Contains(first, glyphBarFull) {
		t.Fatalf("View() = %q, want the sweeping bar", first)
	}
	c.Frame = 1
	if second := l.View(c); second == first {
		t.Fatal("View() rendered an identical frame after the clock advanced")
	}
	if !l.animating(c) {
		t.Fatal("a loading screen always has something to animate")
	}
}

// TestLoadingScreenViewNarrow covers the width clamp on the sweeping bar: it is
// the one element sized from c.Width, so at 20 columns it has to shrink to fit
// rather than wrap the frame. The headline is a fixed sentence and is left to
// the terminal to wrap; only a drawn-to-width element can overflow by arithmetic.
func TestLoadingScreenViewNarrow(t *testing.T) {
	for _, w := range []int{20, 80} {
		v := loadingScreen{}.View(&Context{Width: w, Anim: true})
		bar := ""
		for _, line := range strings.Split(v, "\n") {
			if strings.Contains(line, glyphBarFull) || strings.Contains(line, glyphBarEmpty) {
				bar = line
			}
		}
		if bar == "" {
			t.Fatalf("width %d: no sweeping bar in:\n%s", w, v)
		}
		if got := lipgloss.Width(bar); got > w {
			t.Fatalf("width %d: bar is %d cells wide", w, got)
		}
	}
}

// TestLoadingScreenUpdateIsInert covers the whole of Update: the screen is up
// only until the first snapshot lands, so there is no message it can act on.
func TestLoadingScreenUpdateIsInert(t *testing.T) {
	l := loadingScreen{}
	next, cmd := l.Update(&Context{}, tea.WindowSizeMsg{})
	if cmd != nil {
		t.Fatal("Update returned a command, want nil")
	}
	if _, ok := next.(loadingScreen); !ok {
		t.Fatalf("Update returned %T, want loadingScreen", next)
	}
}
