package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// --- layout -----------------------------------------------------------------

// TestListColumnsAlwaysReturnsFourCells is the contract rows() depends on: a
// hidden column is width 0, never a missing cell, because the bubbles table
// skips a zero-width column when it renders but still indexes rows positionally.
func TestListColumnsAlwaysReturnsFourCells(t *testing.T) {
	for _, kind := range []bool{true, false} {
		for _, detail := range []bool{true, false} {
			cols := listColumns(100, kind, detail)
			if len(cols) != 4 {
				t.Fatalf("listColumns(100,%v,%v) returned %d columns, want 4", kind, detail, len(cols))
			}
			if got := cols[0].Width > 0; got != kind {
				t.Errorf("showKind=%v gave KIND width %d", kind, cols[0].Width)
			}
			if got := cols[3].Width > 0; got != detail {
				t.Errorf("showDetail=%v gave DETAIL width %d", detail, cols[3].Width)
			}
			if cols[1].Width < minNameWidth {
				t.Errorf("NAME is %d cells, want at least %d", cols[1].Width, minNameWidth)
			}
			if cols[2].Width != statusColWidth {
				t.Errorf("STATUS is %d cells, want the fixed %d", cols[2].Width, statusColWidth)
			}
		}
	}
}

// TestListColumnsFloorsTheNameWidth covers the branch a terminal narrower than
// the fixed columns takes: NAME stops shrinking rather than going negative and
// panicking the renderer.
func TestListColumnsFloorsTheNameWidth(t *testing.T) {
	for _, tw := range []int{0, 10, 30} {
		cols := listColumns(tw, true, true)
		if cols[1].Width != minNameWidth-minNameWidth*2/5 {
			t.Errorf("at %d cells NAME is %d, want the floor split", tw, cols[1].Width)
		}
		if cols[1].Width+cols[3].Width != minNameWidth {
			t.Errorf("at %d cells the flex columns sum to %d, want %d",
				tw, cols[1].Width+cols[3].Width, minNameWidth)
		}
	}
}

func TestPaneWidth(t *testing.T) {
	for _, tt := range []struct{ total, want int }{
		{0, 0}, {75, 0}, // too narrow to split at all
		{76, 25}, {90, 30}, // a third of the frame
		{120, 40}, {400, 40}, // capped, so the list keeps the space on a wide screen
	} {
		if got := paneWidth(tt.total); got != tt.want {
			t.Errorf("paneWidth(%d) = %d, want %d", tt.total, got, tt.want)
		}
	}
}

func TestPadLines(t *testing.T) {
	if got := padLines("a\nb", 5); strings.Count(got, "\n") != 4 {
		t.Errorf("padLines grew to %q, want 5 lines", got)
	}
	// It never shrinks: clipping a pane would cut its border and open the frame.
	if got := padLines("a\nb\nc", 2); got != "a\nb\nc" {
		t.Errorf("padLines shrank to %q", got)
	}
	if got := padLines("", 1); got != "" {
		t.Errorf("padLines(%q,1) = %q, want it untouched", "", got)
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("payments-api", 40); got != "payments-api" {
		t.Errorf("trunc = %q, want it whole", got)
	}
	if got := lipgloss.Width(trunc("payments-api-europe-west", 8)); got > 8 {
		t.Errorf("trunc left %d cells, want at most 8", got)
	}
	// A multi-byte name is cut on a rune boundary, not a byte one.
	if got := trunc("pagos-españa", 8); !strings.ContainsRune(got, 'p') || lipgloss.Width(got) > 8 {
		t.Errorf("trunc = %q, want a clean 8-cell cut", got)
	}
	for _, w := range []int{0, -3} {
		if got := trunc("anything", w); got != "" {
			t.Errorf("trunc at width %d = %q, want empty", w, got)
		}
	}
}

// --- the list screen's empty states ------------------------------------------

// TestListEmptyWhenTheFilterMatchesNothing is the first of the three: the fleet
// has data, the reader's own filter is why the grid is blank, so the way out is
// esc.
func TestListEmptyWhenTheFilterMatchesNothing(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.filterText = "no-such-service"
	l.refresh(c)

	out := l.View(c)
	if !strings.Contains(out, "Nothing matches") {
		t.Fatalf("view = %q, want the no-match sentence", out)
	}
	if !strings.Contains(out, "clear the filter") {
		t.Fatalf("view = %q, want the way out of the filter", out)
	}
}

// TestListEmptyWhenTheTabIsEmpty is the second: the fleet has data, this kind
// does not. The shared fixture fills every kind, so this needs its own -- a
// contract with nothing deployed, which is also the commonest real shape of it.
func TestListEmptyWhenTheTabIsEmpty(t *testing.T) {
	c := newContextOver(t, snapshotWithNoTargets(t))
	l := newListScreen(c).(*listScreen)
	for i, k := range l.kinds() {
		if k == fleet.KindTarget {
			l.kindIx = i
		}
	}
	l.refresh(c)
	if l.total != 0 {
		t.Fatalf("the targets tab has %d rows, want none", l.total)
	}

	out := l.View(c)
	if !strings.Contains(out, "No targets in this snapshot") {
		t.Fatalf("view = %q, want the empty-tab sentence", out)
	}
	if !strings.Contains(out, "next kind") {
		t.Fatalf("view = %q, want the way to another tab", out)
	}
}

// TestListEmptyFleetNamesEverySourceConsulted is the third and the one the bug
// report was about. A blank grid with "0 entities" under it is indistinguishable
// from a broken program, so this case has to name what was looked at, what came
// back, how the session was launched and what to run instead.
func TestListEmptyFleetNamesEverySourceConsulted(t *testing.T) {
	snap := emptySnapshot(t)
	c := newContextOver(t, snap)
	c.SourceArgs = []string{"--local=/tmp/nothing-here"}
	l := newListScreen(c).(*listScreen)

	out := l.View(c)
	for _, want := range []string{
		"This snapshot is empty",
		"sources consulted",
		"memory",                    // the source kind
		"test",                      // the source id
		"0 revisions, 0 targets",    // what it actually returned
		"--local=/tmp/nothing-here", // how this session was launched
		"pacto tui --local",         // and three things to try
		"pacto tui --oci",
		"pacto tui --namespace",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the empty-fleet screen never says %q:\n%s", want, out)
		}
	}
}

// TestListEmptyFleetWithNoSourcesAtAll covers the branch where not even a source
// was configured, which must read as "none" rather than as an absent section.
func TestListEmptyFleetWithNoSourcesAtAll(t *testing.T) {
	c := newContextOver(t, emptySnapshot(t))
	l := newListScreen(c).(*listScreen)
	c.Snapshot = &fleet.FleetSnapshot{}

	out := l.emptyFleet(c)
	if !strings.Contains(out, "sources consulted") || !strings.Contains(out, "none") {
		t.Fatalf("emptyFleet = %q, want it to say no source was consulted", out)
	}
}

// TestListEmptyFleetShowsASourceError checks the error a source reported reaches
// the screen: "nothing here" and "the registry rejected your credentials" are
// very different problems and only one of them is the reader's fault.
func TestListEmptyFleetShowsASourceError(t *testing.T) {
	c := newContextOver(t, emptySnapshot(t))
	l := newListScreen(c).(*listScreen)
	c.Snapshot = &fleet.FleetSnapshot{Sources: []fleet.SourceState{{
		ID:     "oci-0",
		Kind:   "oci",
		Status: fleet.SourceUnavailable,
		Error:  &fleet.SourceError{Message: "the registry rejected the available credentials"},
	}}}

	out := l.emptyFleet(c)
	if !strings.Contains(out, "the registry rejected the available credentials") {
		t.Fatalf("emptyFleet = %q, want the source error shown", out)
	}
	if !strings.Contains(out, statusDot(string(fleet.SourceUnavailable))) {
		t.Fatalf("emptyFleet = %q, want the source status marked", out)
	}
}

// --- the detail pane ----------------------------------------------------------

// TestListPaneDescribesTheHighlightedRow covers the populated pane: the fields
// an EntityRef carries that the table has never had room for.
func TestListPaneDescribesTheHighlightedRow(t *testing.T) {
	c := newLoadedContext(t)
	c.Width = 120
	l := newListScreen(c).(*listScreen)
	l.resize(c)

	ref, ok := l.selected()
	if !ok {
		t.Fatal("the fixture list is empty")
	}
	body := l.paneBody(c, paneWidth(c.Width)-4)
	if !strings.Contains(body, ref.Label) {
		t.Errorf("the pane does not name the highlighted row %q:\n%s", ref.Label, body)
	}
	if !strings.Contains(body, string(ref.Kind)) {
		t.Errorf("the pane does not say what kind it is:\n%s", body)
	}
	if !strings.Contains(body, "enter") {
		t.Errorf("the pane does not say what to press:\n%s", body)
	}
}

// TestListPaneWithNothingHighlighted covers the other branch, reached whenever
// the list is empty and the pane still has to draw something.
func TestListPaneWithNothingHighlighted(t *testing.T) {
	c := newContextOver(t, emptySnapshot(t))
	l := newListScreen(c).(*listScreen)
	if got := l.paneBody(c, 36); !strings.Contains(got, "Nothing highlighted") {
		t.Fatalf("paneBody = %q, want the empty-selection line", got)
	}
}

// TestListPaneShowsEveryOptionalField drives the field loop and the explanation
// block with a ref that carries all of them, which no fixture row does.
func TestListPaneShowsEveryOptionalField(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)
	l.entities = []fleet.EntityRef{{
		Kind:          fleet.KindTarget,
		Label:         "payments-api",
		Status:        fleet.StatusNonCompliant,
		Domain:        "ghcr.io/acme",
		Scope:         "production",
		ParentService: "payments-api",
		Version:       "2.1.0",
		Secondary:     "Deployment/payments-api",
		Explanation:   "the running digest does not match the contract",
	}}
	l.tbl.SetRows(l.rows(c))
	l.tbl.SetCursor(0)

	body := l.paneBody(c, 40)
	for _, want := range []string{
		"domain", "ghcr.io/acme",
		"scope", "production",
		"version", "2.1.0",
		"why", "does not match",
		"Non-compliant",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the pane never says %q:\n%s", want, body)
		}
	}
}

// --- split, animation and the clock -------------------------------------------

// TestListSplitDropsThePaneWhenNarrow covers both sides of the layout switch.
func TestListSplitDropsThePaneWhenNarrow(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)

	c.Width = 120
	l.resize(c)
	wide := l.split(c, 10)
	if lipgloss.Width(wide) <= 100 {
		t.Errorf("the split is %d cells wide at 120 columns, want the pane beside the table",
			lipgloss.Width(wide))
	}

	c.Width = 70
	l.resize(c)
	narrow := l.split(c, 10)
	if lipgloss.Width(narrow) > 70 {
		t.Errorf("the unsplit body is %d cells wide at 70 columns", lipgloss.Width(narrow))
	}
}

// TestListSplitRepaintsPulsingRows is why split re-renders rows: a table row is
// a plain string once built, so a badge drawn at last frame's brightness would
// never change again.
func TestListSplitRepaintsPulsingRows(t *testing.T) {
	c := newLoadedContext(t)
	c.Anim = true
	l := newListScreen(c).(*listScreen)
	l.attention = true
	l.resize(c)

	first := l.split(c, 10)
	c.Frame = pulsePeriod / 2
	if second := l.split(c, 10); second == first {
		t.Fatal("a pulsing row rendered identically on a different frame")
	}

	// With nothing wrong the rows are left exactly as they were built.
	l.attention = false
	c.Frame = 0
	held := l.split(c, 10)
	c.Frame = pulsePeriod / 2
	if l.split(c, 10) != held {
		t.Fatal("a healthy fleet repainted its rows anyway")
	}
}

// TestListAnimatingStopsWhenThereIsNothingToShow walks every exit from the
// screen's animating predicate. It is load-bearing: a true here schedules a
// frame sixteen times a second forever.
func TestListAnimatingStopsWhenThereIsNothingToShow(t *testing.T) {
	c := newLoadedContext(t)
	l := newListScreen(c).(*listScreen)

	if l.animating(c) {
		t.Error("motion is off, so the screen must not animate")
	}

	c.Anim = true
	if !l.animating(c) {
		t.Error("the fixture has a non-compliant row, so it should throb")
	}

	// Past the count-up, with nothing needing attention, the clock stops.
	l.attention = false
	l.banner.attention = 0
	c.Frame = l.banner.startFrame + framesFor(countUpDuration) + 1
	if l.animating(c) {
		t.Error("a settled, healthy screen must stop the clock")
	}

	// The count-up alone keeps it going while it runs.
	c.Frame = l.banner.startFrame
	if !l.animating(c) {
		t.Error("the headline is still counting up, so the clock must run")
	}
}

// TestModelClockStopsItself is the same property at the root: the frame chain
// re-arms only while something moves, so an idle TUI schedules no work at all.
func TestModelClockStopsItself(t *testing.T) {
	m := newTestModel(t)
	m.ctx.Anim = true
	m.stack = []screen{stubScreen{}}
	m.transitionStart = m.ctx.Frame

	// Mid-transition the chain continues.
	next, cmd := m.Update(frameMsg{})
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("the clock stopped during a transition")
	}
	if m.ctx.Frame != 1 {
		t.Fatalf("Frame = %d after one tick, want 1", m.ctx.Frame)
	}

	// Past it, with a screen that has no motion, it stops.
	m.ctx.Frame = transitionFrames + 1
	next, cmd = m.Update(frameMsg{})
	m = next.(*Model)
	if cmd != nil {
		t.Fatal("the clock kept running with nothing to animate")
	}
	if m.ticking {
		t.Fatal("the model still believes the clock is running")
	}
}

// TestModelArmTickIsIdempotent guards the other half: a second arm while one is
// already in flight must not start a second chain, or the frame rate doubles
// every time a screen is pushed.
func TestModelArmTickIsIdempotent(t *testing.T) {
	m := newTestModel(t)
	m.ctx.Anim = true
	m.stack = []screen{loadingScreen{}}

	if cmd := m.armTick(); cmd == nil {
		t.Fatal("armTick did not start the clock")
	}
	if cmd := m.armTick(); cmd != nil {
		t.Fatal("armTick started a second clock")
	}

	// And with animation off it never starts at all.
	m2 := newTestModel(t)
	m2.stack = []screen{loadingScreen{}}
	if cmd := m2.armTick(); cmd != nil {
		t.Fatal("armTick started a clock with animation disabled")
	}
	if got := m2.transitionProgress(); got != 1 {
		t.Fatalf("transitionProgress with animation off = %v, want 1", got)
	}
	if m2.animating() {
		t.Fatal("a model with animation off reported motion")
	}
}

// TestModelBeginTransitionRestartsTheWipe checks a push does not inherit the
// previous screen's finished progress, which would show the new screen whole.
func TestModelBeginTransitionRestartsTheWipe(t *testing.T) {
	m := newTestModel(t)
	m.ctx.Anim = true
	m.ctx.Frame = 100
	m.stack = []screen{stubScreen{}}

	if got := m.transitionProgress(); got != 1 {
		t.Fatalf("progress before the transition = %v, want a settled 1", got)
	}
	m.beginTransition()
	if got := m.transitionProgress(); got != 0 {
		t.Fatalf("progress just after beginTransition = %v, want 0", got)
	}
	m.ctx.Frame += transitionFrames
	if got := m.transitionProgress(); got != 1 {
		t.Fatalf("progress after the whole duration = %v, want 1", got)
	}
}

// --- theme --------------------------------------------------------------------

func TestStatusDotAndBadge(t *testing.T) {
	if got := statusDot(fleet.StatusCompliant); !strings.Contains(got, glyphDot) {
		t.Errorf("statusDot = %q, want the dot glyph", got)
	}
	if got := statusDot(fleet.StatusReference); !strings.Contains(got, glyphRing) {
		t.Errorf("a reference should draw the hollow glyph, got %q", got)
	}

	badge := statusBadge(fleet.StatusNonCompliant)
	if !strings.Contains(badge, glyphDot) || !strings.Contains(badge, "Non-compliant") {
		t.Errorf("statusBadge = %q, want the glyph and the words", badge)
	}
	// A target with no verdict gets no badge at all rather than a bare glyph.
	if got := statusBadge(""); got != "" {
		t.Errorf("statusBadge(%q) = %q, want nothing", "", got)
	}
}

// TestPulseStyleOnlyThrobsConfirmedProblems covers both guards and the pulse
// itself: the colour has to actually change between frames, or the whole clock
// is running for nothing.
func TestPulseStyleOnlyThrobsConfirmedProblems(t *testing.T) {
	c := &Context{}
	if pulseStyle(c, fleet.StatusInvalid).String() != statusStyle(fleet.StatusInvalid).String() {
		t.Error("with animation off a status must render in its plain style")
	}

	c.Anim = true
	if pulseStyle(c, fleet.StatusCompliant).String() != statusStyle(fleet.StatusCompliant).String() {
		t.Error("a compliant status must not pulse")
	}

	c.Frame = 0
	dark := pulseStyle(c, fleet.StatusInvalid).String()
	c.Frame = pulsePeriod / 2
	bright := pulseStyle(c, fleet.StatusInvalid).String()
	if dark == bright {
		t.Error("the pulse rendered the same colour at the bottom and the top of the beat")
	}
	if bright != statusStyle(fleet.StatusInvalid).String() {
		t.Error("the top of the beat should be the status's full brightness")
	}
}

func TestDimColor(t *testing.T) {
	r, g, b, _ := dimColor(colRed, 0).RGBA()
	if r|g|b != 0 {
		t.Errorf("dimColor at 0 = (%d,%d,%d), want black", r, g, b)
	}
	full, _, _, _ := dimColor(colRed, 1).RGBA()
	orig, _, _, _ := colRed.RGBA()
	if full != orig {
		t.Errorf("dimColor at 1 = %d, want the original %d", full, orig)
	}
	// Out of range is clamped rather than wrapping a uint8 round the houses.
	if a, _, _, _ := dimColor(colRed, 9).RGBA(); a != orig {
		t.Errorf("dimColor at 9 = %d, want it clamped to %d", a, orig)
	}
	if a, _, _, _ := dimColor(colRed, -9).RGBA(); a != 0 {
		t.Errorf("dimColor at -9 = %d, want it clamped to 0", a)
	}
}

func TestHintsPairsKeysWithDescriptions(t *testing.T) {
	got := hints("enter", "open", "q", "quit")
	for _, want := range []string{"enter", "open", "q", "quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("hints = %q, want it to contain %q", got, want)
		}
	}
	if hints() != "" {
		t.Errorf("hints() = %q, want nothing", hints())
	}
	// An odd tail has no description, so it is dropped rather than rendered
	// against an empty string.
	if strings.Contains(hints("enter", "open", "dangling"), "dangling") {
		t.Error("hints rendered a key with no description")
	}
}

// TestPactoTableKeyMapReleasesTheVerbLetters is the collision fix: d, g and G
// belong to diff, graph and generate, and dispatchVerb runs before the table
// ever sees a key.
func TestPactoTableKeyMapReleasesTheVerbLetters(t *testing.T) {
	k := pactoTableKeyMap()
	for _, tt := range []struct {
		name string
		keys []string
	}{
		{"half page down", k.HalfPageDown.Keys()},
		{"half page up", k.HalfPageUp.Keys()},
		{"goto top", k.GotoTop.Keys()},
		{"goto bottom", k.GotoBottom.Keys()},
	} {
		for _, key := range tt.keys {
			if key == "d" || key == "u" || key == "g" || key == "G" {
				t.Errorf("%s is still bound to %q, which pacto's verbs own", tt.name, key)
			}
		}
	}
	// The arrows and the named keys still work, which is what the hint bar
	// promises the reader.
	if len(k.LineUp.Keys()) == 0 || len(k.LineDown.Keys()) == 0 {
		t.Fatal("the table lost its line movement keys")
	}
}

// --- the graph traversal --------------------------------------------------------

// TestGraphWalkRevealsTheTreeRootOutward covers the animated traversal: the tree
// is already rendered root first, so revealing it line by line is the walk.
func TestGraphWalkRevealsTheTreeRootOutward(t *testing.T) {
	c := newLoadedContext(t)
	ref := firstEntityOfKind(t, c, fleet.KindService)

	g := newGraphScreen(c, ref).(*graphScreen)
	if g.walk(c) != g.body {
		t.Fatal("with animation off the whole tree is drawn at once")
	}
	if g.animating(c) {
		t.Fatal("with animation off there is nothing to animate")
	}

	c.Anim = true
	g.refresh(c)
	if got := g.walkProgress(c); got != 0 {
		t.Fatalf("a refresh must restart the walk, progress = %v", got)
	}
	if !g.animating(c) {
		t.Fatal("a walk that has not finished is still animating")
	}
	if partial := g.walk(c); partial == g.body {
		t.Fatal("the first frame drew the finished tree")
	}
	if n, want := strings.Count(g.walk(c), "\n"), strings.Count(g.body, "\n"); n != want {
		t.Fatalf("the partial walk has %d newlines, want the tree's %d", n, want)
	}

	c.Frame = g.walkStart + framesFor(walkDuration)
	if g.walk(c) != g.body {
		t.Fatal("the finished walk is not the whole tree")
	}
	if g.animating(c) {
		t.Fatal("a finished walk is still reporting motion")
	}
}

// --- helpers --------------------------------------------------------------------

// newTestModel is a root Model over the fixture snapshot, past the loading
// screen, for the tests that drive the clock rather than a screen.
func newTestModel(t *testing.T) *Model {
	t.Helper()
	c := newLoadedContext(t)
	return &Model{ctx: c, stack: []screen{newListScreen(c)}}
}

// snapshotWithNoTargets is a contract that was never deployed: revisions and
// services and an owner, and nothing running. It is the one shape the shared
// fixture cannot produce, because that one deploys everything it declares.
func snapshotWithNoTargets(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{
		Bundle: &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.0",
			Service: contract.Service{
				Name:    "undeployed-svc",
				Version: "1.0.0",
				Owner:   contract.Owner{Team: "platform"},
			},
		}},
		RequestedRef: "file:///tmp/undeployed-svc",
		Digest:       testDigest,
	}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return snap
}
