package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
)

var errBoom = errors.New("boom")

func testOptions() Options {
	return Options{Svc: &app.Service{}, Exe: "/usr/bin/pacto"}
}

func TestNewStartsWithALoadingScreen(t *testing.T) {
	m := New(testOptions())
	if got := m.top().Title(); got != "Loading" {
		t.Fatalf("Title() = %q, want %q", got, "Loading")
	}
	if m.Init() == nil {
		t.Fatal("Init() returned nil, want a snapshot-loading command")
	}
}

func TestViewAlwaysRequestsTheAltScreen(t *testing.T) {
	m := New(testOptions())
	v := m.View()
	if !v.AltScreen {
		t.Fatal("View().AltScreen = false; bubbletea v2 needs it set on every frame")
	}
	if v.Content == "" {
		t.Fatal("View().Content is empty")
	}
}

func TestWindowSizeIsRecordedOnTheContext(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	got := next.(*Model)
	if got.ctx.Width != 120 || got.ctx.Height != 40 {
		t.Fatalf("size = %dx%d, want 120x40", got.ctx.Width, got.ctx.Height)
	}
}

func TestCtrlCQuits(t *testing.T) {
	m := New(testOptions())
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c produced no command, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}
}

func TestErrorMsgIsShownInTheFooter(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(errMsg{err: errBoom})
	if !strings.Contains(next.View().Content, "boom") {
		t.Fatalf("view does not show the error:\n%s", next.View().Content)
	}
}
