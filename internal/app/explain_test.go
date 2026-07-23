package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

// writeReadinessBundle writes a pactoVersion 1.2 bundle with a readiness section
// (one current check, one expired check) and returns its directory.
func writeReadinessBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := []byte(`pactoVersion: "2.0"
service:
  name: payment-api
  version: "1.4.0"
  owner:
    team: payments-team
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
capabilities:
  - type: health
readiness:
  expires: "2026-01-15"
  history:
    - date: "2026-06-21"
      version: "2.1.0"
      author: ed
      description: initial assessment
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://grafana.company.com/payment-api
      weight: 60
      description: Main production dashboard
    - id: security-review
      type: ticket
      status: done
      evidence: SEC-1842
      weight: 40
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), []byte("openapi: \"3.0.0\"\ninfo:\n  title: API\n  version: 1.0.0\npaths: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pinTime pins the readiness clock for the duration of a test.
func pinTime(t *testing.T, at time.Time) {
	t.Helper()
	old := timeNow
	timeNow = func() time.Time { return at }
	t.Cleanup(func() { timeNow = old })
}

func TestExplain_WithReadiness(t *testing.T) {
	pinTime(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	path := writeReadinessBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Readiness == nil {
		t.Fatal("expected readiness summary to be present")
	}
	r := result.Readiness
	type field struct {
		label string
		got   any
		want  any
	}
	fields := []field{
		{"totalWeight", r.TotalWeight, 100},
		{"expired", r.Expired, true},
		{"earnedWeight", r.EarnedWeight, 0},
		{"score", r.Score, 0},
		{"minScore", r.MinScore, 100},
		{"passing", r.Passing, false},
		{"doneCount", r.DoneCount, 2},
		{"notDoneCount", r.NotDoneCount, 0},
		{"checks length", len(r.Checks), 2},
	}
	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("%s: got %v, want %v", f.label, f.got, f.want)
		}
	}
	if len(r.Checks) > 0 && (r.Checks[0].ID != "dashboard" || r.Checks[0].Status != "done") {
		t.Errorf("unexpected first check: %+v", r.Checks[0])
	}
	if len(r.Checks) > 1 && r.Checks[1].Status != "done" {
		t.Errorf("expected second check done, got %s", r.Checks[1].Status)
	}
	if len(r.Revisions) != 1 || r.Revisions[0].Author != "ed" || r.Revisions[0].Version != "2.1.0" {
		t.Errorf("expected mapped revision history, got %+v", r.Revisions)
	}
}

func TestExplain_WithoutReadiness(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Readiness != nil {
		t.Errorf("expected no readiness summary, got %+v", result.Readiness)
	}
}

func TestExplain_Local(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test-svc" {
		t.Errorf("expected Name=test-svc, got %s", result.Name)
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected Version=1.0.0, got %s", result.Version)
	}
	if result.PactoVersion != "2.0" {
		t.Errorf("expected PactoVersion=2.0, got %s", result.PactoVersion)
	}
	if result.Workload != "service" {
		t.Errorf("expected Workload=service, got %s", result.Workload)
	}
}

func TestExplain_WithInterfaces(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(result.Interfaces))
	}
	iface := result.Interfaces[0]
	if iface.Name != "api" {
		t.Errorf("expected interface Name=api, got %s", iface.Name)
	}
	if iface.Type != "openapi" {
		t.Errorf("expected interface Type=openapi, got %s", iface.Type)
	}
	if iface.Ref != "openapi.yaml" {
		t.Errorf("expected interface Ref=openapi.yaml, got %s", iface.Ref)
	}
}

func TestExplain_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test-svc" {
		t.Errorf("expected Name=test-svc, got %s", result.Name)
	}
}

func TestExplain_WithDependencies(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			b := testBundle()
			b.Contract.Dependencies = []contract.Dependency{
				{Ref: "ghcr.io/acme/dep:1.0.0", Required: true, Compatibility: "^1.0.0"},
			}
			return b, nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Dependencies))
	}
	dep := result.Dependencies[0]
	if dep.Ref != "ghcr.io/acme/dep:1.0.0" {
		t.Errorf("expected Ref=ghcr.io/acme/dep:1.0.0, got %s", dep.Ref)
	}
	if !dep.Required {
		t.Error("expected Required=true")
	}
}

func TestExplain_NotFound(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Explain(context.Background(), ExplainOptions{Path: "/nonexistent/pacto.yaml"})
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}
