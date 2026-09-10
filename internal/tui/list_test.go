package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// newLoadedContext returns a Context with a real query over the test snapshot,
// a real service and a recording message sink.
func newLoadedContext(t *testing.T) *Context {
	t.Helper()
	// One snapshot, not two. tui.go:89-90 builds Query and Snapshot from the same
	// load, and verbImpact passes c.Snapshot precisely so the answer binds to the
	// fleet on screen; two independent builds would let that mismatch pass here.
	snap := testSnapshot(t)
	return &Context{
		Svc:      app.NewService(nil, nil),
		Query:    fleet.NewQuery(snap),
		Send:     &sender{p: &msgRecorder{}},
		Snapshot: snap,
		Width:    100,
		Height:   30,
	}
}

func TestListShowsEntities(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	out := s.View(c)
	if !strings.Contains(out, testServiceName) {
		t.Fatalf("list does not show %q:\n%s", testServiceName, out)
	}
}

// TestListOpensOnServices pins the landing tab. All sorts kind-first, so on a
// fleet with enough owners and revisions to fill a page the All tab shows no
// services at all -- which is the whole screen the reader came for.
func TestListOpensOnServices(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	if got := l.kinds()[l.kindIx]; got != fleet.KindService {
		t.Fatalf("the session opened on the %q tab, want %q", got, fleet.KindService)
	}
}

func TestListTabsCycleKinds(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	first := s.(*listScreen).kindIx
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab})
	if s.(*listScreen).kindIx == first {
		t.Fatal("tab did not advance the kind tab")
	}
	if got := s.(*listScreen).kinds()[s.(*listScreen).kindIx]; got != fleet.KindRevision {
		t.Fatalf("the tab after Services = %q, want %q", got, fleet.KindRevision)
	}
	// Shift+tab goes back.
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if s.(*listScreen).kindIx != first {
		t.Fatal("shift+tab did not go back")
	}
}

func TestListTabWrapsAtTheEnd(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c).(*listScreen)
	n := len(s.kinds())
	start := s.kindIx
	var cur screen = s
	for i := 0; i < n; i++ {
		cur, _ = cur.Update(c, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if cur.(*listScreen).kindIx != start {
		t.Fatalf("kindIx = %d after a full cycle, want %d", cur.(*listScreen).kindIx, start)
	}
}

func TestListSelectedReturnsTheHighlightedEntity(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c).(*listScreen)
	if len(s.entities) == 0 {
		t.Fatal("test setup: need at least one entity")
	}
	ref, ok := s.selected()
	if !ok {
		t.Fatal("nothing selected in a non-empty list")
	}
	expected := s.entities[0]
	if ref.Key != expected.Key || ref.Kind != expected.Kind {
		t.Fatalf("selected() = {Kind: %q, Key: %q}, want {Kind: %q, Key: %q}",
			ref.Kind, ref.Key, expected.Kind, expected.Key)
	}
}

func TestListSelectedOnAnEmptyListReportsNothing(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c).(*listScreen)
	s.entities = nil
	if _, ok := s.selected(); ok {
		t.Fatal("an empty list must not report a selection")
	}
}

func TestListSurfacesAQueryError(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c).(*listScreen)
	if len(s.entities) == 0 {
		t.Fatal("test setup: list should have entities initially")
	}
	s.loadErr = errBoom
	s.entities, s.total, s.shown, s.truncated = nil, 0, 0, false
	s.tbl.SetRows(nil)
	if !strings.Contains(s.View(c), "boom") {
		t.Fatalf("the query error is not shown:\n%s", s.View(c))
	}
	if len(s.entities) != 0 {
		t.Fatal("entities should be cleared when an error is set")
	}
}

// TestListSaysWhenTheAnswerIsTruncated asserts on summary() rather than on the
// whole view, and on the sentence rather than on the two numbers loose in it.
// Over the view the old assertion passed with the warning deleted outright:
// "25" was matched by a fixture digest of repeated 2s and "900" by the
// fall-through "%d entities" line, so the test named for the truncation warning
// was green whether or not there was one.
func TestListSaysWhenTheAnswerIsTruncated(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c).(*listScreen)

	s.truncated, s.total, s.shown = true, 900, 25
	if got := s.summary(); !strings.Contains(got, "showing 25 of 900") {
		t.Fatalf("summary = %q, want it to say showing 25 of 900", got)
	}

	// And the whole page does not present itself as truncated.
	s.truncated = false
	if got := s.summary(); !strings.Contains(got, "900 entities") {
		t.Fatalf("summary = %q, want the plain count", got)
	}
}

func TestListHandlesWindowSizeMsg(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	c.Width, c.Height = 120, 40
	s, _ = s.Update(c, tea.WindowSizeMsg{Width: 120, Height: 40})
	l := s.(*listScreen)
	if l.tbl.Width() != 120 {
		t.Fatalf("table width = %d, want 120", l.tbl.Width())
	}
}

func TestListDelegatesOtherMessagesToTable(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	l := s.(*listScreen)
	if len(l.entities) < 2 {
		t.Fatalf("test setup: need at least 2 entities to test cursor movement, got %d", len(l.entities))
	}
	before := l.tbl.Cursor()
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyDown})
	if s == nil {
		t.Fatal("Update returned nil screen")
	}
	after := s.(*listScreen).tbl.Cursor()
	if after <= before {
		t.Fatalf("cursor = %d after down arrow, want > %d", after, before)
	}
}

func TestListResizeHandlesSmallHeights(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	c.Height = 2
	l.resize(c)
	out := l.View(c)
	if out == "" {
		t.Fatal("View() should render even with small height")
	}
}

func TestSlashOpensTheFilterAndCapturesText(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '/', Text: "/"})
	l := s.(*listScreen)
	if !l.capturesText() {
		t.Fatal("/ did not put the list into text-capture mode")
	}
	// A global key must now fall through to the input rather than popping.
	m := New(testOptions())
	m.ctx = c
	m.stack = []screen{l}
	if _, handled := globalKey(m, tea.KeyPressMsg{Code: 'q', Text: "q"}); handled {
		t.Fatal("q was consumed globally while the filter was focused")
	}
}

func TestEnterAppliesTheFilterAndEscapeClearsIt(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '/', Text: "/"})
	s.(*listScreen).input.SetValue("zzz-no-such-service")
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyEnter})
	l := s.(*listScreen)
	if l.capturesText() {
		t.Fatal("enter did not leave text-capture mode")
	}
	if l.filterText != "zzz-no-such-service" {
		t.Fatalf("filterText = %q, want the typed value", l.filterText)
	}
	if len(l.entities) != 0 {
		t.Fatalf("filter matched %d entities, want 0", len(l.entities))
	}
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyEscape})
	if s.(*listScreen).filterText != "" {
		t.Fatal("escape did not clear the applied filter")
	}
	if len(s.(*listScreen).entities) == 0 {
		t.Fatal("clearing the filter did not restore the full list")
	}
}

func TestEscapeWhileTypingCancelsWithoutApplying(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '/', Text: "/"})
	s.(*listScreen).input.SetValue("half-typed")
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := s.(*listScreen).filterText; got != "" {
		t.Fatalf("filterText = %q, want it unapplied", got)
	}
}

func TestListFilterLineShowsInputWhenTyping(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.typing = true
	l.input.SetValue("test")
	line := l.filterLine()
	if !strings.Contains(line, "test") {
		t.Fatalf("filterLine while typing does not show input value: %q", line)
	}
}

func TestListFilterLineShowsAppliedFilter(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.filterText = "applied"
	line := l.filterLine()
	if !strings.Contains(line, "applied") {
		t.Fatalf("filterLine with applied filter does not show it: %q", line)
	}
	if !strings.Contains(line, "esc") {
		t.Fatal("filterLine with applied filter should mention esc clears")
	}
}

func TestListFilterLineShowsHelpWhenNoFilter(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	line := l.filterLine()
	if !strings.Contains(line, "/") {
		t.Fatalf("filterLine with no filter should show help: %q", line)
	}
	if !strings.Contains(line, "attention") {
		t.Fatalf("filterLine help should mention attention: %q", line)
	}
}

func TestListTypingDelegatesToInput(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '/', Text: "/"})
	s, _ = s.Update(c, tea.KeyPressMsg{Code: 't', Text: "t"})
	l := s.(*listScreen)
	if !strings.Contains(l.input.Value(), "t") {
		t.Fatal("typing should update the input value")
	}
}

func TestListEscWithNoFilterDoesNothing(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c)
	initialEntities := len(s.(*listScreen).entities)
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(s.(*listScreen).entities) != initialEntities {
		t.Fatal("esc with no filter should not change entities")
	}
}

func TestListAKeyPushesAttentionScreen(t *testing.T) {
	c := newLoadedContext(t)
	m := New(testOptions())
	m.ctx = c
	m.stack = []screen{newListScreen(c)}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	got := next.(*Model)
	if cmd == nil {
		t.Fatal("'a' key should return a command")
	}
	next, _ = got.Update(cmd())
	if len(next.(*Model).stack) != 2 {
		t.Fatalf("stack length = %d, want 2 after pushing attention", len(next.(*Model).stack))
	}
	if next.(*Model).top().Title() != "Attention" {
		t.Fatalf("top screen title = %q, want Attention", next.(*Model).top().Title())
	}
}

func TestListLoadWithInvalidFilterSetsError(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	if len(l.entities) == 0 {
		t.Fatal("test setup: list should have entities initially")
	}
	l.load(c, fleet.EntityFilter{Offset: -1})
	if l.loadErr == nil {
		t.Fatal("load with negative offset should produce an error")
	}
	if l.entities != nil {
		t.Fatal("entities should be cleared after a query error")
	}
	if l.tbl.Rows() != nil {
		t.Fatal("table rows should be cleared after a query error")
	}
	out := l.View(c)
	if !strings.Contains(out, "query failed") {
		t.Fatalf("View should surface the query error:\n%s", out)
	}
}

func TestListPressGPushesGraphScreen(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	// The unfiltered list sorts owners first and a graph does not root at one,
	// so put the cursor on a service before pressing the key.
	cursorOnKind(t, l, fleet.KindService)
	_, cmd := l.Update(c, tea.KeyPressMsg{Code: 'g', Text: "g"})
	if cmd == nil {
		t.Fatal("g did not produce a command")
	}
	msg := cmd()
	pm, ok := msg.(pushMsg)
	if !ok {
		t.Fatalf("g produced %T, want pushMsg", msg)
	}
	if !strings.Contains(pm.s.Title(), "Graph:") {
		t.Fatalf("g pushed %q, want a graph screen", pm.s.Title())
	}
}

func TestListPressGWithNoSelectionDoesNothing(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.entities = nil // Clear entities so nothing is selected
	_, cmd := l.Update(c, tea.KeyPressMsg{Code: 'g', Text: "g"})
	if cmd == nil {
		t.Fatal("g with no selection should produce a status message")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("g with no selection produced %T, want statusMsg", cmd())
	}
	if msg.text == "" {
		t.Fatal("status message should not be empty")
	}
}

// TestListRowsCannotBeForged covers the half of A3 the reviewer saw survive
// past the confirmation: the same bytes reach the table, where a row can
// repaint itself as a healthier one.
func TestListRowsCannotBeForged(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "evil"), "evil-svc", "1.0.0",
		"  owner:\n    team: \"platform\\r\\e[2Kcompliant\"\n")
	c := newContextOverLocalRoot(t, root)
	l := newListScreen(c).(*listScreen)

	out := l.View(c)
	if strings.Contains(out, "\r") || strings.Contains(out, "\x1b[2K") {
		t.Fatalf("a row emits the bytes that repaint the line:\n%q", out)
	}
}
