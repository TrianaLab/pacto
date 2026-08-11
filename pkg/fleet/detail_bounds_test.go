package fleet

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
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

// A pathological observed-runtime map (wide, deep, long keys and a huge scalar)
// cannot produce an unbounded runtime preview: the emitted list, each key and
// value length, and the nesting are all hard-bounded, and because the walk stopped
// early the exact total is honestly reported as unknown (nil).
// pathologicalRuntime builds a runtime map that is simultaneously very wide, very
// deep (maps and slices), long-keyed and huge-scalar, to attack every bound.
func pathologicalRuntime() map[string]any {
	m := map[string]any{}
	for i := 0; i < maxRuntimeScan*3; i++ {
		m[fmt.Sprintf("k%05d", i)] = i
	}
	deep, arr := any("leaf"), any("leaf")
	for i := 0; i < 20; i++ {
		deep = map[string]any{"n": deep}
		arr = []any{arr}
	}
	m["deep"] = deep
	m["deepArr"] = arr
	m["arr"] = []any{1, "two", map[string]any{"x": 1}}
	m["emptyMap"] = map[string]any{}
	m["emptyArr"] = []any{}
	m["big"] = strings.Repeat("x", maxRuntimeValueLen*4)
	m[strings.Repeat("K", maxRuntimeKeyLen*4)] = 1
	return m
}

// assertRuntimeItemsCapped fails if any fact's key or value exceeds its length cap.
func assertRuntimeItemsCapped(t *testing.T, items []RuntimeFact) {
	t.Helper()
	for _, f := range items {
		if len([]rune(f.Value)) > maxRuntimeValueLen+1 { // +1 for the ellipsis rune
			t.Errorf("runtime value not length-capped: %d runes (key %q)", len([]rune(f.Value)), f.Key)
		}
		if len([]rune(f.Key)) > maxRuntimeKeyLen+1 {
			t.Errorf("runtime key not length-capped: %d runes", len([]rune(f.Key)))
		}
	}
}

func TestRuntimePreviewBounded(t *testing.T) {
	rp := runtimePreview(pathologicalRuntime())
	if rp.Count > MaxDetailPreview || len(rp.Items) > MaxDetailPreview {
		t.Errorf("runtime preview count %d exceeds cap %d", rp.Count, MaxDetailPreview)
	}
	if rp.Scanned > maxRuntimeScan {
		t.Errorf("runtime walk scanned %d facts, exceeds the scan bound %d", rp.Scanned, maxRuntimeScan)
	}
	if !rp.Truncated {
		t.Error("a pathological runtime must be reported truncated")
	}
	if rp.Total != nil {
		t.Errorf("a runtime whose walk stopped early must report an UNKNOWN total (nil), got %d", *rp.Total)
	}
	assertRuntimeItemsCapped(t, rp.Items)
}

// An empty/nil runtime yields an empty, non-nil, non-truncated preview with a KNOWN
// total of zero (the walk trivially completed).
func TestRuntimePreviewEmpty(t *testing.T) {
	e := runtimePreview(nil)
	if e.Total == nil || *e.Total != 0 || e.Count != 0 || e.Scanned != 0 || e.Truncated || e.Items == nil {
		t.Errorf("empty runtime preview = %+v (total=%v), want empty non-nil non-truncated total=0", e, e.Total)
	}
}

// A composite collapsed at the depth limit is summarized with a SHORT structural
// marker, never the whole stringified value (requirement, item 13).
func TestRuntimePreview_DepthCollapseMarker(t *testing.T) {
	huge := map[string]any{}
	for i := 0; i < 5000; i++ {
		huge[fmt.Sprintf("h%05d", i)] = strings.Repeat("y", 100)
	}
	// Wrap so the HUGE map sits exactly at the depth limit (root is depth 1).
	nested := any(huge)
	for i := 0; i < maxRuntimeDepth-1; i++ {
		nested = map[string]any{"a": nested}
	}
	rp := runtimePreview(map[string]any{"root": nested})
	marker := ""
	for _, f := range rp.Items {
		if strings.HasPrefix(f.Value, "{map:") {
			marker = f.Value
		}
	}
	if marker == "" {
		t.Fatalf("expected a depth-collapsed map marker among %d items", len(rp.Items))
	}
	if len(marker) > 64 {
		t.Errorf("depth-collapsed composite was stringified whole (%d chars): %q", len(marker), marker)
	}
	if !strings.Contains(marker, "5000") {
		t.Errorf("depth-collapsed map marker should report the key count, got %q", marker)
	}
}

// A wide map flattens deterministically to its lexicographically-smallest keys
// regardless of Go's random map order, and reports an unknown total.
func TestRuntimePreview_WideMapDeterministic(t *testing.T) {
	wide := map[string]any{}
	for i := 0; i < maxRuntimeScan*4; i++ {
		wide[fmt.Sprintf("w%06d", i)] = i
	}
	a := runtimePreview(wide)
	b := runtimePreview(wide)
	if !reflect.DeepEqual(a.Items, b.Items) {
		t.Error("runtime flatten of a wide map is not deterministic across runs")
	}
	if len(a.Items) == 0 || a.Items[0].Key != "w000000" {
		t.Errorf("wide flatten must start at the lexicographically smallest key, got %q", firstKey(a.Items))
	}
	if a.Total != nil {
		t.Error("a map wider than the scan budget must report an unknown total")
	}
	if a.Scanned > maxRuntimeScan {
		t.Errorf("scanned %d exceeds budget %d", a.Scanned, maxRuntimeScan)
	}
}

// A small, fully-walked structure reports the EXACT total and is not truncated.
func TestRuntimePreview_ExactTotalWhenComplete(t *testing.T) {
	small := map[string]any{"a": 1, "b": map[string]any{"c": 2, "d": 3}, "e": []any{"x", "y"}}
	sp := runtimePreview(small)
	if sp.Total == nil {
		t.Fatal("a fully-walked structure must report a known total")
	}
	// facts: a, b.c, b.d, e[0], e[1] => 5
	if *sp.Total != 5 || sp.Count != 5 || sp.Scanned != 5 || sp.Truncated {
		t.Errorf("small flatten = {total:%v count:%d scanned:%d trunc:%v}, want 5/5/5/false", *sp.Total, sp.Count, sp.Scanned, sp.Truncated)
	}
}

// keysBounded selects the smallest keys with O(limit) allocation and reports
// completeness honestly.
func TestKeysBounded(t *testing.T) {
	km := map[string]any{"c": 1, "a": 1, "b": 1, "e": 1, "d": 1}
	got, all := keysBounded(km, 3)
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) || all {
		t.Errorf("keysBounded(limit 3) = %v all=%v, want [a b c] all=false", got, all)
	}
	got, all = keysBounded(km, 10)
	if !reflect.DeepEqual(got, []string{"a", "b", "c", "d", "e"}) || !all {
		t.Errorf("keysBounded(limit 10) = %v all=%v, want all 5 sorted all=true", got, all)
	}
	if got, all := keysBounded(km, 0); got != nil || all {
		t.Errorf("keysBounded(limit 0) = %v all=%v, want nil false", got, all)
	}
	if _, all := keysBounded(map[string]any{}, 0); !all {
		t.Error("keysBounded of an empty map must report all=true even at limit 0")
	}
}

func firstKey(items []RuntimeFact) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].Key
}

// A huge slice (wider than the scan budget) must stop the walk at the budget and
// report an unknown total, exercising the slice-loop bound (not just the map one).
func TestRuntimePreviewWideSliceBounded(t *testing.T) {
	big := make([]any, maxRuntimeScan*2)
	for i := range big {
		big[i] = i
	}
	rp := runtimePreview(map[string]any{"s": big})
	if rp.Total != nil {
		t.Errorf("a slice wider than the scan budget must report an unknown total, got %d", *rp.Total)
	}
	if rp.Scanned > maxRuntimeScan || !rp.Truncated {
		t.Errorf("wide-slice preview = {scanned:%d trunc:%v}, want scanned<=%d trunc=true", rp.Scanned, rp.Truncated, maxRuntimeScan)
	}

	// A NESTED wide map (not top-level) must also cap its keys and break its loop at
	// the budget, so the bound applies at every level, not only the root.
	nestedWide := map[string]any{}
	for i := 0; i < maxRuntimeScan*3; i++ {
		// Composite values so the walk emits several facts per key and the scan
		// budget is crossed mid-list (exercising the nested loop's break), not just
		// exhausted by the bounded key selection.
		nestedWide[fmt.Sprintf("n%06d", i)] = map[string]any{"a": i, "b": i}
	}
	np := runtimePreview(map[string]any{"outer": nestedWide})
	if np.Total != nil || !np.Truncated || np.Scanned > maxRuntimeScan {
		t.Errorf("nested wide-map preview = {total:%v scanned:%d trunc:%v}, want unknown total, scanned<=%d, truncated", np.Total, np.Scanned, np.Truncated, maxRuntimeScan)
	}
}

// capLen must count RUNES, not bytes: a multibyte string whose byte length exceeds
// the cap but whose rune count does not is returned unchanged; only a string with
// more RUNES than the cap is truncated with an ellipsis.
func TestCapLen(t *testing.T) {
	if got := capLen("abc", 8); got != "abc" {
		t.Errorf("short ASCII: got %q", got)
	}
	// maxRuntimeValueLen multibyte runes: byte length is larger than the cap, rune
	// count equals it, so the value is returned unchanged (no truncation).
	multi := strings.Repeat("é", maxRuntimeValueLen) // 2 bytes each
	if got := capRuntimeValue(multi); got != multi {
		t.Errorf("multibyte at cap must be unchanged: got %d runes", len([]rune(got)))
	}
	// One more rune than the cap: truncated to the cap plus an ellipsis.
	over := strings.Repeat("é", maxRuntimeValueLen+5)
	got := capRuntimeValue(over)
	if r := []rune(got); len(r) != maxRuntimeValueLen+1 || r[len(r)-1] != '…' {
		t.Errorf("multibyte over cap = %d runes, want %d + ellipsis", len(r), maxRuntimeValueLen)
	}
}

// A single finding with more than MaxEvidenceRefsPreview evidence refs (as an
// untrusted extension source could produce) must yield a bounded product finding:
// the true full count as Total, Count capped at the bound, Truncated=true
// (requirement, item 8). The raw finding.Finding stays unbounded on the low-level
// snapshot; only the product shape is bounded.
func TestProductFindingEvidenceRefsBounded(t *testing.T) {
	over := MaxEvidenceRefsPreview + 13
	refs := make([]finding.EvidenceRef, over)
	for i := range refs {
		refs[i] = finding.EvidenceRef{Source: fmt.Sprintf("src-%d", i), ObservedAt: "2030-01-01"}
	}
	pf := productFinding(finding.Finding{
		Code: finding.Code("DRIFT"), Severity: finding.SeverityUnknown,
		Category: finding.CategoryInconclusive, Subject: finding.SubjectRef{Kind: "interface", Name: "http"},
		ContractPath: "interfaces/http", Message: "unevaluable", EvidenceRefs: refs,
	})
	er := pf.EvidenceRefs
	if er.Total != over {
		t.Errorf("evidenceRefs.total = %d, want the true full count %d", er.Total, over)
	}
	if er.Count > MaxEvidenceRefsPreview || len(er.Items) > MaxEvidenceRefsPreview {
		t.Errorf("evidenceRefs.count = %d exceeds bound %d", er.Count, MaxEvidenceRefsPreview)
	}
	if !er.Truncated {
		t.Error("an over-cap evidence list must report truncated=true")
	}
	// Every scalar finding fact is preserved, and the finite severity enum carries
	// through (including unknown, which the engine really emits). Compare the whole
	// finding with the evidence preview substituted out, so only scalars are tested.
	got := pf
	got.EvidenceRefs = ProductEvidenceRefsPreview{}
	want := ProductFinding{
		Code: "DRIFT", Severity: ProductSeverityUnknown, Category: "Inconclusive",
		Subject:      ProductSubjectRef{Kind: "interface", Name: "http"},
		ContractPath: "interfaces/http", Message: "unevaluable",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("product finding scalar facts not preserved:\n got %+v\nwant %+v", got, want)
	}
	// A finding with no evidence refs yields an empty, non-nil, non-truncated preview.
	empty := productFinding(finding.Finding{Code: "X"}).EvidenceRefs
	if empty.Total != 0 || empty.Count != 0 || empty.Truncated || empty.Items == nil {
		t.Errorf("empty evidence preview = %+v, want empty non-nil non-truncated", empty)
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
	d, err := NewQuery(snap).EntityDetail(KindOwner, "team:team-x")
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
	rel := d.Service.Relationships
	if !rel.Truncated {
		t.Error("a service whose neighborhood truncated must report relationships.Truncated=true")
	}
	// The double-truncation regression: the preview must NOT claim a Total that is
	// only the count scanned before the neighborhood truncated. Since the true total
	// was bounded before it could be counted, Total must be UNKNOWN (nil), never
	// Total == Count with Truncated == true.
	if rel.Total != nil {
		t.Errorf("a truncated neighborhood must report an UNKNOWN relationship total, got %d (count=%d) - the scanned-before-truncation count is not the total", *rel.Total, rel.Count)
	}
}

// A service whose neighborhood is NOT truncated must report a KNOWN relationship
// total equal to the edges carried, proving the honest-total path (not only the
// unknown-total path) is exercised.
func TestServiceDetailRelationshipsKnownTotal(t *testing.T) {
	var revs []RawRevision
	dep := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "dep", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: dep, FS: fstest.MapFS{}}, Digest: "sha256:dep"})
	hub := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "hub", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Dependencies: []contract.Dependency{{Name: "dep", Ref: "oci://x/dep", Required: true, Compatibility: "^1.0.0"}}}
	revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: hub, FS: fstest.MapFS{}}, Digest: "sha256:hub"})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: revs}))
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewQuery(snap).EntityDetail(KindService, string(NewServiceKey("hub")))
	if err != nil {
		t.Fatal(err)
	}
	rel := d.Service.Relationships
	if rel.Truncated {
		t.Fatal("a small neighborhood must not truncate")
	}
	if rel.Total == nil || *rel.Total != rel.Count {
		t.Errorf("an untruncated neighborhood must report a known total == count, got total=%v count=%d", rel.Total, rel.Count)
	}
}
