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

// TestGraphScreenDirectionCycles asserts the sequence, not just that the index
// moved and came back. "It changed and it wrapped" is equally true of a cycle
// running backwards, so it left tab and shift+tab free to swap: the help
// promises tab goes both, dependencies, dependents, in that order.
func TestGraphScreenDirectionCycles(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	if got := s.(*graphScreen).dirIx; got != 0 {
		t.Fatalf("a fresh graph opens at dirIx %d, want 0 (%s)", got, graphDirections()[0])
	}
	dirs := graphDirections()
	for _, want := range []int{1, 2, 0} {
		s, _ = s.Update(c, tea.KeyPressMsg{Code: '\t', Text: "tab"})
		if got := s.(*graphScreen).dirIx; got != want {
			t.Fatalf("tab moved to dirIx %d (%s), want %d (%s)", got, dirs[got], want, dirs[want])
		}
	}
}

// TestGraphScreenBarSeparatesRequestedDepthFromEvaluatedDepth pins the second
// number in the bar. A target projection is always one hop however deep the
// reader asked, so printing the request twice would report a neighborhood the
// query never walked.
func TestGraphScreenBarSeparatesRequestedDepthFromEvaluatedDepth(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	g := &graphScreen{
		ref:   ref,
		vp:    viewport.New(),
		depth: 2,
		nb: &fleet.Neighborhood{
			Nodes:          []fleet.NeighborhoodNode{{Ref: ref, Focus: true}},
			EffectiveDepth: 1,
		},
	}
	if got := g.bar(); !strings.Contains(got, "depth 2 (evaluated 1)") {
		t.Fatalf("bar = %q, want it to say depth 2 (evaluated 1)", got)
	}
}

// TestGraphScreenShiftTabCyclesBack makes the documented row true: the guide
// says tab and shift+tab cycle "the kind tabs, or the graph direction", and
// direction used to cycle forward only.
func TestGraphScreenShiftTabCyclesBack(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)
	initial := s.(*graphScreen).dirIx

	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	want := len(graphDirections()) - 1
	if got := s.(*graphScreen).dirIx; got != want {
		t.Fatalf("shift+tab from %d left dirIx at %d, want a wrap to %d", initial, got, want)
	}
	s, _ = s.Update(c, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if got := s.(*graphScreen).dirIx; got != want-1 {
		t.Fatalf("the second shift+tab left dirIx at %d, want %d", got, want-1)
	}
}

// TestGraphScreenRefusesToOpenItself pins A16. A graph screen's selection is
// its own root, so g used to push an identical screen: nothing visibly changed
// and q then had to be pressed once per accidental press.
func TestGraphScreenRefusesToOpenItself(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)
	s := newGraphScreen(c, ref)

	next, cmd := s.Update(c, tea.KeyPressMsg{Code: 'g', Text: "g"})
	if next != s {
		t.Fatalf("g replaced the screen with %T", next)
	}
	if cmd == nil {
		t.Fatal("g produced no command, want a status saying the graph is already open")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("g produced %T, want a statusMsg rather than a second graph screen", cmd())
	}
	if !strings.Contains(msg.text, label(ref)) {
		t.Fatalf("status = %q, want it to name the entity already on screen", msg.text)
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
