package catalog

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// Case 13 -- the caller owns what it passed in and what it got back, and
// neither reaches catalog state.
func TestCallerCannotMutateTheCatalogThroughItsInputs(t *testing.T) {
	root, leaf := at("root", "mi-root"), at("leaf", "mi-leaf")
	src := ct("root", "1.0.0", dep("leaf", "reg/leaf:1"))
	src.Service.Owner = contract.Owner{
		Team:     "platform",
		DRI:      "ada",
		Contacts: []contract.OwnerContact{{Type: "email", Value: "platform@example.com"}},
	}
	f := newFake().
		ok("reg/root:1", rev(root, src)).
		ok("reg/leaf:1", rev(leaf, ct("leaf", "1.0.0")))

	roots := []string{"reg/root:1"}
	c, err := Build(context.Background(), Request{Roots: roots, Resolver: f, Clock: fixedClock})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The request slice, the contract and its nested slices are all the caller's.
	roots[0] = "reg/evil:1"
	src.Service.Owner.Contacts[0].Value = "attacker@example.com"
	src.Service.Owner.Team = "evil"
	src.Dependencies[0].Ref = "reg/evil:1"
	src.Dependencies[0].Name = "evil"

	if got := c.Roots()[0].RequestedRef; got != "reg/root:1" {
		t.Errorf("root ref = %q, the caller's slice reached catalog state", got)
	}
	r := mustRevision(t, c, root)
	if r.Owner.Team != "platform" || r.Owner.Contacts[0].Value != "platform@example.com" {
		t.Errorf("owner = %+v, the caller's contract reached catalog state", r.Owner)
	}
	e := c.Edges()[0]
	if e.Ref != "reg/leaf:1" || e.Name != "leaf" {
		t.Errorf("edge = %+v, the caller's dependency slice reached catalog state", e)
	}
}

func TestCallerCannotMutateTheCatalogThroughReturnedValues(t *testing.T) {
	root, leaf, gone := at("root", "mr-root"), at("leaf", "mr-leaf"), at("leaf", "mr-gone")
	f := newFake().
		ok("reg/root:1", rev(root, ct("root", "1.0.0",
			dep("leaf", "reg/leaf:1"), dep("gone", "reg/gone:1")))).
		ok("reg/leaf:1", rev(leaf, ct("leaf", "1.0.0", dep("root", "reg/root:1")))).
		fail("reg/gone:1", ReasonNotFound)
	// A second root at the same service and version makes a conflict exist.
	f.ok("mirror/leaf:1", rev(gone, ct("leaf", "1.0.0")))

	c := build(t, f, Bounds{}, "reg/root:1", "mirror/leaf:1")
	before := snapshot(c)

	// Every accessor, mutated as hard as the type allows.
	rs := c.Roots()
	rs[0].RequestedRef = "evil"
	rs[0].Revision = at("evil", "evil")

	revs := c.Revisions()
	for i := range revs {
		revs[i].Version = "evil"
		revs[i].Roots = append(revs[i].Roots[:0], 99)
		revs[i].RequestedRefs = append(revs[i].RequestedRefs[:0], "evil")
		revs[i].ResolvedRefs = append(revs[i].ResolvedRefs[:0], "evil")
		for j := range revs[i].Paths {
			revs[i].Paths[j].Root = 99
			revs[i].Paths[j].Steps = nil
		}
		revs[i].Owner.Contacts = append(revs[i].Owner.Contacts, contract.OwnerContact{Value: "evil"})
	}

	one, _ := c.Revision(root)
	one.Paths = nil
	one.RequestedRefs = append(one.RequestedRefs[:0], "evil")

	es := c.Edges()
	es[0].To = at("evil", "evil")

	us := c.Unresolved()
	us[0].Ref = "evil"

	cfs := c.Conflicts()
	for i := range cfs {
		cfs[i].Kind = "evil"
		cfs[i].Revisions = append(cfs[i].Revisions[:0], at("evil", "evil"))
		cfs[i].Versions = append(cfs[i].Versions[:0], "evil")
	}

	cys := c.Cycles()
	for i := range cys {
		cys[i].Revisions = append(cys[i].Revisions[:0], at("evil", "evil"))
	}

	m := c.Meta()
	m.Completeness = "evil"
	if len(m.Limitations) > 0 {
		m.Limitations[0].Code = "EVIL"
	}

	if after := snapshot(c); after != before {
		t.Errorf("the catalog changed after callers mutated what they were handed:\nbefore %s\nafter  %s", before, after)
	}
}

// snapshot renders everything the catalog exposes, so a mutation anywhere shows
// up as a difference rather than needing an assertion per field. It takes no
// *testing.T because concurrent readers call it from their own goroutines.
func snapshot(c *Catalog) string {
	b, err := json.Marshal(struct {
		Meta       Meta
		Roots      []Root
		Revisions  []Revision
		Edges      []Edge
		Unresolved []Unresolved
		Conflicts  []Conflict
		Cycles     []Cycle
	}{c.Meta(), c.Roots(), c.Revisions(), c.Edges(), c.Unresolved(), c.Conflicts(), c.Cycles()})
	if err != nil { // unreachable: every field marshals
		return "unmarshalable: " + err.Error()
	}
	return string(b)
}

func permutationFake() *fake {
	a, b, s := at("a", "perm-a"), at("b", "perm-b"), at("s", "perm-s")
	return newFake().
		ok("reg/a:1", rev(a, ct("a", "1.0.0", dep("s", "reg/s:1")))).
		ok("reg/b:1", rev(b, ct("b", "1.0.0", dep("s", "reg/s:1")))).
		ok("reg/s:1", rev(s, ct("s", "1.0.0")))
}

// Case 14 -- ordering is deterministic and catalogId identifies the resolved
// catalog, not the request that produced it or the moment it was produced.
func TestOrderingIsDeterministic(t *testing.T) {
	first := build(t, permutationFake(), Bounds{}, "reg/a:1", "reg/b:1")
	second := build(t, permutationFake(), Bounds{}, "reg/a:1", "reg/b:1")

	if snapshot(first) != snapshot(second) {
		t.Fatalf("two identical builds differ:\n%s\n%s", snapshot(first), snapshot(second))
	}
	revs := first.Revisions()
	if !slices.IsSortedFunc(revs, func(x, y Revision) int { return compareRevisionID(x.ID(), y.ID()) }) {
		t.Errorf("revisions are not ordered by identity: %+v", revs)
	}
	if !slices.IsSortedFunc(first.Edges(), compareEdge) {
		t.Errorf("edges are not deterministically ordered: %+v", first.Edges())
	}
	for _, r := range revs {
		if !slices.IsSortedFunc(r.Paths, comparePath) {
			t.Errorf("paths of %s are not deterministically ordered: %+v", r.Content, r.Paths)
		}
	}
}

func TestCatalogIDIgnoresRootOrderAndGenerationTime(t *testing.T) {
	base := build(t, permutationFake(), Bounds{}, "reg/a:1", "reg/b:1")
	permuted := build(t, permutationFake(), Bounds{}, "reg/b:1", "reg/a:1")

	if base.Meta().CatalogID != permuted.Meta().CatalogID {
		t.Errorf("catalogId changed when the roots were permuted: %s vs %s",
			base.Meta().CatalogID, permuted.Meta().CatalogID)
	}
	// The permutation really did change the catalog's shape, so the assertion
	// above is about the fingerprint rather than about two identical values.
	if reflect.DeepEqual(base.Roots(), permuted.Roots()) {
		t.Fatal("permuting the roots changed nothing; the fingerprint assertion would be vacuous")
	}

	later := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	aged, err := Build(context.Background(), Request{
		Roots:    []string{"reg/a:1", "reg/b:1"},
		Resolver: permutationFake(),
		Clock:    func() time.Time { return later },
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if aged.Meta().CatalogID != base.Meta().CatalogID {
		t.Errorf("catalogId changed with the clock alone")
	}
	if !aged.Meta().GeneratedAt.Equal(later) {
		t.Errorf("generatedAt = %v, want the injected time", aged.Meta().GeneratedAt)
	}
}

func TestCatalogIDDistinguishesDifferentCatalogs(t *testing.T) {
	base := build(t, permutationFake(), Bounds{}, "reg/a:1", "reg/b:1")

	cases := map[string]*Catalog{
		"one root fewer": build(t, permutationFake(), Bounds{}, "reg/a:1"),
		"different content": build(t, newFake().
			ok("reg/a:1", rev(at("a", "perm-a"), ct("a", "1.0.0", dep("s", "reg/s:1")))).
			ok("reg/b:1", rev(at("b", "perm-b"), ct("b", "1.0.0", dep("s", "reg/s:1")))).
			ok("reg/s:1", rev(at("s", "perm-s-rebuilt"), ct("s", "1.0.0"))),
			Bounds{}, "reg/a:1", "reg/b:1"),
		"a gap": build(t, newFake().
			ok("reg/a:1", rev(at("a", "perm-a"), ct("a", "1.0.0", dep("s", "reg/s:1")))).
			ok("reg/b:1", rev(at("b", "perm-b"), ct("b", "1.0.0", dep("s", "reg/s:1")))),
			Bounds{}, "reg/a:1", "reg/b:1"),
		"a tighter bound": build(t, permutationFake(), Bounds{MaxDepth: 1}, "reg/a:1", "reg/b:1"),
	}
	for name, other := range cases {
		if other.Meta().CatalogID == base.Meta().CatalogID {
			t.Errorf("%s fingerprinted the same as the baseline", name)
		}
	}
}

// The catalog reuses pkg/fleet's completeness words because they mean the same
// thing. If fleet renames one, this fails rather than letting the two drift into
// vocabularies an agent reads as different.
func TestCompletenessVocabularyMatchesFleet(t *testing.T) {
	pairs := [][2]string{
		{string(CompletenessComplete), string(fleet.CompletenessComplete)},
		{string(CompletenessPartial), string(fleet.CompletenessPartial)},
		{string(CompletenessEmpty), string(fleet.CompletenessEmpty)},
		{LimitationUnresolvedDep, fleet.LimitationUnresolvedDep},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("catalog says %q where fleet says %q", p[0], p[1])
		}
	}
}

func TestCatalogIsSafeForConcurrentReaders(t *testing.T) {
	c := build(t, permutationFake(), Bounds{}, "reg/a:1", "reg/b:1")
	want := snapshot(c)

	done := make(chan string, 8)
	for range cap(done) {
		go func() { done <- snapshot(c) }()
	}
	for range cap(done) {
		if got := <-done; got != want {
			t.Errorf("a concurrent reader saw a different catalog")
		}
	}
}

func TestRevisionLookupMissIsNotAnEmptyRevision(t *testing.T) {
	c := build(t, permutationFake(), Bounds{}, "reg/a:1")
	if r, ok := c.Revision(at("absent", "absent")); ok {
		t.Errorf("lookup of an absent identity returned %+v", r)
	}
}

func TestBuildDefaultsTheClockToWallTime(t *testing.T) {
	before := time.Now()
	c, err := Build(context.Background(), Request{Roots: []string{"reg/a:1"}, Resolver: permutationFake()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if at := c.Meta().GeneratedAt; at.Before(before) || at.After(time.Now()) {
		t.Errorf("generatedAt = %v, want a time from this build", at)
	}
}
