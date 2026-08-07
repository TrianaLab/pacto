package fleet

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
)

// This file holds the product-hardening counterexample tests: attention paging
// (item 3.2), nested-collection bounds (item 3.3), view-aware expansion
// affordances (item 4) and entity-detail immutability (item 7).

// ── item 3.2: attention paging ───────────────────────────────────────────────

func TestAttention_RejectsNegativeOffset(t *testing.T) {
	q := productFleet(t)
	if _, err := q.Attention(AttentionFilter{Offset: -1}); err == nil {
		t.Error("a negative offset must be rejected")
	}
}

// TestAttention_PagingStability walks every page with a small limit and proves the
// concatenation reconstructs the full sorted answer exactly once (no gap, no
// overlap), so nothing beyond the first page is ever unreachable.
func TestAttention_PagingStability(t *testing.T) {
	q := productFleet(t)
	full, err := q.Attention(AttentionFilter{Limit: MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	if full.Total < 3 {
		t.Fatalf("need a fixture with several attention items, got %d", full.Total)
	}

	var walked []AttentionItem
	offset, pages := 0, 0
	for {
		page, perr := q.Attention(AttentionFilter{Limit: 2, Offset: offset})
		if perr != nil {
			t.Fatal(perr)
		}
		if page.Total != full.Total {
			t.Errorf("page total = %d, want %d (stable across pages)", page.Total, full.Total)
		}
		if page.Offset != offset {
			t.Errorf("page offset = %d, want %d", page.Offset, offset)
		}
		walked = append(walked, page.Items...)
		pages++
		if pages > full.Total+2 {
			t.Fatal("paging did not terminate")
		}
		if page.NextOffset == nil {
			if page.Truncated {
				t.Error("last page must not be truncated")
			}
			break
		}
		offset = *page.NextOffset
	}
	if len(walked) != len(full.Items) {
		t.Fatalf("paged walk reconstructed %d items, want %d", len(walked), len(full.Items))
	}
	for i := range full.Items {
		if walked[i].Code != full.Items[i].Code || walked[i].Entity.Key != full.Items[i].Entity.Key {
			t.Errorf("paged item %d differs from the single-page order: %+v vs %+v", i, walked[i], full.Items[i])
		}
	}
}

// TestAttention_OffsetBeyondTotal returns an empty last page with no next offset.
func TestAttention_OffsetBeyondTotal(t *testing.T) {
	q := productFleet(t)
	page, err := q.Attention(AttentionFilter{Offset: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 0 || page.Truncated || page.NextOffset != nil {
		t.Errorf("offset beyond total must be an empty final page: %+v", page)
	}
}

// ── item 3.3: nested-collection bounds (adversarial, above every maximum) ─────

// manyClaimsFleet builds a service "app" with n revisions all declaring the same
// dependency on "lib", so the single app->lib edge accumulates n declared claims.
func manyClaimsFleet(t *testing.T, n int) *Query {
	t.Helper()
	lib := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "lib", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	revs := []RawRevision{{Bundle: &contract.Bundle{Contract: lib, FS: fstest.MapFS{}}, Digest: "sha256:lib"}}
	for i := 0; i < n; i++ {
		revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: "app", Version: fmt.Sprintf("1.0.%d", i), Owner: contract.Owner{Team: "t"}},
			Dependencies: []contract.Dependency{{Name: "lib", Ref: "oci://x/lib", Required: true, Compatibility: "^1.0.0"}},
		}, FS: fstest.MapFS{}}, Digest: fmt.Sprintf("sha256:app%d", i)})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// TestNeighborhood_EdgeDeclaredClaimsBounded proves a service with far more than
// MaxEdgeDeclaredClaims revisions declaring the same dependency still yields a
// bounded edge with honest truncation metadata (item 3.3).
func TestNeighborhood_EdgeDeclaredClaimsBounded(t *testing.T) {
	n := MaxEdgeDeclaredClaims + 50
	q := manyClaimsFleet(t, n)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "app", Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	e := edgeBetween(nb, string(NewServiceKey("app")), string(NewServiceKey("lib")))
	if e == nil {
		t.Fatal("no app->lib edge")
	}
	if e.DeclaredClaims.Total != n {
		t.Errorf("declared-claims total = %d, want %d", e.DeclaredClaims.Total, n)
	}
	if e.DeclaredClaims.Count != MaxEdgeDeclaredClaims || len(e.DeclaredClaims.Items) != MaxEdgeDeclaredClaims {
		t.Errorf("declared-claims count = %d, want cap %d", e.DeclaredClaims.Count, MaxEdgeDeclaredClaims)
	}
	if !e.DeclaredClaims.Truncated {
		t.Error("an over-cap edge must report DeclaredClaims.Truncated")
	}
}

// TestPreviews_BoundAboveEveryMaximum feeds each preview constructor an input
// above its cap and proves it truncates with honest total/count/truncated. This
// is the single enforcement point every product nested list passes through.
func TestPreviews_BoundAboveEveryMaximum(t *testing.T) {
	over := MaxDetailPreview + 5
	refs := make([]EntityRef, over)
	fs := make([]finding.Finding, over)
	afs := make([]AttributedFinding, over)
	lims := make([]Limitation, over)
	alims := make([]AttributedLimitation, over)
	evs := make([]EvidenceItem, over)
	edges := make([]NeighborhoodEdge, over)
	tools := make([]ToolSummary, over)
	docs := make([]DocRef, over)
	strs := make([]string, over)
	attn := make([]AttentionItem, over)
	checkDetail := func(name string, total, count int, trunc bool) {
		if total != over || count != MaxDetailPreview || !trunc {
			t.Errorf("%s: total=%d count=%d trunc=%v, want total=%d count=%d trunc=true", name, total, count, trunc, over, MaxDetailPreview)
		}
	}
	rp := refPreview(refs)
	checkDetail("refPreview", rp.Total, rp.Count, rp.Truncated)
	fp := findingsPreview(fs)
	checkDetail("findingsPreview", fp.Total, fp.Count, fp.Truncated)
	afp := attributedFindingsPreview(afs)
	checkDetail("attributedFindingsPreview", afp.Total, afp.Count, afp.Truncated)
	lp := limitationsPreview(lims)
	checkDetail("limitationsPreview", lp.Total, lp.Count, lp.Truncated)
	alp := attributedLimitationsPreview(alims)
	checkDetail("attributedLimitationsPreview", alp.Total, alp.Count, alp.Truncated)
	ep := evidencePreview(evs)
	checkDetail("evidencePreview", ep.Total, ep.Count, ep.Truncated)
	rlp := relationshipsPreview(edges)
	checkDetail("relationshipsPreview", rlp.Total, rlp.Count, rlp.Truncated)
	tp := toolsPreview(tools)
	checkDetail("toolsPreview", tp.Total, tp.Count, tp.Truncated)
	dp := docsPreview(docs)
	checkDetail("docsPreview", dp.Total, dp.Count, dp.Truncated)
	sp := stringsPreview(strs)
	checkDetail("stringsPreview", sp.Total, sp.Count, sp.Truncated)
	ap := attentionPreview(attn)
	checkDetail("attentionPreview", ap.Total, ap.Count, ap.Truncated)

	// The neighborhood edge previews have their own caps.
	claims := make([]DeclaredClaim, MaxEdgeDeclaredClaims+3)
	dc := declaredClaimsPreview(claims)
	if dc.Total != len(claims) || dc.Count != MaxEdgeDeclaredClaims || !dc.Truncated {
		t.Errorf("declaredClaimsPreview: %+v", dc)
	}
	stats := make([]ObservedSourceStat, MaxEdgeObservationSources+3)
	os := observationSourcesPreview(stats)
	if os.Total != len(stats) || os.Count != MaxEdgeObservationSources || !os.Truncated {
		t.Errorf("observationSourcesPreview: %+v", os)
	}

	// A below-cap input is not truncated (the non-truncation branch).
	small := refPreview(refs[:1])
	if small.Truncated || small.Count != 1 || small.Total != 1 {
		t.Errorf("below-cap preview must not be truncated: %+v", small)
	}
}

// ── item 4: view-aware expansion affordances ─────────────────────────────────

// payloadCount reports how many kind payloads an entity detail has populated
// (must always be exactly one). Shared by the detail tests.
func payloadCount(d *EntityDetail) int {
	n := 0
	for _, ok := range []bool{d.Service != nil, d.Revision != nil, d.Target != nil, d.Owner != nil, d.Source != nil} {
		if ok {
			n++
		}
	}
	return n
}

// ownershipIs reports whether an ownership summary names the given owner with a
// non-nil owner reference.
func ownershipIs(o *OwnershipInfo, owner string) bool {
	return o != nil && o.Owner == owner && o.Ref != nil
}

// dirSet turns an expansions slice into a set for assertions.
func dirSet(dirs []Direction) map[Direction]bool {
	m := map[Direction]bool{}
	for _, d := range dirs {
		m[d] = true
	}
	return m
}

// TestExpansions_ViewAware proves expansion affordances are derived from the SAME
// requested knowledge views as traversal, so an excluded knowledge kind never
// leaks a "there is more this way" arrow.
func TestExpansions_ViewAware(t *testing.T) {
	q := productFleet(t)

	// alpha: declared dependency on leaf-svc (outgoing); observed dependent beta
	// (incoming, observed-only); alpha->leaf is also observed.
	expected, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	exp := dirSet(focusNode(expected).Expansions)
	if !exp[DirectionDependencies] {
		t.Error("expected view: declared dependency must offer a dependencies expansion")
	}
	if exp[DirectionDependents] {
		t.Error("expected view: must NOT offer a dependents expansion (beta is observed-only)")
	}

	observed, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewObserved}})
	obs := dirSet(focusNode(observed).Expansions)
	if !obs[DirectionDependents] {
		t.Error("observed view: observed dependent beta must offer a dependents expansion")
	}

	diff, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewDifferences}})
	d := dirSet(focusNode(diff).Expansions)
	if !d[DirectionDependencies] || !d[DirectionDependents] {
		t.Errorf("differences view must offer both expansion directions: %v", focusNode(diff).Expansions)
	}

	// A declared-only fleet: the observed view must offer NO expansion at all.
	cq := chainFleet(t)
	obsChain, _ := cq.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "web", Direction: DirectionDependencies, Views: []KnowledgeView{ViewObserved}})
	if len(focusNode(obsChain).Expansions) != 0 {
		t.Errorf("observed view over a declared-only fleet must offer no expansions: %v", focusNode(obsChain).Expansions)
	}
	expChain, _ := cq.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "web", Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if !dirSet(focusNode(expChain).Expansions)[DirectionDependencies] {
		t.Error("expected view over a declared-only fleet must offer the declared-dependency expansion")
	}
}

// ── item 6/coverage: link state + sibling revisions ──────────────────────────

// TestTargetDetail_InferredLinkState covers the inferred link-state branch via
// leaf-svc's inferred target.
func TestTargetDetail_InferredLinkState(t *testing.T) {
	q := productFleet(t)
	leafRev := revKeyOf(t, q, "leaf-svc")
	d, err := q.EntityDetail(KindRevision, leafRev)
	if err != nil || d.Revision.InferredTargets.Count == 0 {
		t.Fatalf("leaf-svc must have an inferred target: %v %+v", err, d.Revision)
	}
	inferredKey := d.Revision.InferredTargets.Items[0].Key
	td, err := q.EntityDetail(KindTarget, inferredKey)
	if err != nil {
		t.Fatal(err)
	}
	if td.Target.LinkState != "inferred" {
		t.Errorf("inferred target link state = %q, want inferred", td.Target.LinkState)
	}
}

// TestRevisionDetail_SiblingRevisions covers previous/next navigation across a
// service with several known revisions.
func TestRevisionDetail_SiblingRevisions(t *testing.T) {
	q := manyClaimsFleet(t, 3) // "app" now has 3 revisions
	var keys []string
	for k, r := range q.Snapshot().Revisions {
		if r.ServiceKey == NewServiceKey("app") {
			keys = append(keys, string(k))
		}
	}
	if len(keys) != 3 {
		t.Fatalf("want 3 app revisions, got %d", len(keys))
	}
	// The middle revision has both a previous and a next; the ends have one each.
	var hasBoth, hasPrevOnly, hasNextOnly bool
	for _, k := range keys {
		d, err := q.EntityDetail(KindRevision, k)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case d.Revision.Previous != nil && d.Revision.Next != nil:
			hasBoth = true
		case d.Revision.Previous != nil:
			hasPrevOnly = true
		case d.Revision.Next != nil:
			hasNextOnly = true
		}
	}
	if !hasBoth || !hasPrevOnly || !hasNextOnly {
		t.Errorf("sibling navigation incomplete: both=%v prevOnly=%v nextOnly=%v", hasBoth, hasPrevOnly, hasNextOnly)
	}
}

// ── item 7: entity-detail immutability ───────────────────────────────────────

// mutateDetail deeply mutates every reachable field of a detail answer so an
// aliased slice, map or pointer into the snapshot would be caught. It dispatches
// to a per-kind mutator (kept separate to stay within the cyclomatic budget).
func mutateDetail(d *EntityDetail) {
	mutateSources(d.Meta.Sources)
	mutateLimitations(d.Meta.Limitations)
	mutateEntityRef(&d.Entity)
	d.Status = "hacked"
	for i := range d.Actions {
		d.Actions[i] = "hacked"
	}
	switch {
	case d.Service != nil:
		mutateServiceDetail(d.Service)
	case d.Revision != nil:
		mutateRevisionDetail(d.Revision)
	case d.Target != nil:
		mutateTargetDetail(d.Target)
	case d.Owner != nil:
		mutateOwnerDetail(d.Owner)
	case d.Source != nil:
		mutateSourceDetail(d.Source)
	}
}

func mutateServiceDetail(s *ServiceDetailData) {
	mutateRefPreview(&s.Revisions)
	mutateRefPreview(&s.Deployments)
	mutateRefPreview(&s.Dependencies)
	mutateRefPreview(&s.Dependents)
	if s.Ownership != nil {
		s.Ownership.Owner = "hacked"
	}
	for i := range s.Relationships.Items {
		mutateEdge(&s.Relationships.Items[i])
	}
	for i := range s.Findings.Items {
		mutateEntityRef(&s.Findings.Items[i].Entity)
		s.Findings.Items[i].Finding.Message = "hacked"
	}
	for i := range s.Evidence.Items {
		mutateEntityRef(&s.Evidence.Items[i].Target)
		mutateTimePtr(s.Evidence.Items[i].At)
	}
	for i := range s.Limitations.Items {
		mutateEntityRef(&s.Limitations.Items[i].Entity)
		s.Limitations.Items[i].Limitation.Code = "hacked"
	}
}

func mutateRevisionDetail(r *RevisionDetailData) {
	mutateEntityRef(&r.Service)
	r.Identity.Digest = "hacked"
	if r.Readiness != nil {
		r.Readiness.Score = -999
	}
	for i := range r.Validation.Items {
		r.Validation.Items[i].Message = "hacked"
	}
	for i := range r.Tools.Items {
		r.Tools.Items[i].Name = "hacked"
	}
	for i := range r.Skills.Items {
		r.Skills.Items[i] = "hacked"
	}
	for i := range r.Docs.Items {
		r.Docs.Items[i].Title = "hacked"
	}
	mutateRefPreview(&r.ExactTargets)
	mutateRefPreview(&r.InferredTargets)
	for i := range r.Dependencies.Items {
		mutateEdge(&r.Dependencies.Items[i])
	}
	mutateRefPtr(r.Previous)
	mutateRefPtr(r.Next)
}

func mutateTargetDetail(tg *TargetDetailData) {
	mutateEntityRef(&tg.Service)
	mutateRefPtr(tg.Revision)
	if tg.Coverage != nil {
		tg.Coverage.Evaluated = -1
	}
	for i := range tg.ObservedRuntime.Items {
		tg.ObservedRuntime.Items[i].Value = "hacked"
	}
	for i := range tg.Findings.Items {
		tg.Findings.Items[i].Message = "hacked"
	}
	for i := range tg.Sources.Items {
		tg.Sources.Items[i] = "hacked"
	}
	mutateTimePtr(tg.EvidenceAt)
	mutateTimePtr(tg.ReconciledAt)
	for i := range tg.Limitations.Items {
		tg.Limitations.Items[i].Code = "hacked"
	}
}

func mutateOwnerDetail(o *OwnerDetailData) {
	mutateRefPreview(&o.Services)
	mutateRefPreview(&o.Revisions)
	mutateRefPreview(&o.Deployments)
	for i := range o.Attention.Items {
		mutateEntityRef(&o.Attention.Items[i].Entity)
		o.Attention.Items[i].Label = "hacked"
	}
}

func mutateSourceDetail(src *SourceDetailData) {
	if src.Error != nil {
		src.Error.Code = "hacked"
		src.Error.Message = "hacked"
	}
	mutateTimePtr(src.LastSuccessfulSync)
	mutateTimePtr(src.ObservedAt)
	mutateRefPreview(&src.Entities)
	for i := range src.Limitations.Items {
		src.Limitations.Items[i].Code = "hacked"
	}
}

func mutateTimePtr(t *time.Time) {
	if t != nil {
		*t = time.Unix(0, 0)
	}
}

func mutateRefPtr(r *EntityRef) {
	if r != nil {
		mutateEntityRef(r)
	}
}

func mutateRefPreview(p *RefPreview) {
	for i := range p.Items {
		mutateEntityRef(&p.Items[i])
	}
}

func mutateEdge(e *NeighborhoodEdge) {
	mutateEntityRef(&e.From)
	mutateEntityRef(&e.To)
	for i := range e.DeclaredClaims.Items {
		e.DeclaredClaims.Items[i].Compatibility = "hacked"
	}
	for i := range e.ObservationSources.Items {
		e.ObservationSources.Items[i].Source = "hacked"
		mutateTimePtr(e.ObservationSources.Items[i].LastSeen)
	}
}

func TestEntityDetail_Immutable(t *testing.T) {
	q := productFleet(t)
	cases := []struct {
		kind EntityKind
		key  string
	}{
		{KindService, "alpha"},
		{KindRevision, revKeyOf(t, q, "alpha")},
		{KindTarget, string(NewTargetKey("prod", "k8s", "alpha-app"))},
		{KindOwner, "team-a"},
		{KindSource, "local"},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			d1, err := q.EntityDetail(c.kind, c.key)
			if err != nil {
				t.Fatal(err)
			}
			want := mustJSON(t, d1)
			snapBefore := mustJSON(t, q.snap)

			mutateDetail(d1)

			if after := mustJSON(t, q.snap); after != snapBefore {
				t.Errorf("mutating a %s detail changed the snapshot", c.kind)
			}
			d2, err := q.EntityDetail(c.kind, c.key)
			if err != nil {
				t.Fatal(err)
			}
			if got := mustJSON(t, d2); got != want {
				t.Errorf("a second %s detail differs from the first (shared state leaked)", c.kind)
			}
		})
	}
}
