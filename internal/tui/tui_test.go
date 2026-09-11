package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	s := newConfirmScreen("pushed", []string{"pacto"}, func() tea.Cmd { return nil })
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
	m.stack = append(m.stack, loadingScreen{})
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

// TestUpdateDelegatesToScreen sends a message no global key claims and checks
// that the top screen got it: the confirm screen answers n by popping itself.
func TestUpdateDelegatesToScreen(t *testing.T) {
	m := New(testOptions())
	m.stack = append(m.stack, newConfirmScreen("run", []string{"pacto"}, func() tea.Cmd { return nil }))
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if cmd == nil {
		t.Fatal("the top screen was not asked to handle the key")
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

// TestHeaderFitsTheTerminal pins the one thing the header has to do besides
// name the trail: stay on its own line. Every push added a crumb and nothing
// dropped one, so twelve of them gave a 285-column breadcrumb on an 80-column
// terminal and the frame wrapped it over the body.
func TestHeaderFitsTheTerminal(t *testing.T) {
	for _, tt := range []struct {
		name     string
		crumbs   []string
		readOnly bool
		want     string // a crumb the header must still show
	}{
		{"a short trail is untouched", []string{"Fleet", "payments"}, false, "payments"},
		{"a long trail keeps both ends", []string{
			"Fleet", "payments-api", "Graph: payments-api", "orders-api",
			"Graph: orders-api", "shipping-api", "Graph: shipping-api",
			"billing-api", "Graph: billing-api", "notifications-api",
		}, false, "Fleet"},
		{"the read-only tag is part of the budget", []string{
			"Fleet", "payments-api", "Graph: payments-api", "orders-api",
			"Graph: orders-api", "shipping-api", "Graph: shipping-api",
		}, true, "[read-only]"},
		{"two crumbs too long for the width are cut", []string{
			strings.Repeat("a", 60), strings.Repeat("b", 60),
		}, false, "aaaa"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := New(testOptions())
			m.ctx.ReadOnly = tt.readOnly
			m.stack = nil
			for _, c := range tt.crumbs {
				m.stack = append(m.stack, newOutputScreen(c))
			}
			h := m.header()
			if got := lipgloss.Width(h); got > m.ctx.Width {
				t.Fatalf("header is %d columns wide on a %d-column terminal:\n%s", got, m.ctx.Width, h)
			}
			if !strings.Contains(h, tt.want) {
				t.Fatalf("header lost %q:\n%s", tt.want, h)
			}
		})
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

// TestFooterPrefersTheErrorOverAStaleStatus pins the order of the two branches.
// Both fields are set at once on the ordinary path -- a verb sets a status when
// it starts and the failure lands afterwards -- so checking Status first would
// leave the reader looking at the line that announced the work while the reason
// it stopped is never shown.
func TestFooterPrefersTheErrorOverAStaleStatus(t *testing.T) {
	m := New(testOptions())
	m.ctx.Status = "running pacto validate"
	m.err = errBoom
	f := m.footer()
	if !strings.Contains(f, "boom") {
		t.Fatalf("footer = %q, want the error", f)
	}
	if strings.Contains(f, "running pacto validate") {
		t.Fatalf("footer = %q, want the error to replace the status that preceded it", f)
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
