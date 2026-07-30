package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/otelobserver"
	"github.com/trianalab/pacto/v3/pkg/reconcile"
)

func TestObservedFromEdges_DomainScopedAndUnresolved(t *testing.T) {
	// "payments" exists in both eu and us (globally ambiguous); "web" is unique.
	snap := &fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{
		fleet.NewServiceKeyDomain("eu", "web"):      {Name: "web"},
		fleet.NewServiceKeyDomain("eu", "payments"): {Name: "payments"},
		fleet.NewServiceKeyDomain("us", "payments"): {Name: "payments"},
	}}
	edges := []otelobserver.Edge{
		{From: "web", To: "payments", Count: 5}, // caller unique, callee resolves within eu
		{From: "web", To: "external", Count: 2}, // caller unique, callee not a fleet service -> bare name
		{From: "payments", To: "x", Count: 1},   // caller ambiguous -> unresolved
		{From: "ghost", To: "y", Count: 3},      // caller unknown -> unresolved
	}
	observed, unresolved := observedFromEdges(snap, edges)

	wantObserved := []reconcile.Observed{
		{Service: "eu/web", Dependency: "eu/payments", Count: 5},
		{Service: "eu/web", Dependency: "external", Count: 2},
	}
	if !reflect.DeepEqual(observed, wantObserved) {
		t.Errorf("observed = %+v, want %+v", observed, wantObserved)
	}
	wantUnresolved := []reconcile.Unresolved{
		{Service: "payments", Dependency: "x", Count: 1, Reason: "ambiguous"},
		{Service: "ghost", Dependency: "y", Count: 3, Reason: "unknown"},
	}
	if !reflect.DeepEqual(unresolved, wantUnresolved) {
		t.Errorf("unresolved = %+v, want %+v", unresolved, wantUnresolved)
	}
}

func TestDeclaredFromSnapshot_SkipsNonDependencyAndFallsBack(t *testing.T) {
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{
		{Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, FromService: "web", To: "payments", ToService: "payments-svc", Required: true},
		{Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, FromService: "web", To: "cache"},        // unresolved -> fall back to To
		{Type: fleet.RelationshipConfigRef, Provenance: fleet.ProvenanceDeclared, FromService: "web", To: "shared-config"}, // not a dependency -> skipped
	}}
	got := declaredFromSnapshot(snap)
	want := []reconcile.Declared{
		{Service: "web", Dependency: "payments-svc", Required: true},
		{Service: "web", Dependency: "cache"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("declaredFromSnapshot = %+v, want %+v", got, want)
	}
}

// A snapshot built with an observation source carries observed dependency edges
// (Provenance=observed) in Relationships alongside declared ones. Only the
// contract-declared edges may enter the declared reconciliation input; folding
// an observed edge into it makes reconciliation report a shadow dependency as a
// healthy match (review section S2).
func TestDeclaredFromSnapshot_ExcludesObservedProvenance(t *testing.T) {
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{
		{Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, FromService: "eu/a", ToService: "eu/b"},
		{Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved, FromService: "eu/a", ToService: "eu/b"},
		{Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved, FromService: "eu/a", ToService: "eu/c"},
	}}
	got := declaredFromSnapshot(snap)
	want := []reconcile.Declared{{Service: "eu/a", Dependency: "eu/b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("declaredFromSnapshot = %+v, want only the declared edge %+v", got, want)
	}
}

func writeLocalBundleWithDeps(t *testing.T, dir, name string, deps ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"1.0.0\"\n"
	if len(deps) > 0 {
		body += "dependencies:\n"
		for _, d := range deps {
			body += "  - name: " + d + "\n    ref: \"oci://example.com/" + d + ":1.0.0\"\n    required: true\n    compatibility: \"^1.0.0\"\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const reconcileTrace = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
  "scopeSpans":[{"spans":[
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]},
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"shadow"}}]}
  ]}]}]}`

// TestFleet_TraceFilesFoldObserved proves the real observation pipeline: a
// --traces file becomes an observation source whose edges Build folds into the
// snapshot as domain-qualified observed relationships.
func TestFleet_TraceFilesFoldObserved(t *testing.T) {
	root := t.TempDir()
	writeLocalBundleWithDeps(t, filepath.Join(root, "web"), "web")
	writeLocalBundleWithDeps(t, filepath.Join(root, "payments"), "payments")
	tf := filepath.Join(t.TempDir(), "traces.json")
	if err := os.WriteFile(tf, []byte(reconcileTrace), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := NewService(nil, nil).Fleet(context.Background(), FleetOptions{
		LocalRoots: []string{root},
		TraceFiles: []string{tf},
	})
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	found := false
	for _, r := range snap.Relationships {
		if r.Provenance == fleet.ProvenanceObserved && string(r.FromService) == "web" && string(r.ToService) == "payments" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an observed web->payments relationship folded from --traces, got %+v", snap.Relationships)
	}
}

func TestService_Reconcile(t *testing.T) {
	root := t.TempDir()
	writeLocalBundleWithDeps(t, filepath.Join(root, "web"), "web", "payments", "cache")

	svc := NewService(nil, nil)
	rep, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Fleet:  FleetOptions{LocalRoots: []string{root}},
		Traces: []byte(reconcileTrace),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.Summary != (reconcile.Summary{Matched: 1, DeclaredNotObserved: 1, ObservedNotDeclared: 1}) {
		t.Fatalf("summary = %+v", rep.Summary)
	}
	byStatus := map[reconcile.Status]string{}
	for _, e := range rep.Entries {
		byStatus[e.Status] = e.Dependency
	}
	if byStatus[reconcile.StatusMatched] != "payments" ||
		byStatus[reconcile.StatusDeclaredNotObserved] != "cache" ||
		byStatus[reconcile.StatusObservedNotDeclared] != "shadow" {
		t.Errorf("entries mismapped: %+v", rep.Entries)
	}
}

const reconcileTraceABC = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"a"}}]},
  "scopeSpans":[{"spans":[
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"b"}}]},
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"c"}}]}
  ]}]}]}`

// End-to-end guard for the S2 contamination: when the snapshot itself is built
// with an observation source (opts.Fleet.TraceFiles), its observed edges are
// folded into snap.Relationships. Reconciliation must still treat only the
// contract-declared edge (a->b) as declared, so the observed-only edge a->c is
// reported observed-not-declared, never a match.
func TestService_Reconcile_ObservedInSnapshotStaysUndeclared(t *testing.T) {
	root := t.TempDir()
	writeLocalBundleWithDeps(t, filepath.Join(root, "a"), "a", "b") // a declares only b
	writeLocalBundleWithDeps(t, filepath.Join(root, "b"), "b")
	writeLocalBundleWithDeps(t, filepath.Join(root, "c"), "c")
	tf := filepath.Join(t.TempDir(), "traces.json")
	if err := os.WriteFile(tf, []byte(reconcileTraceABC), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := NewService(nil, nil).Reconcile(context.Background(), ReconcileOptions{
		Fleet:  FleetOptions{LocalRoots: []string{root}, TraceFiles: []string{tf}},
		Traces: []byte(reconcileTraceABC),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.Summary != (reconcile.Summary{Matched: 1, ObservedNotDeclared: 1}) {
		t.Fatalf("summary = %+v, want matched=1 declared-not-observed=0 observed-not-declared=1", rep.Summary)
	}
	for _, e := range rep.Entries {
		if e.Status == reconcile.StatusMatched && e.Dependency != "" && e.Dependency[len(e.Dependency)-1] == 'c' {
			t.Errorf("observed-only dependency ...->c must not be a match: %+v", e)
		}
	}
}

func TestService_Reconcile_BadTraces(t *testing.T) {
	svc := NewService(nil, nil)
	if _, err := svc.Reconcile(context.Background(), ReconcileOptions{Traces: []byte("{bad")}); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestService_Reconcile_FleetError(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Reconcile(context.Background(), ReconcileOptions{
		Traces: []byte(`{"resourceSpans":[]}`),
		Fleet: FleetOptions{
			TargetStateFiles: []string{filepath.Join(t.TempDir(), "missing.yaml")},
			DisallowPartial:  true,
		},
	})
	if err == nil {
		t.Fatal("expected fleet build error")
	}
}
