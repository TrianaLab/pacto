package catalog

import (
	"slices"
	"testing"
)

// A revision reached by two routes re-declares the same dependencies. Those are
// the same declarations, not new ones, so neither the edge nor the gap is
// duplicated -- while both routes stay on the revision.
func TestASecondRouteDoesNotDuplicateEdgesOrGaps(t *testing.T) {
	r, a, b, x, leaf := ociID("d-r"), ociID("d-a"), ociID("d-b"), ociID("d-x"), ociID("d-leaf")
	f := newFake().
		ok("reg/r:1", rev(r, ct("r", "1.0.0", dep("a", "reg/a:1"), dep("b", "reg/b:1")))).
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("x", "reg/x:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0", dep("x", "reg/x:1")))).
		ok("reg/x:1", rev(x, ct("x", "1.0.0", dep("leaf", "reg/leaf:1"), dep("gone", "reg/gone:1")))).
		ok("reg/leaf:1", rev(leaf, ct("leaf", "1.0.0"))).
		fail("reg/gone:1", ReasonNotFound)

	c := build(t, f, Bounds{}, "reg/r:1")

	if got := edgeTargets(c, x, 0); !slices.Equal(got, []ContentID{leaf}) {
		t.Errorf("x's leaf edge = %v, want it recorded exactly once", got)
	}
	if n := len(c.Unresolved()); n != 1 {
		t.Errorf("unresolved = %d, want one gap: the same declaration failing twice is one gap", n)
	}
	if paths := mustRevision(t, c, x).Paths; len(paths) != 2 {
		t.Errorf("x paths = %+v, want both routes even though its declarations are shared", paths)
	}
	if got := f.countFor("reg/gone:1"); got != 1 {
		t.Errorf("resolver calls for the failing reference = %d, want 1: a failure is memoized too", got)
	}
}

func TestTheEdgeBoundAlsoStopsEdgesThatCostNoResolution(t *testing.T) {
	r, x := ociID("eb-r"), ociID("eb-x")
	f := newFake().
		ok("reg/r:1", rev(r, ct("r", "1.0.0", dep("first", "reg/x:1"), dep("second", "reg/x:1")))).
		ok("reg/x:1", rev(x, ct("x", "1.0.0")))

	c := build(t, f, Bounds{MaxEdges: 1}, "reg/r:1")

	if f.count() != 2 {
		t.Errorf("resolver calls = %d, want 2: the second declaration is a memo hit", f.count())
	}
	if n := len(c.Edges()); n != 1 {
		t.Errorf("edges = %d, want the bound respected", n)
	}
	if !hasLimitation(c, LimitationEdgeLimit) || c.Meta().Completeness != CompletenessPartial {
		t.Errorf("meta = %+v, want a partial answer naming the edge bound", c.Meta())
	}
}

func TestTheUnresolvedBoundCapsReportingWithoutHidingThatItDid(t *testing.T) {
	r := ociID("ub-r")
	f := newFake().ok("reg/r:1", rev(r, ct("r", "1.0.0",
		dep("g0", "reg/g0:1"), dep("g1", "reg/g1:1"), dep("g2", "reg/g2:1"))))

	c := build(t, f, Bounds{MaxUnresolved: 1}, "reg/r:1")

	if n := len(c.Unresolved()); n != 1 {
		t.Errorf("unresolved = %d, want the list capped at the bound", n)
	}
	if !hasLimitation(c, LimitationUnresolvedLimit) {
		t.Errorf("limitations = %+v, want the reporting cap declared", c.Meta().Limitations)
	}
	// The bound is on reporting, so the healthy work still happened: all three
	// were attempted rather than abandoned because the first two failed.
	if f.count() != 4 {
		t.Errorf("resolver calls = %d, want 4: a reporting cap must not stop resolution", f.count())
	}
}

func TestTheConflictBoundCapsReportingAndSaysSo(t *testing.T) {
	f := newFake().
		ok("reg/a:1", rev(ociID("cb-a1"), ct("a", "1.0.0"))).
		ok("reg/a:2", rev(ociID("cb-a2"), ct("a", "2.0.0"))).
		ok("reg/b:1", rev(ociID("cb-b1"), ct("b", "1.0.0"))).
		ok("reg/b:2", rev(ociID("cb-b2"), ct("b", "2.0.0")))

	full := build(t, f, Bounds{}, "reg/a:1", "reg/a:2", "reg/b:1", "reg/b:2")
	if n := len(full.Conflicts()); n != 2 {
		t.Fatalf("baseline conflicts = %d, want 2", n)
	}

	c := build(t, f, Bounds{MaxConflicts: 1}, "reg/a:1", "reg/a:2", "reg/b:1", "reg/b:2")
	if n := len(c.Conflicts()); n != 1 {
		t.Errorf("conflicts = %d, want the list capped at the bound", n)
	}
	if !hasLimitation(c, LimitationConflictLimit) {
		t.Errorf("limitations = %+v, want the reporting cap declared", c.Meta().Limitations)
	}
}

func TestTheLimitationBoundLeavesRoomForItsOwnMarker(t *testing.T) {
	f := newFake()

	c := build(t, f, Bounds{MaxLimitations: 2}, "reg/x:1", "reg/y:1", "reg/z:1")

	limits := c.Meta().Limitations
	if len(limits) != 2 {
		t.Fatalf("limitations = %+v, want exactly the bound", limits)
	}
	if limits[0].Code != LimitationLimitationLimit {
		t.Errorf("limitations = %+v, want the overflow marker present", limits)
	}
	if limits[1].Code != LimitationRootUnresolved {
		t.Errorf("limitations = %+v, want a real limitation retained alongside it", limits)
	}
	// Every root is still reported with its own reason: the cap is on the
	// limitation list, never on the per-root truth.
	for _, r := range c.Roots() {
		if r.Resolved || r.Reason.Code != ReasonNotFound {
			t.Errorf("root %+v lost its reason to the limitation cap", r)
		}
	}
}

func TestOneReferenceRequestedTwiceIsOneLimitation(t *testing.T) {
	f := newFake()

	c := build(t, f, Bounds{}, "reg/x:1", "reg/x:1")

	if f.count() != 1 {
		t.Errorf("resolver calls = %d, want 1: the same failing reference is resolved once", f.count())
	}
	if n := len(c.Meta().Limitations); n != 1 {
		t.Errorf("limitations = %+v, want one", c.Meta().Limitations)
	}
	if roots := c.Roots(); len(roots) != 2 || roots[0].Reason.Code != roots[1].Reason.Code {
		t.Errorf("roots = %+v, want both reported with the same reason", roots)
	}
}

func TestOneLoopFoundFromTwoRootsIsOneCycle(t *testing.T) {
	r1, r2, a, b := ociID("c-r1"), ociID("c-r2"), ociID("c-a"), ociID("c-b")
	f := newFake().
		ok("reg/r1:1", rev(r1, ct("r1", "1.0.0", dep("a", "reg/a:1")))).
		ok("reg/r2:1", rev(r2, ct("r2", "1.0.0", dep("a", "reg/a:1")))).
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("b", "reg/b:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0", dep("a", "reg/a:1"))))

	c := build(t, f, Bounds{}, "reg/r1:1", "reg/r2:1")

	if n := len(c.Cycles()); n != 1 {
		t.Fatalf("cycles = %+v, want one: the same loop reached twice is one loop", c.Cycles())
	}
	if got := mustRevision(t, c, a).Roots; !slices.Equal(got, []RootID{0, 1}) {
		t.Errorf("a roots = %v, want both entry points retained", got)
	}
}

func TestSeparateLoopsAreSeparateCycles(t *testing.T) {
	r, a, b, x, y := ociID("s-r"), ociID("s-a"), ociID("s-b"), ociID("s-x"), ociID("s-y")
	f := newFake().
		ok("reg/r:1", rev(r, ct("r", "1.0.0", dep("a", "reg/a:1"), dep("x", "reg/x:1")))).
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("b", "reg/b:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0", dep("a", "reg/a:1")))).
		ok("reg/x:1", rev(x, ct("x", "1.0.0", dep("y", "reg/y:1")))).
		ok("reg/y:1", rev(y, ct("y", "1.0.0", dep("x", "reg/x:1"))))

	c := build(t, f, Bounds{}, "reg/r:1")

	cycles := c.Cycles()
	if len(cycles) != 2 {
		t.Fatalf("cycles = %+v, want two independent loops", cycles)
	}
	if !slices.IsSortedFunc(cycles, func(p, q Cycle) int {
		return slices.CompareFunc(p.Contents, q.Contents, compareContentID)
	}) {
		t.Errorf("cycles are not deterministically ordered: %+v", cycles)
	}
	for _, cy := range cycles {
		if compareContentID(cy.Contents[0], cy.Contents[1]) > 0 {
			t.Errorf("cycle %+v is not rotated to its smallest identity", cy.Contents)
		}
	}
}
