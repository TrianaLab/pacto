package fleet

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestExternalDependencyIsNotAGap: an external dependency the fleet does not
// resolve carries its version and is never counted as unresolved, flagged for
// attention, explained as a gap or reported as declared-not-observed. A plain
// dependency that does not resolve still is. A fleet service that shares the
// external dependency's name is not it.
func TestExternalDependencyIsNotAGap(t *testing.T) {
	agent := &contract.Contract{PactoVersion: "2.1", Service: contract.Service{Name: "agent", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Readiness: readyContract(),
		Dependencies: []contract.Dependency{
			{Name: "model", External: true, Version: "claude-sonnet-4-5", Required: true},
			{Name: "ghost", Ref: "oci://x/ghost:1.0.0", Compatibility: "^1.0.0", Required: true},
		}}
	other := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "other", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Readiness: readyContract()}
	// An unrelated fleet service that happens to share the external name.
	model := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "model", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Readiness: readyContract()}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: agent, FS: fstest.MapFS{}}, Digest: "sha256:agent"},
			{Bundle: &contract.Bundle{Contract: other, FS: fstest.MapFS{}}, Digest: "sha256:other"},
			{Bundle: &contract.Bundle{Contract: model, FS: fstest.MapFS{}}, Digest: "sha256:model"},
		},
		Observed: []ObservedEdge{{From: "agent", To: "other", Count: 1, FirstSeen: fixedNow().Add(-time.Hour), LastSeen: fixedNow()}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, src)
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]Relationship{}
	for _, r := range snap.Relationships {
		if r.Provenance == ProvenanceDeclared {
			rels[r.To] = r
		}
	}
	if m := rels["model"]; !m.External || m.Version != "claude-sonnet-4-5" || m.Resolved || m.ToService != "" || m.Reconciliation != "" ||
		m.Reason != "an external dependency: this Pacto system does not deliver it" {
		t.Fatalf("model = %+v", m)
	}
	if g := rels["ghost"]; g.External || g.Reconciliation != ReconciliationDeclaredNotObserved || g.Reason != "no service in the fleet resolves this dependency ref" {
		t.Fatalf("ghost = %+v", g)
	}
	for k, deps := range snap.reverseDeps {
		if len(deps) > 0 {
			t.Fatalf("nothing depends on %s: %v", k, deps)
		}
	}
	checkExternalNotCounted(t, NewQuery(snap), rels["model"].FromService)
}

func checkExternalNotCounted(t *testing.T, q *Query, from ServiceKey) {
	t.Helper()
	if n := q.Overview().Summary.UnresolvedRelationships; n != 1 {
		t.Errorf("unresolved relationships = %d, want 1", n)
	}
	if n := len(q.unresolvedAttention()); n != 1 {
		t.Errorf("attention items = %d, want 1", n)
	}
	var sum ServiceSummary
	q.tallyServiceEdges(&sum, from)
	if sum.UnresolvedDeclared != 1 {
		t.Errorf("unresolved declared = %d, want 1", sum.UnresolvedDeclared)
	}
	res, err := q.Explain(string(from))
	if err != nil {
		t.Fatal(err)
	}
	gaps := 0
	for _, r := range res.Reasons {
		if r.Code == LimitationUnresolvedDep {
			gaps++
		}
	}
	if gaps != 1 {
		t.Errorf("explained gaps = %d, want 1: %+v", gaps, res.Reasons)
	}
}

func TestLockFromNoBundle(t *testing.T) {
	if lockFrom(RawRevision{}) != nil {
		t.Fatal("a revision with no bundle has no lock")
	}
}
