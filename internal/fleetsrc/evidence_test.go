package fleetsrc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvidenceSource_IDAndKind(t *testing.T) {
	if got := NewEvidenceSource("", "/x").ID(); got != "evidence" {
		t.Errorf("default id = %q, want evidence", got)
	}
	if got := NewEvidenceSource("custom", "/x").ID(); got != "custom" {
		t.Errorf("custom id = %q, want custom", got)
	}
	if got := NewEvidenceSource("", "/x").Kind(); got != "evidence" {
		t.Errorf("kind = %q, want evidence", got)
	}
}

const evidenceYAML = `targets:
  - scope: prod-eu
    kind: kubernetes-workload
    name: orders
    service: orders
    labels:
      env: prod
    requestedRef: oci://ghcr.io/acme/orders:1.0.0
    resolvedRef: oci://ghcr.io/acme/orders@sha256:abc
    digest: sha256:abc
    compliance: NonCompliant
    coverage:
      evaluated: 3
      required: 5
    findings:
      - code: STATELESS_PERSISTENT_CONFLICT
        severity: error
        category: StateMismatch
        subjectKind: service
        subjectName: orders
        message: drift detected
    observedRuntime:
      replicas: 3
    evidenceAt: 2026-07-01T00:00:00Z
    reconciledAt: 2026-07-02T00:00:00Z
state:
  status: stale
  message: cluster snapshot is old
`

func TestEvidenceSource_CollectYAML(t *testing.T) {
	path := writeFile(t, t.TempDir(), "evidence.yaml", evidenceYAML)
	col, err := NewEvidenceSource("", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(col.Targets))
	}
	tgt := col.Targets[0]
	assertOrdersTarget(t, tgt)
	if len(tgt.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(tgt.Findings))
	}
	assertDriftFinding(t, tgt.Findings[0])
	if col.State == nil || col.State.Status != fleet.SourceStale {
		t.Errorf("state = %+v, want stale", col.State)
	}
}

func assertOrdersTarget(t *testing.T, tgt fleet.RawTarget) {
	t.Helper()
	if tgt.Name != "orders" || tgt.Service != "orders" || tgt.Scope != "prod-eu" {
		t.Errorf("unexpected target identity: %+v", tgt)
	}
	if tgt.Labels["env"] != "prod" {
		t.Errorf("labels = %v", tgt.Labels)
	}
	if tgt.Compliance != "NonCompliant" {
		t.Errorf("compliance = %q", tgt.Compliance)
	}
	if tgt.Coverage == nil || tgt.Coverage.Evaluated != 3 || tgt.Coverage.Required != 5 {
		t.Errorf("coverage = %+v", tgt.Coverage)
	}
	if tgt.EvidenceAt == nil || tgt.ReconciledAt == nil {
		t.Errorf("evidenceAt/reconciledAt not parsed: %v %v", tgt.EvidenceAt, tgt.ReconciledAt)
	}
	if tgt.ObservedRuntime["replicas"] == nil {
		t.Errorf("observedRuntime = %v", tgt.ObservedRuntime)
	}
}

func assertDriftFinding(t *testing.T, f finding.Finding) {
	t.Helper()
	if string(f.Code) != "STATELESS_PERSISTENT_CONFLICT" || string(f.Severity) != "error" ||
		string(f.Category) != "StateMismatch" || f.Subject.Kind != "service" || f.Subject.Name != "orders" {
		t.Errorf("finding mapped wrong: %+v", f)
	}
}

func TestEvidenceSource_CollectJSON(t *testing.T) {
	// JSON is a YAML subset; an invalid state status falls back to available.
	body := `{"targets":[{"scope":"prod-us","kind":"k8s","name":"payments","service":"payments","compliance":"Unknown"}],"state":{"status":"bogus"}}`
	path := writeFile(t, t.TempDir(), "evidence.json", body)
	col, err := NewEvidenceSource("", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Targets) != 1 || col.Targets[0].Name != "payments" {
		t.Fatalf("targets = %+v", col.Targets)
	}
	if len(col.Targets[0].Findings) != 0 {
		t.Errorf("expected no findings, got %v", col.Targets[0].Findings)
	}
	if col.State == nil || col.State.Status != fleet.SourceAvailable {
		t.Errorf("invalid status should fall back to available, got %+v", col.State)
	}
}

func TestEvidenceSource_NoStateBlock(t *testing.T) {
	body := "targets:\n  - name: only\n    service: only\n"
	path := writeFile(t, t.TempDir(), "e.yaml", body)
	col, err := NewEvidenceSource("", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if col.State != nil {
		t.Errorf("expected nil state, got %+v", col.State)
	}
}

func TestEvidenceSource_MissingFile(t *testing.T) {
	_, err := NewEvidenceSource("", filepath.Join(t.TempDir(), "nope.yaml")).Collect(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEvidenceSource_MalformedYAML(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.yaml", "targets: [unterminated")
	_, err := NewEvidenceSource("", path).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "parse evidence") {
		t.Fatalf("err = %v, want parse evidence error", err)
	}
}

func TestEvidenceSource_ContextCancelled(t *testing.T) {
	path := writeFile(t, t.TempDir(), "e.yaml", "targets: []\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewEvidenceSource("", path).Collect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestToState_AllStatuses(t *testing.T) {
	cases := map[string]fleet.SourceStatus{
		"available":   fleet.SourceAvailable,
		"stale":       fleet.SourceStale,
		"partial":     fleet.SourcePartial,
		"unavailable": fleet.SourceUnavailable,
		"weird":       fleet.SourceAvailable, // invalid → fallback
		"":            fleet.SourceAvailable,
	}
	for in, want := range cases {
		if got := (stateFixture{Status: in}).toState().Status; got != want {
			t.Errorf("toState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToRaw_NoFindings(t *testing.T) {
	raw := (targetFixture{Name: "n", Service: "s"}).toRaw()
	if raw.Findings != nil {
		t.Errorf("expected nil findings, got %v", raw.Findings)
	}
}
