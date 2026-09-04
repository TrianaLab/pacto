package fleet

import (
	"errors"
	"testing"
)

// Bounds that exist so a LARGE fleet cannot hand a consumer an answer whose size is
// the fleet's size: the graph traversal's node cap, the envelope caps applied to
// Meta (not just ProductMeta), and the per-service revision grouping that keeps a
// link pass proportional to the revisions a target could actually match.

func TestGraph_MaxNodesRejectsNegative(t *testing.T) {
	q := graphFleet(t)
	_, err := q.Graph(GraphQuery{Service: "g-a", Transitive: true, MaxNodes: -1})
	var iqe *InvalidQueryError
	if !errors.As(err, &iqe) || iqe.Field != "maxNodes" {
		t.Fatalf("negative maxNodes must be rejected as an invalid query, got %v", err)
	}
}

func TestGraph_MaxNodesTruncatesDeterministically(t *testing.T) {
	q := graphFleet(t)
	full, err := q.Graph(GraphQuery{Service: "g-a", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if full.Truncated {
		t.Fatalf("the whole fixture is far under the cap, so it must not report truncation")
	}
	if len(full.Nodes) < 2 {
		t.Fatalf("fixture too small to truncate: %d nodes", len(full.Nodes))
	}

	limit := len(full.Nodes) - 1
	res, err := q.Graph(GraphQuery{Service: "g-a", Transitive: true, MaxNodes: limit})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("an answer cut short by the node cap must say so")
	}
	if len(res.Nodes) != limit {
		t.Errorf("node cap not applied: %d nodes, cap %d", len(res.Nodes), limit)
	}
	// The dependency indexes are sorted at build, so the kept prefix is a fact
	// about the fleet and not about map iteration order.
	again, err := q.Graph(GraphQuery{Service: "g-a", Transitive: true, MaxNodes: limit})
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Nodes {
		if res.Nodes[i].Key != again.Nodes[i].Key {
			t.Fatalf("truncation is not deterministic: %v vs %v", res.Nodes, again.Nodes)
		}
	}
}

func TestGraph_MaxNodesClampedToCeiling(t *testing.T) {
	q := graphFleet(t)
	// A caller asking for more than the engine will ever draw gets the engine's
	// ceiling, not their number -- and, on a fixture this size, the whole answer.
	res, err := q.Graph(GraphQuery{Service: "g-a", Transitive: true, MaxNodes: MaxGraphNodes * 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Truncated {
		t.Error("a fixture far under the ceiling must not report truncation")
	}
}

func TestMeta_AppliesTheSameEnvelopeCapsAsProductMeta(t *testing.T) {
	q := productFleet(t)
	// The snapshot is shared by every answer, so an over-cap envelope has to be
	// bounded in BOTH the product meta and the plain one -- two answers from one
	// snapshot disagreeing about how much they left out is the failure this guards.
	var lims []Limitation
	for i := 0; i < MaxMetaLimitations+5; i++ {
		lims = append(lims, Limitation{Code: "L"})
	}
	var srcs []SourceState
	for i := 0; i < MaxMetaSources+5; i++ {
		srcs = append(srcs, SourceState{ID: "s", Status: SourceAvailable})
	}
	q.snap.Limitations = lims
	q.snap.Sources = srcs

	m := q.meta()
	if len(m.Limitations) != MaxMetaLimitations || !m.LimitationsTruncated {
		t.Errorf("Meta limitations unbounded: len=%d truncated=%v", len(m.Limitations), m.LimitationsTruncated)
	}
	if len(m.Sources) != MaxMetaSources || !m.SourcesTruncated {
		t.Errorf("Meta sources unbounded: len=%d truncated=%v", len(m.Sources), m.SourcesTruncated)
	}
}

func TestBoundLimitations_ExportedWrapperMatchesTheInternalCap(t *testing.T) {
	// Exported for pkg/impact, which carries the same snapshot-owned slice.
	var many []Limitation
	for i := 0; i < MaxMetaLimitations+3; i++ {
		many = append(many, Limitation{Code: "L"})
	}
	got, trunc := BoundLimitations(many)
	if len(got) != MaxMetaLimitations || !trunc {
		t.Errorf("BoundLimitations must apply MaxMetaLimitations: len=%d trunc=%v", len(got), trunc)
	}
	few, trunc := BoundLimitations([]Limitation{{Code: "X"}})
	if len(few) != 1 || trunc {
		t.Errorf("under-cap limitations verbatim: len=%d trunc=%v", len(few), trunc)
	}
}

func TestSnapshotForSerialization_IsTheLiveSnapshot(t *testing.T) {
	q := productFleet(t)
	// Deliberately NOT a copy: the one caller is the bulk-export handler, which
	// marshals it and drops it. Snapshot() stays the safe default for everyone else.
	if q.SnapshotForSerialization() != q.snap {
		t.Error("SnapshotForSerialization must hand back the snapshot itself, not a copy")
	}
	if q.Snapshot() == q.snap {
		t.Error("Snapshot must still defensively copy")
	}
}

func TestRevisionKeysByService_GroupsAndSortsEveryRevision(t *testing.T) {
	q := graphFleet(t)
	byService := revisionKeysByService(q.snap)

	total := 0
	for svc, keys := range byService {
		total += len(keys)
		for i := 1; i < len(keys); i++ {
			if keys[i-1] >= keys[i] {
				t.Fatalf("revision keys for %q are not sorted: %v", svc, keys)
			}
		}
		for _, k := range keys {
			if q.snap.Revisions[k].ServiceKey != svc {
				t.Fatalf("revision %q filed under the wrong service %q", k, svc)
			}
		}
	}
	if total != len(q.snap.Revisions) {
		t.Errorf("grouping dropped revisions: %d of %d", total, len(q.snap.Revisions))
	}
}

func TestMatchRevisionIn_AgreesWithTheWholeFleetMatcher(t *testing.T) {
	// The link pass groups once and reuses the groups; a single-target caller lets
	// matchRevision derive them. The two must never disagree about which revision a
	// target runs, or the fleet-wide answer and the one-off answer differ.
	snap := productFleet(t).snap
	if len(snap.Targets) == 0 {
		t.Fatal("fixture has no targets to match")
	}
	byService := revisionKeysByService(snap)
	for _, tgt := range snap.Targets {
		wantKey, wantKind := matchRevision(snap, tgt)
		gotKey, gotKind := matchRevisionIn(snap, byService[tgt.ServiceKey], tgt)
		if wantKey != gotKey || wantKind != gotKind {
			t.Errorf("target %q: grouped match %q/%q != ungrouped %q/%q", tgt.Key, gotKey, gotKind, wantKey, wantKind)
		}
	}
}
