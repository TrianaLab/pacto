package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
)

// writeReadinessBundle writes a pactoVersion 1.1 bundle with a readiness section
// (one current check, one expired check) and returns its directory.
func writeReadinessBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := []byte(`pactoVersion: "1.1"
service:
  name: payment-api
  version: "1.4.0"
  owner:
    team: payments-team
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
readiness:
  checks:
    - id: dashboard
      type: url
      evidence: https://grafana.company.com/payment-api
      weight: 60
      expires: "2026-12-31"
      description: Main production dashboard
    - id: security-review
      type: ticket
      evidence: SEC-1842
      weight: 40
      expires: "2026-01-15"
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
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
	if r.TotalWeight != 100 {
		t.Errorf("expected total weight 100, got %d", r.TotalWeight)
	}
	if r.CurrentWeight != 60 {
		t.Errorf("expected current weight 60, got %d", r.CurrentWeight)
	}
	if r.Score != 60 {
		t.Errorf("expected score 60, got %d", r.Score)
	}
	if r.MinScore != 100 {
		t.Errorf("expected default minScore 100, got %d", r.MinScore)
	}
	if r.Passing {
		t.Error("expected gate not passing (score 60 < minScore 100)")
	}
	if r.ExpiredCount != 1 || r.CurrentCount != 1 {
		t.Errorf("unexpected counts: current=%d expired=%d", r.CurrentCount, r.ExpiredCount)
	}
	if len(r.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(r.Checks))
	}
	if r.Checks[0].ID != "dashboard" || r.Checks[0].Status != "Current" {
		t.Errorf("unexpected first check: %+v", r.Checks[0])
	}
	if r.Checks[1].Status != "Expired" {
		t.Errorf("expected second check Expired, got %s", r.Checks[1].Status)
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
	if result.PactoVersion != "1.0" {
		t.Errorf("expected PactoVersion=1.0, got %s", result.PactoVersion)
	}
	if result.Runtime.WorkloadType != "service" {
		t.Errorf("expected WorkloadType=service, got %s", result.Runtime.WorkloadType)
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
	if iface.Type != "http" {
		t.Errorf("expected interface Type=http, got %s", iface.Type)
	}
	if iface.Port == nil || *iface.Port != 8080 {
		t.Errorf("expected interface Port=8080, got %v", iface.Port)
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
