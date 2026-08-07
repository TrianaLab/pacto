package fleet

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// This file holds the adversarial bounds tests for the nested product-detail
// structures that were unbounded when phase-1 item 4 was first marked DONE:
// ownership conflicts, readiness checks, observed runtime, and the owner-attention
// / service-relationship double-truncation.

// A service with more than MaxDetailPreview differently-owned revisions yields a
// bounded conflicts preview with an honest total/count/truncated.
func TestServiceOwnershipConflictsBounded(t *testing.T) {
	over := MaxDetailPreview + 5
	revs := make([]*ContractRevision, over)
	for i := range revs {
		revs[i] = &ContractRevision{
			Key:   RevisionKey(fmt.Sprintf("svc@%d", i)),
			Owner: contract.Owner{Team: fmt.Sprintf("team-%d", i)},
		}
	}
	info := serviceOwnership(&ServiceRecord{Key: "svc", Owner: contract.Owner{Team: "svc-owner"}}, revs)
	c := info.Conflicts
	if c.Total != over || c.Count != MaxDetailPreview || !c.Truncated || len(c.Items) != MaxDetailPreview {
		t.Errorf("conflicts preview = {total:%d count:%d trunc:%v items:%d}, want total=%d count=%d trunc=true",
			c.Total, c.Count, c.Truncated, len(c.Items), over, MaxDetailPreview)
	}
}

// A readiness assessment with more than MaxDetailPreview checks yields a bounded
// checks preview while every scalar readiness fact is preserved.
func TestProductReadinessBounded(t *testing.T) {
	if productReadiness(nil) != nil {
		t.Fatal("nil readiness must map to nil")
	}
	over := MaxDetailPreview + 7
	checks := make([]readiness.CheckResult, over)
	for i := range checks {
		checks[i] = readiness.CheckResult{ID: fmt.Sprintf("c%d", i), Weight: 1}
	}
	days := 3
	r := &readiness.Result{
		Score: 42, TotalWeight: 100, EarnedWeight: 42, MinScore: 80, PartialCredit: 0.5,
		Expires: "2030-01-01", Expired: false, DaysRemaining: &days,
		DoneCount: 3, PartialCount: 2, NotDoneCount: 1, DeferredCount: 4, Passing: false,
		Checks: checks,
	}
	pr := productReadiness(r)
	// Every scalar readiness fact is preserved (compared as a whole, with the
	// bounded checks preview substituted in so only the scalars are under test).
	wantDays := 3
	want := &ProductReadiness{
		Score: 42, TotalWeight: 100, EarnedWeight: 42, MinScore: 80, PartialCredit: 0.5,
		Expires: "2030-01-01", Expired: false, DaysRemaining: &wantDays,
		DoneCount: 3, PartialCount: 2, NotDoneCount: 1, DeferredCount: 4, Passing: false,
		Checks: pr.Checks,
	}
	if !reflect.DeepEqual(pr, want) {
		t.Errorf("scalar readiness facts not preserved:\n got %+v\nwant %+v", pr, want)
	}
	if pr.Checks.Total != over || pr.Checks.Count != MaxDetailPreview || !pr.Checks.Truncated {
		t.Errorf("checks preview = {total:%d count:%d trunc:%v}, want total=%d count=%d trunc=true",
			pr.Checks.Total, pr.Checks.Count, pr.Checks.Truncated, over, MaxDetailPreview)
	}
	// An expired assessment carries no DaysRemaining; it must map to nil, not 0.
	if pr := productReadiness(&readiness.Result{Expired: true}); pr.DaysRemaining != nil {
		t.Errorf("expired readiness must have nil daysRemaining, got %v", *pr.DaysRemaining)
	}
}

// A pathological observed-runtime map (wide, deep and with a huge scalar) cannot
// produce an unbounded runtime preview: the emitted list, each value's length and
// the nesting are all hard-bounded.
func TestRuntimePreviewBounded(t *testing.T) {
	m := map[string]any{}
	for i := 0; i < MaxDetailPreview*3; i++ {
		m[fmt.Sprintf("k%05d", i)] = i
	}
	// A chain of maps far deeper than maxRuntimeDepth.
	var deep any = "leaf"
	for i := 0; i < 20; i++ {
		deep = map[string]any{"n": deep}
	}
	m["deep"] = deep
	// A chain of slices far deeper than maxRuntimeDepth.
	var deepArr any = "leaf"
	for i := 0; i < 20; i++ {
		deepArr = []any{deepArr}
	}
	m["deepArr"] = deepArr
	// A shallow non-empty slice (mixed scalar + nested map).
	m["arr"] = []any{1, "two", map[string]any{"x": 1}}
	// Empty composites are represented as a single non-truncating leaf.
	m["emptyMap"] = map[string]any{}
	m["emptyArr"] = []any{}
	// A single huge scalar value.
	m["big"] = strings.Repeat("x", maxRuntimeValueLen*4)

	rp := runtimePreview(m)
	if rp.Count > MaxDetailPreview || len(rp.Items) > MaxDetailPreview {
		t.Errorf("runtime preview count %d exceeds cap %d", rp.Count, MaxDetailPreview)
	}
	if !rp.Truncated {
		t.Error("a pathological runtime must be reported truncated")
	}
	for _, f := range rp.Items {
		if len([]rune(f.Value)) > maxRuntimeValueLen+1 { // +1 for the ellipsis rune
			t.Errorf("runtime value not length-capped: %d runes (key %q)", len([]rune(f.Value)), f.Key)
		}
	}
	// An empty/nil runtime yields an empty, non-nil, non-truncated preview.
	e := runtimePreview(nil)
	if e.Total != 0 || e.Count != 0 || e.Truncated || e.Items == nil {
		t.Errorf("empty runtime preview = %+v, want empty non-nil non-truncated", e)
	}
}

// An owner with more than DefaultAttentionLimit matching attention items must have
// its owner-detail attention preview report the TRUE matched total (not the paged
// page size) and Truncated=true — the double-truncation regression.
func TestOwnerDetailAttentionTrueTotal(t *testing.T) {
	over := DefaultAttentionLimit + 15
	revs := make([]RawRevision, over)
	for i := range revs {
		// A revision with no readiness declared yields one MISSING_READINESS
		// attention item, attributed to a service owned by team-x.
		c := &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: fmt.Sprintf("svc%04d", i), Version: "1.0.0", Owner: contract.Owner{Team: "team-x"}},
		}
		revs[i] = RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: fmt.Sprintf("sha256:%04d", i)}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("local", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewQuery(snap).EntityDetail(KindOwner, "team-x")
	if err != nil {
		t.Fatal(err)
	}
	att := d.Owner.Attention
	if att.Total != over {
		t.Errorf("owner attention total = %d, want the TRUE matched total %d (double-truncation regression)", att.Total, over)
	}
	if att.Count != MaxDetailPreview || !att.Truncated {
		t.Errorf("owner attention preview = {count:%d trunc:%v}, want count=%d trunc=true", att.Count, att.Truncated, MaxDetailPreview)
	}
}

// A service whose neighborhood is truncated (more neighbors than the node cap)
// must report its relationships preview as truncated, never falsely complete.
func TestServiceDetailRelationshipsTruncationHonest(t *testing.T) {
	over := DefaultMaxNodes + 5
	var revs []RawRevision
	hubDeps := make([]contract.Dependency, over)
	for i := 0; i < over; i++ {
		dep := fmt.Sprintf("dep%03d", i)
		hubDeps[i] = contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true, Compatibility: "^1.0.0"}
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: dep, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
		revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + dep})
	}
	hub := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "hub", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Dependencies: hubDeps}
	revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: hub, FS: fstest.MapFS{}}, Digest: "sha256:hub"})

	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	// Sanity: the neighborhood itself truncates at the node cap.
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(NewServiceKey("hub")), Direction: DirectionBoth, Views: allViews()})
	if err != nil {
		t.Fatal(err)
	}
	if !nb.Truncated {
		t.Fatalf("precondition: neighborhood of a %d-neighbor hub must truncate at the node cap", over)
	}
	d, err := q.EntityDetail(KindService, string(NewServiceKey("hub")))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Service.Relationships.Truncated {
		t.Error("a service whose neighborhood truncated must report relationships.Truncated=true")
	}
}
