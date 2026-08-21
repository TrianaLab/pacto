package fleet

import (
	"context"
	"fmt"
	"testing"
)

// hugeRuntimeTarget builds a snapshot with one target whose observed-runtime map is
// `width` keys wide, plus a companion small target, so a test can compare the
// query-time work between a pathologically wide source and a trivial one.
func runtimeSnapshot(width int) (*FleetSnapshot, string) {
	raw := make(map[string]any, width)
	for i := 0; i < width; i++ {
		raw[fmt.Sprintf("k%08d", i)] = i
	}
	src := NewMemorySource("k8s", "kubernetes", &Collection{Targets: []RawTarget{{
		Scope: "prod", Kind: "k8s", Name: "wide", Service: "svc",
		Compliance: StatusCompliant, ObservedRuntime: raw,
	}}})
	snap, _ := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	return snap, string(NewTargetKey("prod", "k8s", "wide"))
}

// TestRuntimePreview_QueryWorkIsBounded proves the product query does O(fixed bound)
// work, not O(original raw map width): the observed-runtime bound lives at ingestion,
// so querying a target whose source map is orders of magnitude above the product cap
// allocates essentially the same as querying a trivial one.
func TestRuntimePreview_QueryWorkIsBounded(t *testing.T) {
	smallSnap, smallKey := runtimeSnapshot(4)
	hugeSnap, hugeKey := runtimeSnapshot(200_000)

	smallQ := NewQuery(smallSnap)
	hugeQ := NewQuery(hugeSnap)

	// Warm both paths once so lazy one-time allocations do not skew the measurement.
	if _, err := hugeQ.EntityDetail(KindTarget, hugeKey); err != nil {
		t.Fatal(err)
	}

	smallAlloc := bytesAllocated(func() {
		if _, err := smallQ.EntityDetail(KindTarget, smallKey); err != nil {
			t.Fatal(err)
		}
	})
	var det *EntityDetail
	hugeAlloc := bytesAllocated(func() {
		d, err := hugeQ.EntityDetail(KindTarget, hugeKey)
		if err != nil {
			t.Fatal(err)
		}
		det = d
	})

	rt := det.Target.ObservedRuntime
	if rt.Count > MaxDetailPreview {
		t.Errorf("observed-runtime preview emitted %d facts, want <= %d", rt.Count, MaxDetailPreview)
	}
	if !rt.Truncated {
		t.Error("a 200k-wide source runtime must report Truncated")
	}
	if rt.Total != nil {
		t.Errorf("Total must be omitted (unknown) when the walk stopped early, got %d", *rt.Total)
	}
	// The query touches only bounded state, so a 50,000x wider source must not make
	// the query allocate materially more. (Before the ingestion bound, the query both
	// re-flattened AND deep-cloned the raw map, so this was tens of MB.)
	if hugeAlloc > smallAlloc+(1<<19) {
		t.Errorf("query over a 200k-wide runtime allocated %d bytes vs %d for a 4-wide one; query work is not bounded", hugeAlloc, smallAlloc)
	}
}
