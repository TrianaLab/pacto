package fleetsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// writeFixture writes body to a temp file and returns its path.
func writeFixture(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTargetStateFileSource_IDAndKind(t *testing.T) {
	if got := NewTargetStateFileSource("", "/x").ID(); got != "target-state" {
		t.Errorf("default id = %q, want target-state", got)
	}
	if got := NewTargetStateFileSource("custom", "/x").ID(); got != "custom" {
		t.Errorf("custom id = %q, want custom", got)
	}
	if got := NewTargetStateFileSource("", "/x").Kind(); got != "target-state" {
		t.Errorf("kind = %q, want target-state", got)
	}
}

// validFixture is a schema-valid file with a rich target (findings, coverage,
// evidence, labels, observed runtime) and a minimal one (empty compliance).
const validFixture = `schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - scope: prod
    kind: k8s
    name: orders
    service: orders
    requestedRef: oci://x/orders:1.0.0
    resolvedRef: oci://x/orders@sha256:o
    digest: sha256:o
    compliance: NonCompliant
    coverage: { evaluated: 2, required: 3 }
    observedRuntime: { replicas: 3 }
    evidenceAt: 2026-07-29T09:40:00Z
    reconciledAt: 2026-07-29T09:41:00Z
    labels: { env: prod }
    findings:
      - code: DRIFT
        severity: error
        category: RuntimeDrift
        subjectKind: service
        subjectName: orders
        message: drift detected
  - name: minimal
    service: minimal
state:
  status: available
`

func TestTargetStateFileSource_ValidYAML(t *testing.T) {
	path := writeFixture(t, "targets.yaml", validFixture)
	col, err := NewTargetStateFileSource("", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Targets) != 2 {
		t.Fatalf("want 2 targets, got %d", len(col.Targets))
	}
	if len(col.Limitations) != 0 {
		t.Errorf("valid fixture should have no limitations: %+v", col.Limitations)
	}
	if col.State == nil || col.State.Status != fleet.SourceAvailable {
		t.Errorf("state block should map to an available source state: %+v", col.State)
	}
	orders := findTarget(t, col.Targets, "orders")
	if orders.Compliance != fleet.StatusNonCompliant || orders.Digest != "sha256:o" {
		t.Errorf("orders mapped wrong: %+v", orders)
	}
	if orders.Coverage == nil || orders.Coverage.Required != 3 {
		t.Errorf("coverage not mapped: %+v", orders.Coverage)
	}
	if len(orders.Findings) != 1 || orders.Findings[0].Code != finding.Code("DRIFT") {
		t.Errorf("finding not mapped: %+v", orders.Findings)
	}
	if orders.Findings[0].Subject.Name != "orders" || orders.Findings[0].Message != "drift detected" {
		t.Errorf("finding subject/message not mapped: %+v", orders.Findings[0])
	}
	// The minimal entry has no findings → toRaw's nil-findings branch.
	if fm := findTarget(t, col.Targets, "minimal"); len(fm.Findings) != 0 {
		t.Errorf("minimal target should carry no findings: %+v", fm.Findings)
	}
}

func TestTargetStateFileSource_ValidJSON(t *testing.T) {
	body := `{"schemaVersion":"pacto.dev/fleet-targets/v1","targets":[{"service":"s","name":"n","compliance":"Compliant"}]}`
	path := writeFixture(t, "targets.json", body)
	col, err := NewTargetStateFileSource("", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect JSON: %v", err)
	}
	if len(col.Targets) != 1 || col.Targets[0].Service != "s" {
		t.Errorf("JSON fixture not parsed: %+v", col.Targets)
	}
}

func TestTargetStateFileSource_SchemaVersionErrors(t *testing.T) {
	cases := map[string]string{
		"missing schemaVersion": "targets: []\n",
		"wrong schemaVersion":   "schemaVersion: pacto.dev/other/v9\ntargets: []\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t, "t.yaml", body)
			if _, err := NewTargetStateFileSource("", path).Collect(context.Background()); err == nil {
				t.Error("expected a schema-version error")
			}
		})
	}
}

func TestTargetStateFileSource_UnknownFieldRejected(t *testing.T) {
	body := "schemaVersion: pacto.dev/fleet-targets/v1\ntargets: []\nbogusTopLevel: nope\n"
	path := writeFixture(t, "t.yaml", body)
	if _, err := NewTargetStateFileSource("", path).Collect(context.Background()); err == nil {
		t.Error("strict decoding should reject an unknown field")
	}
}

func TestTargetStateFileSource_SecondDocumentRejected(t *testing.T) {
	body := "schemaVersion: pacto.dev/fleet-targets/v1\ntargets: []\n---\nschemaVersion: pacto.dev/fleet-targets/v1\ntargets: []\n"
	path := writeFixture(t, "t.yaml", body)
	if _, err := NewTargetStateFileSource("", path).Collect(context.Background()); err == nil {
		t.Error("a second YAML document must be rejected (exactly one document)")
	}
}

func TestTargetStateFileSource_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	if _, err := NewTargetStateFileSource("", path).Collect(context.Background()); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestTargetStateFileSource_Malformed(t *testing.T) {
	path := writeFixture(t, "t.yaml", "::: not yaml [")
	if _, err := NewTargetStateFileSource("", path).Collect(context.Background()); err == nil {
		t.Error("expected an error for malformed YAML")
	}
}

func TestTargetStateFileSource_ContextCancelled(t *testing.T) {
	path := writeFixture(t, "t.yaml", validFixture)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewTargetStateFileSource("", path).Collect(ctx); err == nil {
		t.Error("expected a context-cancelled error")
	}
}

// invalidEntriesFixture has one valid target plus one of every invalidity the
// per-entry validator rejects, so each is skipped with a limitation.
const invalidEntriesFixture = `schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - service: good
    name: good
    compliance: Compliant
  - name: no-service
  - service: no-name
  - service: s
    name: bad-compliance
    compliance: Bogus
  - service: s
    name: neg-coverage
    coverage: { evaluated: -1, required: 0 }
  - service: s
    name: no-finding-code
    findings:
      - severity: error
  - service: s
    name: bad-severity
    findings:
      - code: X
        severity: critical
`

func TestTargetStateFileSource_InvalidEntriesSkipped(t *testing.T) {
	path := writeFixture(t, "t.yaml", invalidEntriesFixture)
	col, err := NewTargetStateFileSource("ts", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Targets) != 1 || col.Targets[0].Name != "good" {
		t.Fatalf("only the valid entry should be kept, got %+v", col.Targets)
	}
	if len(col.Limitations) != 6 {
		t.Fatalf("want 6 SOURCE_RECORD_INVALID limitations, got %d: %+v", len(col.Limitations), col.Limitations)
	}
	for _, l := range col.Limitations {
		if l.Code != fleet.LimitationSourceRecordInvalid || l.Source != "ts" {
			t.Errorf("unexpected limitation: %+v", l)
		}
	}
}

func TestTargetStateFileSource_TooManyTargets(t *testing.T) {
	var b strings.Builder
	b.WriteString("schemaVersion: pacto.dev/fleet-targets/v1\ntargets:\n")
	for i := 0; i <= maxTargetFixtures; i++ { // maxTargetFixtures+1 entries
		b.WriteString("  - {service: s, name: n}\n")
	}
	path := writeFixture(t, "big.yaml", b.String())
	if _, err := NewTargetStateFileSource("", path).Collect(context.Background()); err == nil {
		t.Errorf("expected an error when targets exceed %d", maxTargetFixtures)
	}
}

func TestStateFixtureToState(t *testing.T) {
	cases := map[string]fleet.SourceStatus{
		"available":   fleet.SourceAvailable,
		"stale":       fleet.SourceStale,
		"partial":     fleet.SourcePartial,
		"unavailable": fleet.SourceUnavailable,
		"weird":       fleet.SourceAvailable, // unknown → available fallback
		"":            fleet.SourceAvailable,
	}
	for status, want := range cases {
		if got := (stateFixture{Status: status}).toState().Status; got != want {
			t.Errorf("toState(%q) = %q, want %q", status, got, want)
		}
	}
}

func findTarget(t *testing.T, targets []fleet.RawTarget, name string) fleet.RawTarget {
	t.Helper()
	for _, tg := range targets {
		if tg.Name == name {
			return tg
		}
	}
	t.Fatalf("target %q not found in %+v", name, targets)
	return fleet.RawTarget{}
}
