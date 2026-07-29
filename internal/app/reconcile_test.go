package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/reconcile"
)

func TestDeclaredFromSnapshot_SkipsNonDependencyAndFallsBack(t *testing.T) {
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{
		{Type: fleet.RelationshipDependency, FromService: "web", To: "payments", ToService: "payments-svc", Required: true},
		{Type: fleet.RelationshipDependency, FromService: "web", To: "cache"},        // unresolved -> fall back to To
		{Type: fleet.RelationshipConfigRef, FromService: "web", To: "shared-config"}, // not a dependency -> skipped
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
