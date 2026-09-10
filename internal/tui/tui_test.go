package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
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

func TestUpdateStatusMsg(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(statusMsg{text: "test status"})
	got := next.(*Model)
	if got.ctx.Status != "test status" {
		t.Fatalf("ctx.Status = %q, want %q", got.ctx.Status, "test status")
	}
}

func TestUpdatePushMsg(t *testing.T) {
	m := New(testOptions())
	s := loadingScreen{note: "pushed"}
	next, cmd := m.Update(pushMsg{s: s})
	got := next.(*Model)
	if len(got.stack) != 2 {
		t.Fatalf("stack length = %d, want 2", len(got.stack))
	}
	if got.top() != s {
		t.Fatal("top screen is not the pushed screen")
	}
	if cmd != nil {
		t.Fatal("push should not return a command")
	}
}

func TestUpdatePopMsg(t *testing.T) {
	m := New(testOptions())
	m.stack = append(m.stack, loadingScreen{note: "second"})
	next, cmd := m.Update(popMsg{})
	got := next.(*Model)
	if len(got.stack) != 1 {
		t.Fatalf("stack length = %d, want 1", len(got.stack))
	}
	if cmd != nil {
		t.Fatal("pop should not return a command")
	}
}

func TestUpdatePopMsgWithSingleScreen(t *testing.T) {
	m := New(testOptions())
	if len(m.stack) != 1 {
		t.Fatalf("initial stack length = %d, want 1", len(m.stack))
	}
	next, _ := m.Update(popMsg{})
	got := next.(*Model)
	if len(got.stack) != 1 {
		t.Fatalf("stack length = %d, want 1 (should not pop last screen)", len(got.stack))
	}
}

func TestUpdateDelegatesToScreen(t *testing.T) {
	m := New(testOptions())
	// Send a message that loadingScreen handles (depResolvedMsg)
	next, _ := m.Update(depResolvedMsg{id: 0})
	// The loadingScreen should have updated its note
	ls, ok := next.(*Model).top().(loadingScreen)
	if !ok {
		t.Fatalf("top screen is %T, want loadingScreen", next.(*Model).top())
	}
	if ls.note != "resolved a dependency" {
		t.Fatalf("note = %q, want %q", ls.note, "resolved a dependency")
	}
}

func TestHeaderWithReadOnly(t *testing.T) {
	m := New(testOptions())
	m.ctx.ReadOnly = true
	h := m.header()
	if !strings.Contains(h, "[read-only]") {
		t.Fatalf("header = %q, want it to contain [read-only]", h)
	}
}

func TestHeaderWithoutReadOnly(t *testing.T) {
	m := New(testOptions())
	m.ctx.ReadOnly = false
	h := m.header()
	if strings.Contains(h, "[read-only]") {
		t.Fatalf("header = %q, should not contain [read-only]", h)
	}
}

func TestFooterWithStatus(t *testing.T) {
	m := New(testOptions())
	m.ctx.Status = "test status"
	f := m.footer()
	if !strings.Contains(f, "test status") {
		t.Fatalf("footer = %q, want it to contain status", f)
	}
}

func TestFooterWithError(t *testing.T) {
	m := New(testOptions())
	m.err = errBoom
	f := m.footer()
	if !strings.Contains(f, "boom") {
		t.Fatalf("footer = %q, want it to contain error", f)
	}
}

func TestFooterDefault(t *testing.T) {
	m := New(testOptions())
	f := m.footer()
	if !strings.Contains(f, "help") {
		t.Fatalf("footer = %q, want default help text", f)
	}
}

// TestAReloadDisarmsAPendingComparison pins A9. The status line was the only
// sign that d or i was half-pressed, and a reload clears it, so a left-hand
// side that survived would ambush the next press with a comparison against a
// revision the reader stopped looking at -- and which the new snapshot may not
// contain at all.
func TestAReloadDisarmsAPendingComparison(t *testing.T) {
	m := New(testOptions())
	m.Update(snapshotMsg{snap: testSnapshot(t)})

	sel, err := resolveSelection(m.ctx, firstEntityOfKind(t, m.ctx, fleet.KindService))
	if err != nil {
		t.Fatalf("resolveSelection: %v", err)
	}
	m.ctx.pendingDiff, m.ctx.pendingImpact = sel, sel

	m.Update(snapshotMsg{snap: testSnapshot(t)})
	if m.ctx.pendingDiff != (Selection{}) {
		t.Errorf("pendingDiff survived the reload as %+v", m.ctx.pendingDiff)
	}
	if m.ctx.pendingImpact != (Selection{}) {
		t.Errorf("pendingImpact survived the reload as %+v", m.ctx.pendingImpact)
	}
}
