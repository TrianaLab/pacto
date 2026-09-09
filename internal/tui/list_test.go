package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// newLoadedContext returns a Context with a real query over the test snapshot.
func newLoadedContext(t *testing.T) *Context {
	t.Helper()
	return &Context{
		Query:  fleet.NewQuery(testSnapshot(t)),
		Send:   &sender{},
		Width:  100,
		Height: 30,
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

func TestListTabsCycleKinds(t *testing.T) {
	c := newLoadedContext(t)
	var s screen = newListScreen(c)
	first := s.(*listScreen).kindIx
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab})
	if s.(*listScreen).kindIx == first {
		t.Fatal("tab did not advance the kind tab")
	}
	if got := s.(*listScreen).kinds()[s.(*listScreen).kindIx]; got != fleet.KindService {
		t.Fatalf("tab 1 = %q, want %q", got, fleet.KindService)
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
	var cur screen = s
	for i := 0; i < n; i++ {
		cur, _ = cur.Update(c, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if cur.(*listScreen).kindIx != 0 {
		t.Fatalf("kindIx = %d after a full cycle, want 0", cur.(*listScreen).kindIx)
	}
}

func TestListSelectedReturnsTheHighlightedEntity(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c).(*listScreen)
	ref, ok := s.selected()
	if !ok {
		t.Fatal("nothing selected in a non-empty list")
	}
	if ref.Key == "" {
		t.Fatal("selected ref has no key")
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

func TestListSaysWhenTheAnswerIsTruncated(t *testing.T) {
	c := newLoadedContext(t)
	s := newListScreen(c).(*listScreen)
	s.truncated, s.total, s.shown = true, 900, 25
	out := s.View(c)
	if !strings.Contains(out, "900") || !strings.Contains(out, "25") {
		t.Fatalf("a truncated page must state both numbers:\n%s", out)
	}
}

func TestListHandlesWindowSizeMsg(t *testing.T) {
	c := newLoadedContext(t)
	var s screen = newListScreen(c)
	c.Width, c.Height = 120, 40
	s, _ = s.Update(c, tea.WindowSizeMsg{Width: 120, Height: 40})
	l := s.(*listScreen)
	if l.tbl.Width() != 120 {
		t.Fatalf("table width = %d, want 120", l.tbl.Width())
	}
}

func TestListDelegatesOtherMessagesToTable(t *testing.T) {
	c := newLoadedContext(t)
	var s screen = newListScreen(c)
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyDown})
	if s == nil {
		t.Fatal("Update returned nil screen")
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

func TestListRefreshHandlesQueryErrors(t *testing.T) {
	snap := &fleet.FleetSnapshot{}
	c := &Context{
		Query: fleet.NewQuery(snap),
		Send:  &sender{},
		Svc:   app.NewService(nil, nil),
		Width: 100,
		Height: 30,
	}
	l := newListScreen(c).(*listScreen)
	f := fleet.EntityFilter{Limit: -1}
	list, err := c.Query.Entities(f)
	if err != nil {
		l.loadErr = err
		if err != nil {
			l.entities, l.total, l.shown, l.truncated = nil, 0, 0, false
			l.tbl.SetRows(nil)
		}
	} else {
		l.entities = list.Entities
		l.total, l.shown, l.truncated = list.Total, list.Count, list.Truncated
	}
	if err != nil && l.loadErr == nil {
		t.Fatal("loadErr should be set when query fails")
	}
	if err != nil && len(l.entities) != 0 {
		t.Fatal("entities should be empty after error")
	}
}

func TestSlashOpensTheFilterAndCapturesText(t *testing.T) {
	c := newLoadedContext(t)
	var s screen = newListScreen(c)
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
	var s screen = newListScreen(c)
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
	var s screen = newListScreen(c)
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
	var s screen = newListScreen(c)
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '/', Text: "/"})
	s, _ = s.Update(c, tea.KeyPressMsg{Code: 't', Text: "t"})
	l := s.(*listScreen)
	if !strings.Contains(l.input.Value(), "t") {
		t.Fatal("typing should update the input value")
	}
}

func TestListEscWithNoFilterDoesNothing(t *testing.T) {
	c := newLoadedContext(t)
	var s screen = newListScreen(c)
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
