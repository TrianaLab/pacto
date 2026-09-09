package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestGraphScreenBuildsFromQuery(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	out := s.View(c)
	if !strings.Contains(out, "depth") {
		t.Fatalf("graph screen does not show depth bar:\n%s", out)
	}
	if !strings.Contains(out, ref.Label) && !strings.Contains(out, ref.Key) {
		t.Fatalf("graph does not show the focus entity:\n%s", out)
	}
}

func TestGraphScreenDepthIncrement(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	initial := s.(*graphScreen).depth
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '+', Text: "+"})
	if got := s.(*graphScreen).depth; got != initial+1 {
		t.Fatalf("+ set depth to %d, want %d", got, initial+1)
	}
}

func TestGraphScreenDepthDecrement(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	s.(*graphScreen).depth = 3
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '-', Text: "-"})
	if got := s.(*graphScreen).depth; got != 2 {
		t.Fatalf("- set depth to %d, want 2", got)
	}
}

func TestGraphScreenDepthClampAtMax(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	s.(*graphScreen).depth = fleet.MaxNeighborhoodDepth
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '+', Text: "+"})
	if got := s.(*graphScreen).depth; got != fleet.MaxNeighborhoodDepth {
		t.Fatalf("+ beyond max set depth to %d, want %d", got, fleet.MaxNeighborhoodDepth)
	}
}

func TestGraphScreenDepthClampAtMin(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	s.(*graphScreen).depth = 1
	s, _ = s.Update(c, tea.KeyPressMsg{Code: '-', Text: "-"})
	if got := s.(*graphScreen).depth; got != 1 {
		t.Fatalf("- below min set depth to %d, want 1", got)
	}
}

func TestGraphScreenDirectionCycles(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	initial := s.(*graphScreen).dirIx
	s, _ = s.Update(c, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if got := s.(*graphScreen).dirIx; got == initial {
		t.Fatal("d did not change direction")
	}
	// Cycle through remaining directions to wrap back
	dirs := graphDirections()
	for i := 1; i < len(dirs); i++ {
		s, _ = s.Update(c, tea.KeyPressMsg{Code: 'd', Text: "d"})
	}
	if s.(*graphScreen).dirIx != initial {
		t.Fatalf("direction did not wrap after a full cycle, got %d want %d", s.(*graphScreen).dirIx, initial)
	}
}

func TestGraphScreenWithQueryError(t *testing.T) {
	c := newLoadedContext(t)
	// Build through newGraphScreen with a ref the query cannot resolve
	ref := fleet.EntityRef{Kind: fleet.KindService, Key: "nonexistent", Label: "nonexistent"}
	s := newGraphScreen(c, ref)
	g := s.(*graphScreen)
	// The query must have failed during refresh
	if g.loadErr == nil {
		t.Fatal("expected loadErr to be set for nonexistent entity")
	}
	// The view must surface the failure
	out := g.View(c)
	if !strings.Contains(out, "neighborhood query failed") {
		t.Fatalf("query error not shown:\n%s", out)
	}
	// The body must be cleared on error
	if g.body != "" {
		t.Fatalf("body should be empty on error, got %q", g.body)
	}
}

func TestGraphScreenBarWithNilNeighborhood(t *testing.T) {
	g := &graphScreen{nb: nil}
	if got := g.bar(); got != "" {
		t.Fatalf("bar() with nil neighborhood = %q, want empty", got)
	}
}

func TestGraphScreenBarShowsTruncated(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	// Build a screen with a manually-constructed truncated neighborhood
	g := &graphScreen{
		ref:   ref,
		vp:    viewport.New(),
		depth: fleet.DefaultNeighborhoodDepth,
		nb: &fleet.Neighborhood{
			Nodes:     []fleet.NeighborhoodNode{{Ref: ref, Focus: true}},
			Truncated: true,
		},
		body: "test",
	}
	out := g.View(c)
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncated warning not shown:\n%s", out)
	}
}

func TestGraphScreenSelected(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	got, ok := s.(*graphScreen).selected()
	if !ok {
		t.Fatal("selected() returned false")
	}
	if got.Key != ref.Key {
		t.Fatalf("selected() = %+v, want %+v", got, ref)
	}
}

func TestGraphScreenTitle(t *testing.T) {
	c := newLoadedContext(t)
	ref := fleet.EntityRef{Kind: fleet.KindService, Key: "svc", Label: "Service Label"}
	s := newGraphScreen(c, ref)
	title := s.Title()
	if !strings.Contains(title, "Service Label") {
		t.Fatalf("Title() = %q, want it to contain the label", title)
	}
}

func TestGraphScreenPassesUnhandledKeysToViewport(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	g := s.(*graphScreen)
	// Press an unhandled key that falls through to vp.Update
	s, _ = s.Update(c, tea.KeyPressMsg{Code: 'x', Text: "x"})
	// The fallthrough must return the same screen pointer, not a replacement
	if s.(*graphScreen) != g {
		t.Fatal("screen was replaced instead of updated in place")
	}
}

func TestGraphScreenResizeWithSmallHeight(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	g := s.(*graphScreen)
	// Set a very small height that would result in h < 3 before the clamp
	c.Height = 2
	g.resize(c)
	// The viewport height should be clamped to at least 3
	if got := g.vp.Height(); got != 3 {
		t.Fatalf("viewport height = %d, want 3 (clamped)", got)
	}
}
