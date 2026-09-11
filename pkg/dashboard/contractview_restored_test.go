package dashboard

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/diff"
	depgraph "github.com/trianalab/pacto/v3/pkg/graph"
)

// These cover the four deprecated mappers restored above: they have no caller
// left in Pacto, so a test is the only thing keeping them honest.

func TestDiffResultFromEngine(t *testing.T) {
	r := &diff.Result{
		Classification: diff.Breaking,
		Changes: []diff.Change{
			{
				Path:           "service.version",
				Type:           diff.Modified,
				OldValue:       "1.0.0",
				NewValue:       "2.0.0",
				Classification: diff.Breaking,
				Reason:         "major version bump",
			},
		},
	}

	dr := DiffResultFromEngine(Ref{Name: "svc", Version: "1.0.0"}, Ref{Name: "svc", Version: "2.0.0"}, r)
	if dr.Classification != "BREAKING" {
		t.Errorf("classification = %q, want BREAKING", dr.Classification)
	}
	if dr.From.Version != "1.0.0" || dr.To.Version != "2.0.0" {
		t.Errorf("refs = %v -> %v, want 1.0.0 -> 2.0.0", dr.From, dr.To)
	}
	if len(dr.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(dr.Changes))
	}
	if dr.Changes[0].Type != "modified" {
		t.Errorf("change type = %q, want modified", dr.Changes[0].Type)
	}
	if dr.Changes[0].Reason != "major version bump" {
		t.Errorf("reason = %q, want the engine's reason", dr.Changes[0].Reason)
	}
}

func TestComputeDiff(t *testing.T) {
	old := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "svc", Version: "1.0.0"},
	}}
	// Only the new bundle carries an FS, so both the set and the nil branch of
	// the FS pick-up run in one call.
	next := &contract.Bundle{
		Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "2.0.0"}},
		FS:       fstest.MapFS{},
	}

	dr := ComputeDiff(context.Background(), Ref{Name: "svc", Version: "1.0.0"}, Ref{Name: "svc", Version: "2.0.0"}, old, next)
	if dr == nil {
		t.Fatal("expected a diff result")
	}
	if dr.From.Version != "1.0.0" || dr.To.Version != "2.0.0" {
		t.Errorf("refs = %v -> %v, want 1.0.0 -> 2.0.0", dr.From, dr.To)
	}
	if dr.Classification == "" {
		t.Error("expected the engine classification to be carried through")
	}

	// Mirror image: the old bundle has the FS and the new one does not.
	old.FS = fstest.MapFS{}
	next.FS = nil
	if got := ComputeDiff(context.Background(), Ref{}, Ref{}, old, next); got == nil {
		t.Fatal("expected a diff result with the FS on the old side")
	}
}

func TestGraphFromResult_NilInputs(t *testing.T) {
	if GraphFromResult(nil) != nil {
		t.Error("expected nil for a nil result")
	}
	if GraphFromResult(&depgraph.Result{Root: nil}) != nil {
		t.Error("expected nil for a nil root")
	}
}

func TestGraphFromResult(t *testing.T) {
	r := &depgraph.Result{
		Root: &depgraph.Node{
			Name:    "svc",
			Version: "1.0.0",
			Ref:     "oci://example/svc:1.0.0",
			Dependencies: []depgraph.Edge{
				{
					Ref:           "dep-svc",
					Required:      true,
					Compatibility: "compatible",
					Shared:        true,
					Node:          &depgraph.Node{Name: "dep-svc", Version: "2.0.0"},
				},
				// An unresolved edge keeps mapGraphNode's nil-node branch live.
				{Ref: "missing-dep", Required: true, Error: "not found"},
			},
		},
		Cycles:    [][]string{{"a", "b", "a"}},
		Conflicts: []depgraph.Conflict{{Name: "dep-svc", Versions: []string{"1.0.0", "2.0.0"}}},
	}

	g := GraphFromResult(r)
	if g == nil {
		t.Fatal("expected a graph")
	}
	if g.Root.Name != "svc" || g.Root.Ref != "oci://example/svc:1.0.0" {
		t.Errorf("root = %+v, want the resolver's root", g.Root)
	}
	if len(g.Root.Dependencies) != 2 {
		t.Fatalf("dependencies = %d, want 2", len(g.Root.Dependencies))
	}
	resolved, missing := g.Root.Dependencies[0], g.Root.Dependencies[1]
	if resolved.Node == nil || resolved.Node.Name != "dep-svc" {
		t.Errorf("resolved edge node = %+v, want dep-svc", resolved.Node)
	}
	if !resolved.Shared || resolved.Compatibility != "compatible" {
		t.Errorf("resolved edge lost its flags: %+v", resolved)
	}
	if missing.Node != nil {
		t.Error("expected a nil node on the unresolved edge")
	}
	if missing.Error != "not found" {
		t.Errorf("edge error = %q, want 'not found'", missing.Error)
	}
	if len(g.Cycles) != 1 {
		t.Errorf("cycles = %d, want 1", len(g.Cycles))
	}
	if len(g.Conflicts) != 1 || g.Conflicts[0] != "dep-svc: [1.0.0 2.0.0]" {
		t.Errorf("conflicts = %v, want the formatted conflict", g.Conflicts)
	}
}

func TestComputeRuntimeDiff_MatchAndMismatch(t *testing.T) {
	tr := true

	rows := ComputeRuntimeDiff("service", &StateInfo{Type: "stateless"}, &ObservedRuntime{WorkloadKind: "Deployment"})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].Field != "Workload Type" || rows[0].Status != "match" {
		t.Errorf("workload row = %+v, want a match", rows[0])
	}
	if rows[1].ContractPath != "state.type" || rows[1].Status != "match" {
		t.Errorf("state row = %+v, want a match on state.type", rows[1])
	}

	rows = ComputeRuntimeDiff("service", &StateInfo{Type: "stateless"}, &ObservedRuntime{WorkloadKind: "StatefulSet", HasPVC: &tr})
	if rows[0].Status != "mismatch" {
		t.Errorf("workload row = %+v, want a mismatch", rows[0])
	}
	if rows[1].ObservedValue != "PVC" || rows[1].Status != "mismatch" {
		t.Errorf("state row = %+v, want a PVC mismatch", rows[1])
	}

}

func TestComputeRuntimeDiff_NothingToCompare(t *testing.T) {
	if rows := ComputeRuntimeDiff("", nil, nil); rows != nil {
		t.Errorf("expected nil with nothing declared and nothing observed, got %v", rows)
	}

	// A nil declared state leaves the state row with nothing to compare.
	rows := ComputeRuntimeDiff("service", nil, &ObservedRuntime{WorkloadKind: "Deployment"})
	if len(rows) != 2 {
		t.Fatalf("rows = %d with a nil state, want 2", len(rows))
	}
	if rows[1].Status != "skipped" {
		t.Errorf("state row = %+v, want skipped", rows[1])
	}

	// Nothing observed at all: the rows still exist, both skipped.
	rows = ComputeRuntimeDiff("service", &StateInfo{Type: "stateless"}, nil)
	if len(rows) != 2 {
		t.Fatalf("rows = %d with nothing observed, want 2", len(rows))
	}
	if rows[0].Status != "skipped" {
		t.Errorf("workload row = %+v, want skipped", rows[0])
	}
	if rows[1].ObservedValue != "stateless" || rows[1].Status != "match" {
		t.Errorf("state row = %+v, want stateless matched against stateless", rows[1])
	}

}

func TestStorageState(t *testing.T) {
	tr, fa := true, false
	cases := []struct {
		name string
		obs  *ObservedRuntime
		want string
	}{
		{"nothing observed", nil, ""},
		{"no volumes of either kind", &ObservedRuntime{HasPVC: &fa}, "stateless"},
		{"emptyDir only", &ObservedRuntime{HasEmptyDir: &tr}, "emptyDir"},
		{"both", &ObservedRuntime{HasPVC: &tr, HasEmptyDir: &tr}, "PVC, emptyDir"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := storageState(c.obs); got != c.want {
				t.Errorf("storageState = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMapWorkloadToDeclared(t *testing.T) {
	for in, want := range map[string]string{
		"service":   "Deployment",
		"Service":   "Deployment",
		"job":       "Job",
		"scheduled": "CronJob",
		"custom":    "custom",
		"":          "",
	} {
		if got := mapWorkloadToDeclared(in); got != want {
			t.Errorf("mapWorkloadToDeclared(%q) = %q, want %q", in, got, want)
		}
	}
}
