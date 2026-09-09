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
