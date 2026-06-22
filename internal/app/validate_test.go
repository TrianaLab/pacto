package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

func TestValidate_LocalValid(t *testing.T) {
	dir := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidate_LocalInvalid(t *testing.T) {
	dir := writeInvalidBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result")
	}
}

func TestValidate_ReadinessValid(t *testing.T) {
	dir := writeReadinessBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid readiness contract, got errors: %v", result.Errors)
	}
}

func TestValidate_ReadinessDuplicateID(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`pactoVersion: "1.2"
service:
  name: payment-api
  version: "1.4.0"
readiness:
  expires: "2026-12-31"
  checks:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 50
    - id: dashboard
      type: url
      status: done
      evidence: https://y
      weight: 50
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid result for duplicate readiness id")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_READINESS_ID" && e.Path == "readiness.checks[1].id" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DUPLICATE_READINESS_ID at readiness.checks[1].id, got %v", result.Errors)
	}
}

func TestValidate_ReadinessGate_FailsWhenStale(t *testing.T) {
	pinTime(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)) // security-review (2026-01-15) expired
	dir := writeReadinessBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir, Readiness: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Fatal("expected --readiness gate to fail on a stale contract")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "READINESS_GATE_UNMET" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected READINESS_GATE_UNMET, got %v", result.Errors)
	}
}

func TestValidate_ReadinessGate_MessageShape(t *testing.T) {
	dir := t.TempDir()
	// Scores 50 (one done w=50, one not-done w=30, one deferred w=20 excluded):
	// earned 50 of total 80 -> 63, below default minScore 100. Far-future expiry
	// so the failure is "score below gate", and the message lists every count.
	content := []byte(`pactoVersion: "1.2"
service:
  name: payment-api
  version: "1.4.0"
readiness:
  expires: "2099-12-31"
  checks:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 50
    - id: runbook
      type: document
      status: not-done
      evidence: docs/rb.md
      weight: 30
    - id: dr-plan
      type: document
      status: deferred
      evidence: docs/dr.md
      weight: 20
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir, Readiness: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Fatal("expected gate to fail (score below minScore)")
	}
	var msg string
	for _, e := range result.Errors {
		if e.Code == "READINESS_GATE_UNMET" {
			msg = e.Message
		}
	}
	if msg == "" {
		t.Fatalf("expected READINESS_GATE_UNMET, got %v", result.Errors)
	}
	for _, want := range []string{"score below gate", "done", "partial", "not-done", "deferred"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected message to contain %q, got %q", want, msg)
		}
	}
}

func TestValidate_ReadinessGate_PassesWhenCurrent(t *testing.T) {
	pinTime(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)) // both checks still current
	dir := writeReadinessBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir, Readiness: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected gate to pass when all current, got errors: %v", result.Errors)
	}
}

func TestValidate_ReadinessGate_OffByDefault(t *testing.T) {
	pinTime(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)) // stale, but gate not requested
	dir := writeReadinessBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid without --readiness (gate off), got errors: %v", result.Errors)
	}
}

func TestValidate_ReadinessGate_NoReadinessDeclared(t *testing.T) {
	dir := writeTestBundle(t) // no readiness section
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir, Readiness: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid when no readiness declared, got errors: %v", result.Errors)
	}
}

func TestValidate_DefaultPath(t *testing.T) {
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: ""})
	if err != nil {
		// This may fail because pacto.yaml doesn't exist in cwd, which is fine
		t.Skip("no pacto.yaml in cwd")
	}
	_ = result
}

func TestValidate_FileNotFound(t *testing.T) {
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: "/nonexistent/dir"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return a result with PARSE_ERROR
	if result.Valid {
		t.Error("expected invalid result for nonexistent directory")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error")
	}
}

func TestValidate_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidate_OCIRef_MissingPactoYAML(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			b := testBundle()
			b.FS = fstest.MapFS{} // empty FS, no pacto.yaml
			b.RawYAML = nil
			return b, nil
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err == nil {
		t.Error("expected error when pacto.yaml missing from OCI bundle FS")
	}
}

func TestValidate_NoRawYAMLNoFS(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			b := testBundle()
			b.RawYAML = nil
			b.FS = nil
			return b, nil
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err == nil {
		t.Error("expected error when bundle has no raw YAML and no FS")
	}
}

func TestValidate_OCIRef_NilStore(t *testing.T) {
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for nil store")
	}
}

func TestBundleResolverAdapter(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	adapter := &bundleResolverAdapter{svc: svc}
	bundle, err := adapter.ResolveBundle(context.Background(), "oci://ghcr.io/acme/svc:1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle == nil {
		t.Fatal("expected non-nil bundle")
	}
}
