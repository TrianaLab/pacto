package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
)

// writeReadinessBundle writes a pactoVersion 1.2 bundle with a readiness section
// (one current check, one expired check) and returns its directory.
func writeReadinessBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := []byte(`pactoVersion: "1.2"
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
  expires: "2026-01-15"
  checks:
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
	// Pinned to 2026-06-08, after the assessment expires (2026-01-15), so the
	// whole assessment is expired: every in-scope check earns zero.
	if !r.Expired {
		t.Error("expected assessment to be expired")
	}
	if r.EarnedWeight != 0 {
		t.Errorf("expected earned weight 0 when expired, got %d", r.EarnedWeight)
	}
	if r.Score != 0 {
		t.Errorf("expected score 0 when expired, got %d", r.Score)
	}
	if r.MinScore != 100 {
		t.Errorf("expected default minScore 100, got %d", r.MinScore)
	}
	if r.Passing {
		t.Error("expected gate not passing (expired)")
	}
	if r.DoneCount != 2 || r.NotDoneCount != 0 {
		t.Errorf("unexpected counts: done=%d not-done=%d", r.DoneCount, r.NotDoneCount)
	}
	if len(r.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(r.Checks))
	}
	if r.Checks[0].ID != "dashboard" || r.Checks[0].Status != "done" {
		t.Errorf("unexpected first check: %+v", r.Checks[0])
	}
	if r.Checks[1].Status != "done" {
		t.Errorf("expected second check done, got %s", r.Checks[1].Status)
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
