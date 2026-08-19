package catalog

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Case 1 -- two independent roots, each with its own direct dependency. Nothing
// is shared, so nothing may be merged.
func TestTwoIndependentRootsKeepTheirOwnDependencies(t *testing.T) {
	a, b, da, db := at("a", "a"), at("b", "b"), at("da", "da"), at("db", "db")
	f := newFake().
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("da", "reg/da:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0", dep("db", "reg/db:1")))).
		ok("reg/da:1", rev(da, ct("da", "1.0.0"))).
		ok("reg/db:1", rev(db, ct("db", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/a:1", "reg/b:1")

	if got := len(c.Revisions()); got != 4 {
		t.Fatalf("revisions = %d, want 4", got)
	}
	if m := c.Meta(); m.Completeness != CompletenessComplete {
		t.Errorf("completeness = %q, want complete (limitations %v)", m.Completeness, m.Limitations)
	}
	for _, r := range c.Roots() {
		if !r.Resolved {
			t.Errorf("root %q did not resolve: %+v", r.RequestedRef, r.Reason)
		}
	}
	depA := mustRevision(t, c, da)
	if depA.Rank != RankDirect || depA.MinDepth != 1 {
		t.Errorf("da rank = %q depth = %d, want direct/1", depA.Rank, depA.MinDepth)
	}
	if !hasPath(depA, Path{Root: 0, Steps: []DeclarationID{{From: a, Index: 0}}}) {
		t.Errorf("da paths = %+v, want the single route through root 0", depA.Paths)
	}
	if depA.Shared() {
		t.Error("da is reachable from one root only; it must not report as shared")
	}
	depB := mustRevision(t, c, db)
	if !hasPath(depB, Path{Root: 1, Steps: []DeclarationID{{From: b, Index: 0}}}) {
		t.Errorf("db paths = %+v, want the single route through root 1", depB.Paths)
	}
	if len(c.Conflicts()) != 0 || len(c.Cycles()) != 0 || len(c.Unresolved()) != 0 {
		t.Errorf("independent roots produced conflicts/cycles/unresolved: %+v %+v %+v",
			c.Conflicts(), c.Cycles(), c.Unresolved())
	}
}

// Case 2 -- two roots reaching one immutable transitive revision. It is one
// revision, and both provenance paths survive.
func TestSharedTransitiveRevisionKeepsBothProvenancePaths(t *testing.T) {
	ra, rb, x, y, z := at("ra", "ra"), at("rb", "rb"), at("x", "x"), at("y", "y"), at("z", "z")
	f := newFake().
		ok("reg/ra:1", rev(ra, ct("ra", "1.0.0", dep("x", "reg/x:1")))).
		ok("reg/rb:1", rev(rb, ct("rb", "1.0.0", dep("y", "reg/y:1")))).
		ok("reg/x:1", rev(x, ct("x", "1.0.0", dep("z", "reg/z:1")))).
		ok("reg/y:1", rev(y, ct("y", "1.0.0", dep("z", "reg/z:1")))).
		ok("reg/z:1", rev(z, ct("z", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/ra:1", "reg/rb:1")

	shared := mustRevision(t, c, z)
	if n := len(c.Revisions()); n != 5 {
		t.Fatalf("revisions = %d, want 5 (z canonically deduplicated)", n)
	}
	if !shared.Shared() || !slices.Equal(shared.Roots, []RootID{0, 1}) {
		t.Errorf("z roots = %v, want both roots", shared.Roots)
	}
	if len(shared.Paths) != 2 {
		t.Fatalf("z paths = %+v, want both provenance routes", shared.Paths)
	}
	if !hasPath(shared, Path{Root: 0, Steps: []DeclarationID{{From: ra, Index: 0}, {From: x, Index: 0}}}) ||
		!hasPath(shared, Path{Root: 1, Steps: []DeclarationID{{From: rb, Index: 0}, {From: y, Index: 0}}}) {
		t.Errorf("z paths = %+v, want one route through each root", shared.Paths)
	}
	// The second route costs no work: "reg/z:1" is resolved exactly once.
	if got := f.countFor("reg/z:1"); got != 1 {
		t.Errorf("resolver calls for reg/z:1 = %d, want 1", got)
	}
}

// Case 3 -- a diamond. Both branches are facts about the revision at the bottom
// and both are preserved.
func TestDiamondPreservesEveryPath(t *testing.T) {
	root, left, right, bottom := at("root", "root"), at("left", "left"), at("right", "right"), at("bottom", "bottom")
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0", dep("left", "reg/left:1"), dep("right", "reg/right:1")))).
		ok("reg/left:1", rev(left, ct("left", "1.0.0", dep("bottom", "reg/bottom:1")))).
		ok("reg/right:1", rev(right, ct("right", "1.0.0", dep("bottom", "reg/bottom:1")))).
		ok("reg/bottom:1", rev(bottom, ct("bottom", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/root:1")

	b := mustRevision(t, c, bottom)
	if len(b.Paths) != 2 {
		t.Fatalf("bottom paths = %+v, want both diamond branches", b.Paths)
	}
	if !hasPath(b, Path{Root: 0, Steps: []DeclarationID{{From: root, Index: 0}, {From: left, Index: 0}}}) ||
		!hasPath(b, Path{Root: 0, Steps: []DeclarationID{{From: root, Index: 1}, {From: right, Index: 0}}}) {
		t.Errorf("bottom paths = %+v, want one route per branch", b.Paths)
	}
	if b.PathsTruncated {
		t.Error("both diamond branches fit inside the bounds; nothing should be truncated")
	}
	if len(edgeTargets(c, left, 0)) != 1 || len(edgeTargets(c, right, 0)) != 1 {
		t.Errorf("each branch declares the bottom once: %+v", c.Edges())
	}
}

// Case 4 -- a cycle terminates and stays visible.
func TestCycleTerminatesAndStaysVisible(t *testing.T) {
	r, a, b := at("r", "r"), at("a", "a"), at("b", "b")
	f := newFake().
		ok("reg/r:1", rev(r, ct("r", "1.0.0", dep("a", "reg/a:1")))).
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("b", "reg/b:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0", dep("a", "reg/a:1"))))

	c := build(t, f, Bounds{}, "reg/r:1")

	cycles := c.Cycles()
	if len(cycles) != 1 {
		t.Fatalf("cycles = %+v, want exactly one", cycles)
	}
	if len(cycles[0].Revisions) != 2 ||
		!slices.Contains(cycles[0].Revisions, a) || !slices.Contains(cycles[0].Revisions, b) {
		t.Errorf("cycle = %+v, want the a<->b loop", cycles[0].Revisions)
	}
	// The closing edge is still in the graph -- the loop is visible, not pruned.
	if got := edgeTargets(c, b, 0); !slices.Equal(got, []RevisionID{a}) {
		t.Errorf("closing edge b->a = %v, want it retained", got)
	}
	if got := len(c.Revisions()); got != 3 {
		t.Errorf("revisions = %d, want 3; the walk must terminate without duplicating the loop", got)
	}
}

// Case 5 -- an unresolved root and an unresolved transitive dependency are
// partial knowledge. Not empty, and certainly not complete.
func TestUnresolvedRootAndDependencyArePartialNotEmpty(t *testing.T) {
	ok, mid := at("ok", "ok"), at("mid", "mid")
	f := newFake().
		ok("reg/ok:1", rev(ok, ct("ok", "1.0.0", dep("mid", "reg/mid:1")))).
		ok("reg/mid:1", rev(mid, ct("mid", "1.0.0", dep("gone", "reg/gone:1")))).
		fail("reg/gone:1", ReasonNotFound)

	c := build(t, f, Bounds{}, "reg/broken:1", "reg/ok:1")

	m := c.Meta()
	if m.Completeness != CompletenessPartial {
		t.Fatalf("completeness = %q, want partial", m.Completeness)
	}
	if len(c.Revisions()) == 0 {
		t.Fatal("the resolvable part of the closure must survive; partial is not empty")
	}
	roots := c.Roots()
	if roots[0].Resolved || roots[0].Reason.Code != ReasonNotFound {
		t.Errorf("root 0 = %+v, want an unresolved root carrying its reason", roots[0])
	}
	if roots[0].RequestedRef != "reg/broken:1" {
		t.Errorf("root 0 ref = %q; an invalid root is reported, never dropped", roots[0].RequestedRef)
	}
	if !roots[1].Resolved {
		t.Errorf("root 1 = %+v, want the healthy root unaffected", roots[1])
	}
	un := c.Unresolved()
	if len(un) != 1 || un[0].Declaration != (DeclarationID{From: mid, Index: 0}) || un[0].Reason.Code != ReasonNotFound {
		t.Fatalf("unresolved = %+v, want the mid->gone declaration", un)
	}
	if un[0].Ref != "reg/gone:1" || un[0].Name != "gone" {
		t.Errorf("unresolved = %+v, want the declared name and reference preserved", un[0])
	}
	if !hasLimitation(c, LimitationRootUnresolved) || !hasLimitation(c, LimitationUnresolvedDep) {
		t.Errorf("limitations = %+v, want both gaps named", m.Limitations)
	}
}

// The positive arm of case 5: the same shape, fully resolvable, is complete and
// carries no gap. Without it, "partial" above could be the only answer the code
// can produce.
func TestFullyResolvableClosureIsComplete(t *testing.T) {
	ok, mid, leaf := at("ok", "ok"), at("mid", "mid"), at("leaf", "leaf")
	f := newFake().
		ok("reg/ok:1", rev(ok, ct("ok", "1.0.0", dep("mid", "reg/mid:1")))).
		ok("reg/mid:1", rev(mid, ct("mid", "1.0.0", dep("leaf", "reg/leaf:1")))).
		ok("reg/leaf:1", rev(leaf, ct("leaf", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/ok:1")

	m := c.Meta()
	if m.Completeness != CompletenessComplete {
		t.Fatalf("completeness = %q, want complete (limitations %+v)", m.Completeness, m.Limitations)
	}
	if len(m.Limitations) != 0 || len(c.Unresolved()) != 0 {
		t.Errorf("a complete catalog carries no limitation and no gap: %+v %+v", m.Limitations, c.Unresolved())
	}
	if m.RequestedRoots != 1 || m.SchemaVersion != SchemaVersion {
		t.Errorf("meta = %+v, want one requested root and the catalog schema", m)
	}
	if !m.GeneratedAt.Equal(testTime) {
		t.Errorf("generatedAt = %v, want the injected clock", m.GeneratedAt)
	}
}

// Case 6 -- one service name at one version, two different digests. Content is
// identity; a name and a version are not.
func TestSameNameAndVersionWithDifferentDigestsStayDistinct(t *testing.T) {
	one, two := at("api", "api-build-1"), at("api", "api-build-2")
	f := newFake().
		ok("reg/api:1.0.0", rev(one, ct("api", "1.0.0"))).
		ok("mirror/api:1.0.0", rev(two, ct("api", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/api:1.0.0", "mirror/api:1.0.0")

	if n := len(c.Revisions()); n != 2 {
		t.Fatalf("revisions = %d, want 2; different bytes are different revisions", n)
	}
	conflicts := c.Conflicts()
	if len(conflicts) != 1 || conflicts[0].Kind != ConflictContent {
		t.Fatalf("conflicts = %+v, want one content conflict", conflicts)
	}
	if !slices.Equal(conflicts[0].Revisions, sortedRevisions(map[RevisionID]bool{one: true, two: true})) {
		t.Errorf("conflict revisions = %+v, want both digests named", conflicts[0].Revisions)
	}
	if conflicts[0].Service.Name != "api" || conflicts[0].Version != "1.0.0" {
		t.Errorf("conflict = %+v, want it attributed to api 1.0.0", conflicts[0])
	}
}

// Case 7 -- a tag and a digest pin naming one content. One revision, both
// requested references kept.
func TestTagAndDigestForOneContentDeduplicateButKeepBothReferences(t *testing.T) {
	p := at("api", "pinned")
	pinned := "reg/api@" + p.Content.Digest
	f := newFake().
		ok("reg/api:1.0.0", rev(p, ct("api", "1.0.0"))).
		ok(pinned, rev(p, ct("api", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/api:1.0.0", pinned)

	if n := len(c.Revisions()); n != 1 {
		t.Fatalf("revisions = %d, want 1; the same bytes are one revision", n)
	}
	r := mustRevision(t, c, p)
	if !slices.Equal(r.RequestedRefs, []string{"reg/api:1.0.0", pinned}) {
		t.Errorf("requestedRefs = %v, want both references preserved", r.RequestedRefs)
	}
	if !slices.Equal(r.Roots, []RootID{0, 1}) || len(r.Paths) != 2 {
		t.Errorf("provenance = roots %v paths %+v, want both roots visible", r.Roots, r.Paths)
	}
	if len(c.Conflicts()) != 0 {
		t.Errorf("one content reached twice is not a conflict: %+v", c.Conflicts())
	}
	for _, root := range c.Roots() {
		if root.Revision != p {
			t.Errorf("root %q resolved to %+v, want the shared revision", root.RequestedRef, root.Revision)
		}
	}
}

// revisionsOf returns every revision carrying this content, in catalog order.
// Mirrored bytes make that more than one.
func revisionsOf(c *Catalog, id ContentID) []Revision {
	var out []Revision
	for _, r := range c.Revisions() {
		if r.Content == id {
			out = append(out, r)
		}
	}
	return out
}

// Case 7b -- one bundle mirrored into two registry namespaces. The content
// digest is identical by construction; that is what mirroring is. Two
// domain-qualified services published the same bytes, and neither of them is
// the other, so content alone cannot name a revision.
func TestMirroredContentInTwoDomainsStaysTwoServices(t *testing.T) {
	mirrored, lib := ociID("mirrored-api"), at("lib", "mirrored-lib")
	inAlpha := RevisionID{Service: ServiceID{Domain: "reg/alpha", Name: "api"}, Content: mirrored}
	inBeta := RevisionID{Service: ServiceID{Domain: "reg/beta", Name: "api"}, Content: mirrored}
	alpha, beta := "reg/alpha/api:1.0.0", "reg/beta/api:1.0.0"
	// Byte-identical bundles: same declared service, same version, same
	// dependency text. Only the domain that published them differs.
	newMirror := func() *fake {
		return newFake().
			ok(alpha, rev(inAlpha, ct("api", "1.0.0", dep("lib", "reg/lib:1.0.0")))).
			ok(beta, rev(inBeta, ct("api", "1.0.0", dep("lib", "reg/lib:1.0.0")))).
			ok("reg/lib:1.0.0", rev(lib, ct("lib", "1.0.0")))
	}

	c := build(t, newMirror(), Bounds{}, alpha, beta)

	mirrors := revisionsOf(c, mirrored)
	if len(mirrors) != 2 {
		t.Fatalf("revisions holding the mirrored content = %d, want 2: one per publishing domain", len(mirrors))
	}
	if got := []string{mirrors[0].Service.Domain, mirrors[1].Service.Domain}; !slices.Equal(got, []string{"reg/alpha", "reg/beta"}) {
		t.Errorf("mirrored domains = %v, want both publishers named", got)
	}
	if cf := c.Conflicts(); len(cf) != 0 {
		t.Errorf("two mirrors of one bundle are two services, not a disagreement: %+v", cf)
	}
	// Same content, same declaration index, different declaring service. The two
	// declarations of reg/lib:1.0.0 are two declarations.
	decls := map[DeclarationID]bool{}
	for _, e := range c.Edges() {
		decls[e.Declaration] = true
	}
	if len(c.Edges()) != 2 || len(decls) != 2 {
		t.Errorf("edges = %+v, want one per mirror from two distinct declarations", c.Edges())
	}
	l := revisionsOf(c, lib.Content)
	if len(l) != 1 || len(l[0].Paths) != 2 {
		t.Fatalf("lib = %+v, want one revision reached by both mirrors", l)
	}
	if slices.Equal(l[0].Paths[0].Steps, l[0].Paths[1].Steps) {
		t.Errorf("both routes to lib traverse the same step %+v; a step must name which mirror declared it", l[0].Paths[0].Steps)
	}
	// Root order is a detail of the request, never a fact about the catalog.
	reversed := build(t, newMirror(), Bounds{}, beta, alpha)
	if got, want := reversed.Meta().CatalogID, c.Meta().CatalogID; got != want {
		t.Errorf("catalogId = %s asking beta first, %s asking alpha first; root order must not change catalog truth", got, want)
	}
	if n := len(revisionsOf(reversed, mirrored)); n != 2 {
		t.Errorf("reversed: revisions holding the mirrored content = %d, want 2", n)
	}
	// A mirror in a third namespace is a third catalog: which domain published
	// the bytes is part of what the catalog found, not decoration on top of it.
	gamma := "reg/gamma/api:1.0.0"
	inGamma := RevisionID{Service: ServiceID{Domain: "reg/gamma", Name: "api"}, Content: mirrored}
	elsewhere := build(t, newMirror().ok(gamma, rev(inGamma, ct("api", "1.0.0", dep("lib", "reg/lib:1.0.0")))),
		Bounds{}, alpha, gamma)
	if elsewhere.Meta().CatalogID == c.Meta().CatalogID {
		t.Errorf("mirroring into reg/gamma fingerprinted the same as reg/beta: %s", c.Meta().CatalogID)
	}
}

// Case 8 -- a mutable tag whose registry answer changes. It is read once during
// construction and the session does not move afterwards.
func TestMutableTagIsResolvedOnceAndTheSessionDoesNotMove(t *testing.T) {
	before, after := at("api", "before"), at("api", "after")
	f := newFake().
		ok("reg/api:latest", rev(before, ct("api", "1.0.0"))).
		ok("reg/api:latest", rev(after, ct("api", "2.0.0")))

	c := build(t, f, Bounds{}, "reg/api:latest", "reg/api:latest")

	if got := f.countFor("reg/api:latest"); got != 1 {
		t.Fatalf("resolver calls = %d, want exactly 1: a mutable reference is read once", got)
	}
	if _, ok := c.Revision(before); !ok {
		t.Fatalf("the catalog must hold the content resolved during construction")
	}
	if _, moved := c.Revision(after); moved {
		t.Error("the registry's later answer reached the frozen session")
	}
	// Positive arm: the registry really did move. A second construction sees it,
	// so the assertion above is about the session, not about a static fixture.
	c2 := build(t, f, Bounds{}, "reg/api:latest")
	if _, ok := c2.Revision(after); !ok {
		t.Fatalf("the scripted registry did not move; the resolve-once assertion would be vacuous")
	}
	if c.Meta().CatalogID == c2.Meta().CatalogID {
		t.Error("two different resolved contents must not fingerprint alike")
	}
}

// Case 9 -- the same relative dependency text declared from two different local
// bases resolves against its own declaring base. Case 15's declaration conflict
// and the retained-path bound both ride on this shape.
func localTwoBaseFake() (*fake, RevisionID, RevisionID, RevisionID, RevisionID, RevisionID) {
	a, b := atLocal("a", "dir-a"), atLocal("b", "dir-b")
	shared, lib1, lib2 := atLocal("shared", "shared"), atLocal("lib", "lib-1"), atLocal("lib", "lib-2")
	f := newFake().
		ok("/a", rev(a, ct("a", "1.0.0", dep("shared", "./shared"))).withBase("/a").withoutResolvedRef()).
		ok("/b", rev(b, ct("b", "1.0.0", dep("shared", "./shared"))).withBase("/b").withoutResolvedRef()).
		okFrom("/a", "./shared", rev(shared, ct("shared", "1.0.0", dep("lib", "../lib"))).withBase("/a/shared").withoutResolvedRef()).
		okFrom("/b", "./shared", rev(shared, ct("shared", "1.0.0", dep("lib", "../lib"))).withBase("/b/shared").withoutResolvedRef()).
		okFrom("/a/shared", "../lib", rev(lib1, ct("lib", "1.0.0")).withoutResolvedRef()).
		okFrom("/b/shared", "../lib", rev(lib2, ct("lib", "2.0.0")).withoutResolvedRef())
	return f, a, b, shared, lib1, lib2
}

func TestRelativeDependencyResolvesAgainstItsDeclaringBase(t *testing.T) {
	f, _, _, shared, lib1, lib2 := localTwoBaseFake()

	c := build(t, f, Bounds{}, "/a", "/b")

	if len(c.Unresolved()) != 0 {
		t.Fatalf("unresolved = %+v; a relative reference must be resolved against its declarer", c.Unresolved())
	}
	got := edgeTargets(c, shared, 0)
	want := sortedRevisions(map[RevisionID]bool{lib1: true, lib2: true})
	if !slices.Equal(got, want) {
		t.Fatalf("shared's ../lib resolved to %v, want both %v: one per declaring base", got, want)
	}
	// The two byte-identical directories are one revision, reachable from both.
	s := mustRevision(t, c, shared)
	if !s.Shared() || len(s.Paths) != 2 {
		t.Errorf("shared = roots %v paths %+v, want one revision with both routes", s.Roots, s.Paths)
	}
	if s.ResolvedRefs != nil {
		t.Errorf("resolvedRefs = %v; a local resolution has no immutable reference beyond its content", s.ResolvedRefs)
	}
	bases := map[string]bool{}
	for _, call := range f.calls {
		if call.Ref == "../lib" {
			bases[call.Base] = true
		}
	}
	if !bases["/a/shared"] || !bases["/b/shared"] {
		t.Errorf("../lib was asked from bases %v, want both declaring directories", bases)
	}
}

// Case 10 -- hostile identity text. Names carrying "/", ":", "%" and non-ASCII
// must not collide, and neither must the declarations or paths they appear in.
func TestHostileNamesDoNotCollide(t *testing.T) {
	// If a ServiceID were a joined string, "a/b"+"c" and "a"+"b/c" would be the
	// same service at the same version and the catalog would report a content
	// conflict that does not exist.
	one := at("c", "h1").inDomain("a/b")
	two := at("b/c", "h2").inDomain("a")
	three := at("e:f", "h3").inDomain("d%3A")
	four := at("f", "h4").inDomain("d%3A:e")
	f := newFake().
		ok("reg/one:1", rev(one, ct("c", "1.0.0"))).
		ok("reg/two:1", rev(two, ct("b/c", "1.0.0"))).
		ok("reg/three:1", rev(three, ct("e:f", "1.0.0"))).
		ok("reg/four:1", rev(four, ct("f", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/one:1", "reg/two:1", "reg/three:1", "reg/four:1")

	if n := len(c.Revisions()); n != 4 {
		t.Fatalf("revisions = %d, want 4 distinct identities", n)
	}
	if cf := c.Conflicts(); len(cf) != 0 {
		t.Fatalf("hostile names collided into conflicts: %+v", cf)
	}
}

func TestHostileDeclarationsAndPathsDoNotCollide(t *testing.T) {
	root, x, y := at("root", "hroot"), at("x", "hx"), at("y", "hy")
	// Two declarations sharing a name, and references full of delimiters.
	refX, refY := "reg/ä:1%2F", "reg/ä:1%2F/b"
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0", dep("same", refX), dep("same", refY)))).
		ok(refX, rev(x, ct("x", "1.0.0"))).
		ok(refY, rev(y, ct("y", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/root:1")

	if n := len(c.Edges()); n != 2 {
		t.Fatalf("edges = %d, want 2; two declarations sharing a name are two declarations", n)
	}
	if !slices.Equal(edgeTargets(c, root, 0), []RevisionID{x}) || !slices.Equal(edgeTargets(c, root, 1), []RevisionID{y}) {
		t.Errorf("edges = %+v, want each declaration index to keep its own target", c.Edges())
	}
	if len(c.Conflicts()) != 0 {
		t.Errorf("distinct declarations must not read as one ambiguous declaration: %+v", c.Conflicts())
	}
	px := mustRevision(t, c, x)
	py := mustRevision(t, c, y)
	if comparePath(px.Paths[0], py.Paths[0]) == 0 {
		t.Errorf("paths collided: %+v vs %+v", px.Paths[0], py.Paths[0])
	}
}

// Case 11 -- reachable directly and transitively. Direct wins the rank and no
// path is deleted to make that true.
func TestRevisionReachableDirectlyAndTransitivelyRanksDirect(t *testing.T) {
	root, mid, leaf := at("root", "r11"), at("mid", "m11"), at("leaf", "l11")
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0", dep("mid", "reg/mid:1"), dep("leaf", "reg/leaf:1")))).
		ok("reg/mid:1", rev(mid, ct("mid", "1.0.0", dep("leaf", "reg/leaf:1")))).
		ok("reg/leaf:1", rev(leaf, ct("leaf", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/root:1")

	l := mustRevision(t, c, leaf)
	if l.Rank != RankDirect || l.MinDepth != 1 {
		t.Errorf("leaf rank = %q depth = %d, want direct/1", l.Rank, l.MinDepth)
	}
	if len(l.Paths) != 2 {
		t.Fatalf("leaf paths = %+v, want the direct and the transitive route", l.Paths)
	}
	if !hasPath(l, Path{Root: 0, Steps: []DeclarationID{{From: root, Index: 1}}}) {
		t.Error("the direct route is missing")
	}
	if !hasPath(l, Path{Root: 0, Steps: []DeclarationID{{From: root, Index: 0}, {From: mid, Index: 0}}}) {
		t.Error("the transitive route was deleted to make the rank true")
	}
	if got := mustRevision(t, c, root).Rank; got != RankRoot {
		t.Errorf("root rank = %q, want root", got)
	}
	if got := mustRevision(t, c, mid).Rank; got != RankDirect {
		t.Errorf("mid rank = %q, want direct", got)
	}
}

func TestTransitiveRankIsReachable(t *testing.T) {
	root, mid, leaf := at("root", "r11b"), at("mid", "m11b"), at("leaf", "l11b")
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0", dep("mid", "reg/mid:1")))).
		ok("reg/mid:1", rev(mid, ct("mid", "1.0.0", dep("leaf", "reg/leaf:1")))).
		ok("reg/leaf:1", rev(leaf, ct("leaf", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/root:1")

	if got := mustRevision(t, c, leaf).Rank; got != RankTransitive {
		t.Errorf("leaf rank = %q, want transitive", got)
	}
}

// Case 12 -- every work bound stops the resolver, not just the output. Each
// test below asserts the resolver call count, so a bound that merely sliced a
// finished answer would fail here.

func TestRootBoundStopsResolverWork(t *testing.T) {
	a, b := at("a", "br-a"), at("b", "br-b")
	f := newFake().ok("reg/a:1", rev(a, ct("a", "1.0.0"))).ok("reg/b:1", rev(b, ct("b", "1.0.0")))

	c := build(t, f, Bounds{MaxRoots: 1}, "reg/a:1", "reg/b:1")

	if f.count() != 1 {
		t.Errorf("resolver calls = %d, want 1: the surplus root is never resolved", f.count())
	}
	if n := len(c.Roots()); n != 1 {
		t.Errorf("roots = %d, want 1", n)
	}
	if c.Meta().RequestedRoots != 2 {
		t.Errorf("requestedRoots = %d, want the true request size", c.Meta().RequestedRoots)
	}
	if !hasLimitation(c, LimitationRootLimit) || c.Meta().Completeness != CompletenessPartial {
		t.Errorf("meta = %+v, want a partial answer naming the root bound", c.Meta())
	}
}

func TestRevisionBoundStopsResolverWork(t *testing.T) {
	a, b, d := at("a", "bv-a"), at("b", "bv-b"), at("d", "bv-d")
	f := newFake().
		ok("reg/a:1", rev(a, ct("a", "1.0.0"))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0"))).
		ok("reg/d:1", rev(d, ct("d", "1.0.0")))

	c := build(t, f, Bounds{MaxRevisions: 2}, "reg/a:1", "reg/b:1", "reg/d:1")

	if f.count() != 2 {
		t.Errorf("resolver calls = %d, want 2: the third root is refused before any fetch", f.count())
	}
	if n := len(c.Revisions()); n != 2 {
		t.Errorf("revisions = %d, want 2", n)
	}
	third := c.Roots()[2]
	if third.Resolved || third.Reason.Code != ReasonBoundExceeded {
		t.Errorf("root 2 = %+v, want it reported as stopped by a bound", third)
	}
	if !hasLimitation(c, LimitationRevisionLimit) {
		t.Errorf("limitations = %+v, want the revision bound named", c.Meta().Limitations)
	}
}

func TestEdgeBoundStopsResolverWork(t *testing.T) {
	root := at("root", "be-root")
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0",
			dep("d0", "reg/d0:1"), dep("d1", "reg/d1:1"), dep("d2", "reg/d2:1")))).
		ok("reg/d0:1", rev(at("d0", "be-0"), ct("d0", "1.0.0"))).
		ok("reg/d1:1", rev(at("d1", "be-1"), ct("d1", "1.0.0"))).
		ok("reg/d2:1", rev(at("d2", "be-2"), ct("d2", "1.0.0")))

	c := build(t, f, Bounds{MaxEdges: 2}, "reg/root:1")

	if f.count() != 3 {
		t.Errorf("resolver calls = %d, want 3: the third dependency is refused before any fetch", f.count())
	}
	if n := len(c.Edges()); n != 2 {
		t.Errorf("edges = %d, want 2", n)
	}
	if !hasLimitation(c, LimitationEdgeLimit) {
		t.Errorf("limitations = %+v, want the edge bound named", c.Meta().Limitations)
	}
	un := c.Unresolved()
	if len(un) != 1 || un[0].Reason.Code != ReasonBoundExceeded {
		t.Errorf("unresolved = %+v, want the refused dependency reported as bounded", un)
	}
}

// A dependency that fails records no edge, so a bound measured against recorded
// edges never engages on the one closure that costs the most: a contract whose
// declarations are all broken. The bound is on dependency WORK, and work is what
// a failure spends.
func TestEdgeBoundStopsFailingDependencyWorkToo(t *testing.T) {
	root := at("root", "bef-root")
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0",
			dep("d0", "reg/d0:1"), dep("d1", "reg/d1:1"), dep("d2", "reg/d2:1"), dep("d3", "reg/d3:1")))).
		fail("reg/d0:1", ReasonNotFound).
		fail("reg/d1:1", ReasonNotFound).
		fail("reg/d2:1", ReasonNotFound).
		fail("reg/d3:1", ReasonNotFound)

	c := build(t, f, Bounds{MaxEdges: 2}, "reg/root:1")

	if f.count() != 3 {
		t.Errorf("resolver calls = %d, want 3: the root plus the two dependencies the bound allows", f.count())
	}
	for _, ref := range []string{"reg/d2:1", "reg/d3:1"} {
		if n := f.countFor(ref); n != 0 {
			t.Errorf("%s was fetched %d times; a dependency past the bound must never be attempted", ref, n)
		}
	}
	if !hasLimitation(c, LimitationEdgeLimit) {
		t.Errorf("limitations = %+v, want the edge bound named", c.Meta().Limitations)
	}
	if c.Meta().Completeness != CompletenessPartial {
		t.Errorf("completeness = %q, want partial: work stopped short of the request", c.Meta().Completeness)
	}
	bounded := 0
	for _, u := range c.Unresolved() {
		if u.Reason.Code == ReasonBoundExceeded {
			bounded++
		}
	}
	if bounded != 2 || len(c.Unresolved()) != 4 {
		t.Errorf("unresolved = %+v, want all four gaps reported and two of them attributed to the bound", c.Unresolved())
	}
}

// One budget, spent in declaration order, with no refund for the attempts that
// failed. Reporting bounds stay a separate question: see
// TestTheUnresolvedBoundCapsReportingWithoutHidingThatItDid.
func TestEdgeBoundSpendsOneBudgetOnSuccessAndFailureAlike(t *testing.T) {
	root := at("root", "bem-root")
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0",
			dep("gone", "reg/gone:1"), dep("here", "reg/here:1"), dep("late", "reg/late:1")))).
		fail("reg/gone:1", ReasonNotFound).
		ok("reg/here:1", rev(at("here", "bem-here"), ct("here", "1.0.0"))).
		ok("reg/late:1", rev(at("late", "bem-late"), ct("late", "1.0.0")))

	c := build(t, f, Bounds{MaxEdges: 2}, "reg/root:1")

	if f.count() != 3 {
		t.Errorf("resolver calls = %d, want 3: the failed first dependency spent budget the third one then lacked", f.count())
	}
	if n := f.countFor("reg/late:1"); n != 0 {
		t.Errorf("reg/late:1 was fetched %d times; it is resolvable, and that is exactly why the bound must still refuse it", n)
	}
	if n := len(c.Edges()); n != 1 {
		t.Errorf("edges = %d, want 1: of the two attempts the bound paid for, one resolved", n)
	}
	if !hasLimitation(c, LimitationEdgeLimit) {
		t.Errorf("limitations = %+v, want the edge bound named", c.Meta().Limitations)
	}
}

func TestDepthBoundStopsResolverWork(t *testing.T) {
	r, a, b := at("r", "bd-r"), at("a", "bd-a"), at("b", "bd-b")
	f := newFake().
		ok("reg/r:1", rev(r, ct("r", "1.0.0", dep("a", "reg/a:1")))).
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("b", "reg/b:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0")))

	c := build(t, f, Bounds{MaxDepth: 1}, "reg/r:1")

	if f.count() != 2 {
		t.Errorf("resolver calls = %d, want 2: the depth bound stops before the third fetch", f.count())
	}
	if !hasLimitation(c, LimitationDepthLimit) {
		t.Errorf("limitations = %+v, want the depth bound named", c.Meta().Limitations)
	}
}

func TestPathLengthBoundStopsResolverWork(t *testing.T) {
	r, a, b := at("r", "bp-r"), at("a", "bp-a"), at("b", "bp-b")
	f := newFake().
		ok("reg/r:1", rev(r, ct("r", "1.0.0", dep("a", "reg/a:1")))).
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("b", "reg/b:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0")))

	c := build(t, f, Bounds{MaxDepth: 5, MaxPathLength: 1}, "reg/r:1")

	if f.count() != 2 {
		t.Errorf("resolver calls = %d, want 2: a route longer than the bound is never walked", f.count())
	}
	if !hasLimitation(c, LimitationPathLengthLimit) {
		t.Errorf("limitations = %+v, want the path-length bound named", c.Meta().Limitations)
	}
}

func TestRetainedPathBoundStopsResolverWork(t *testing.T) {
	unbounded, _, _, shared, _, _ := localTwoBaseFake()
	full := build(t, unbounded, Bounds{}, "/a", "/b")
	if unbounded.count() != 6 || len(full.Revisions()) != 5 {
		t.Fatalf("unbounded baseline = %d calls / %d revisions, want 6/5",
			unbounded.count(), len(full.Revisions()))
	}

	bounded, _, _, _, _, _ := localTwoBaseFake()
	c := build(t, bounded, Bounds{MaxPaths: 1}, "/a", "/b")

	if bounded.count() != 5 {
		t.Errorf("resolver calls = %d, want 5: the second route is not walked, so its subtree is never fetched",
			bounded.count())
	}
	s := mustRevision(t, c, shared)
	if !s.PathsTruncated || len(s.Paths) != 1 {
		t.Errorf("shared = %+v, want one retained route marked truncated", s)
	}
	if !slices.Equal(s.Roots, []RootID{0, 1}) {
		t.Errorf("shared roots = %v; reachability survives even when a route is not retained", s.Roots)
	}
	if !hasLimitation(c, LimitationPathLimit) {
		t.Errorf("limitations = %+v, want the retained-path bound named", c.Meta().Limitations)
	}
}

// Case 15 -- disagreement stays visible. Nothing is selected away, and the
// conflicting constraints stay attached to their own declarations.
func TestConflictsStayVisible(t *testing.T) {
	f, _, _, shared, lib1, lib2 := localTwoBaseFake()

	c := build(t, f, Bounds{}, "/a", "/b")

	var kinds []ConflictKind
	for _, cf := range c.Conflicts() {
		kinds = append(kinds, cf.Kind)
	}
	if !slices.Contains(kinds, ConflictVersion) || !slices.Contains(kinds, ConflictDeclaration) {
		t.Fatalf("conflicts = %+v, want the version and declaration disagreements reported", c.Conflicts())
	}
	for _, cf := range c.Conflicts() {
		switch cf.Kind {
		case ConflictVersion:
			if cf.Service.Name != "lib" || !slices.Equal(cf.Versions, []string{"1.0.0", "2.0.0"}) {
				t.Errorf("version conflict = %+v, want lib at both versions", cf)
			}
		case ConflictDeclaration:
			if cf.Declaration != (DeclarationID{From: shared, Index: 0}) {
				t.Errorf("declaration conflict = %+v, want shared's ../lib declaration", cf)
			}
		case ConflictContent:
			t.Errorf("unexpected content conflict: %+v", cf)
		}
	}
	// Neither side was dropped: both remain first-class revisions.
	if _, ok := c.Revision(lib1); !ok {
		t.Error("lib 1.0.0 was selected away")
	}
	if _, ok := c.Revision(lib2); !ok {
		t.Error("lib 2.0.0 was selected away")
	}
}

func TestConflictingConstraintsStayAttachedToTheirDeclaration(t *testing.T) {
	root, one, two := at("root", "cc-root"), at("lib", "cc-1"), at("lib", "cc-2")
	c1 := dep("lib", "reg/lib:1")
	c1.Compatibility = "^1.0.0"
	c2 := dep("lib", "reg/lib:2")
	c2.Compatibility = ">=2.0.0"
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0", c1, c2))).
		ok("reg/lib:1", rev(one, ct("lib", "1.0.0"))).
		ok("reg/lib:2", rev(two, ct("lib", "2.0.0")))

	c := build(t, f, Bounds{}, "reg/root:1")

	got := map[string]string{}
	for _, e := range c.Edges() {
		got[e.Ref] = e.Constraint
	}
	if got["reg/lib:1"] != "^1.0.0" || got["reg/lib:2"] != ">=2.0.0" {
		t.Errorf("edge constraints = %v, want each declaration to keep its own", got)
	}
	if len(c.Conflicts()) != 1 || c.Conflicts()[0].Kind != ConflictVersion {
		t.Fatalf("conflicts = %+v, want the version disagreement reported rather than resolved", c.Conflicts())
	}
}

func TestBuildRejectsRequestsItCannotAnswer(t *testing.T) {
	if _, err := Build(context.Background(), Request{Roots: []string{"reg/a:1"}}); err != ErrNoResolver {
		t.Errorf("err = %v, want ErrNoResolver", err)
	}
	if _, err := Build(context.Background(), Request{Resolver: newFake()}); err != ErrNoRoots {
		t.Errorf("err = %v, want ErrNoRoots: a catalog is never inferred from nothing", err)
	}
}

func TestCancellationIsPartialKnowledge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newFake().ok("reg/a:1", rev(at("a", "cx"), ct("a", "1.0.0")))

	c, err := Build(ctx, Request{Roots: []string{"reg/a:1"}, Resolver: f, Clock: fixedClock})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if f.count() != 0 {
		t.Errorf("resolver calls = %d, want 0 after cancellation", f.count())
	}
	if !hasLimitation(c, LimitationCancelled) || c.Meta().Completeness != CompletenessPartial {
		t.Errorf("meta = %+v, want a partial answer naming cancellation", c.Meta())
	}
}

func TestEmptyReferenceIsRejectedWithoutCallingTheResolver(t *testing.T) {
	root := at("root", "er")
	f := newFake().ok("reg/root:1", rev(root, ct("root", "1.0.0", dep("blank", ""))))

	c := build(t, f, Bounds{}, "reg/root:1")

	if f.count() != 1 {
		t.Errorf("resolver calls = %d, want 1: an empty reference is not worth a fetch", f.count())
	}
	un := c.Unresolved()
	if len(un) != 1 || un[0].Reason.Code != ReasonInvalidReference {
		t.Fatalf("unresolved = %+v, want the empty declaration reported", un)
	}
}

func TestResolverOutputIsValidatedBeforeItBecomesIdentity(t *testing.T) {
	good := ociID("v-good")
	cases := []struct {
		name string
		res  Resolution
		want string
	}{
		{"no contract", Resolution{Content: good}, ReasonInvalidContract},
		{"no service name", Resolution{Contract: ct("", "1.0.0"), Content: good}, ReasonInvalidContract},
		{"no content identity", Resolution{Contract: ct("a", "1.0.0")}, ReasonInvalidIdentity},
		{"unknown scheme", Resolution{Contract: ct("a", "1.0.0"),
			Content: ContentID{Scheme: "tag", Digest: good.Digest}}, ReasonInvalidIdentity},
		{"version as identity", Resolution{Contract: ct("a", "1.0.0"),
			Content: ContentID{Scheme: SchemeOCI, Digest: "1.0.0"}}, ReasonInvalidIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake().ok("reg/a:1", tc.res)
			c := build(t, f, Bounds{}, "reg/a:1")
			if got := c.Roots()[0].Reason.Code; got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
			if len(c.Revisions()) != 0 {
				t.Errorf("revisions = %+v, want none: an unusable resolution is not identity", c.Revisions())
			}
		})
	}
}

func TestUnsanitizedResolverErrorsAreNotEchoed(t *testing.T) {
	f := &rawErrorResolver{}
	c, err := Build(context.Background(), Request{Roots: []string{"reg/a:1"}, Resolver: f, Clock: fixedClock})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	r := c.Roots()[0].Reason
	if r.Code != ReasonUnavailable {
		t.Errorf("reason code = %q, want the generic category", r.Code)
	}
	for _, secret := range []string{"hunter2", "reg.example", "401", errSecretBearing.Error()} {
		if strings.Contains(r.Message, secret) {
			t.Errorf("reason message = %q, it echoed %q out of the raw transport error", r.Message, secret)
		}
	}
}

func TestBoundsReportedInMetaAreTheOnesThatApplied(t *testing.T) {
	f := newFake().ok("reg/a:1", rev(at("a", "mb"), ct("a", "1.0.0")))
	c := build(t, f, Bounds{MaxRoots: 1_000_000, MaxDepth: 3}, "reg/a:1")

	got := c.Meta().Bounds
	if got.MaxRoots != CeilingMaxRoots {
		t.Errorf("maxRoots = %d, want the ceiling", got.MaxRoots)
	}
	if got.MaxDepth != 3 || got.MaxRevisions != DefaultMaxRevisions {
		t.Errorf("bounds = %+v, want the caller's depth and the default revision bound", got)
	}
}
