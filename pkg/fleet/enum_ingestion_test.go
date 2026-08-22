package fleet

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/finding"
)

// These tests enforce the ingestion/domain invariant: every
// finite-value field stored in a FleetSnapshot that may reach a typed product
// response is canonical BEFORE it enters the product query layer. A custom Source
// is an extension seam; it can emit any string. The Build boundary must normalize
// out-of-vocabulary finite values (conservatively, to a degraded/unknown state),
// keep the usable record, and surface a structured limitation, so the runtime can
// never emit a value the generated OpenAPI enum forbids.

// findTarget returns the single target in a snapshot (the tests build exactly one).
func findTarget(t *testing.T, snap *FleetSnapshot) *TargetRecord {
	t.Helper()
	for _, tr := range snap.Targets {
		return tr
	}
	t.Fatal("snapshot has no target")
	return nil
}

func TestBuild_NormalizesNonCanonicalCompliance(t *testing.T) {
	src := NewMemorySource("ext", "custom", &Collection{
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "web", Service: "web", Compliance: "Banana"}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	tr := findTarget(t, snap)
	if tr.Compliance != StatusUnknown {
		t.Errorf("compliance = %q, want %q (non-canonical value normalized conservatively)", tr.Compliance, StatusUnknown)
	}
	if !ValidStatus(tr.Compliance) {
		t.Errorf("stored compliance %q is not canonical", tr.Compliance)
	}
	if !hasLimitation(tr.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("target should carry a SOURCE_RECORD_INVALID limitation: %+v", tr.Limitations)
	}
	if !hasLimitation(snap.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("snapshot should surface a SOURCE_RECORD_INVALID limitation: %+v", snap.Limitations)
	}
	if sourceStatus(snap, "ext") != SourcePartial {
		t.Errorf("source with an invalid record should be marked partial, got %q", sourceStatus(snap, "ext"))
	}
}

func TestBuild_NormalizesNonCanonicalFindingSeverity(t *testing.T) {
	src := NewMemorySource("ext", "custom", &Collection{
		Targets: []RawTarget{{
			Scope: "prod", Kind: "k8s", Name: "web", Service: "web", Compliance: StatusNonCompliant,
			Findings: []finding.Finding{
				{Severity: finding.Severity("critical"), Message: "a"},
				{Severity: finding.SeverityError, Message: "b"},
			},
		}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	tr := findTarget(t, snap)
	for i, f := range tr.Findings {
		if !validSeverity(f.Severity) {
			t.Errorf("finding[%d] severity %q is not canonical", i, f.Severity)
		}
	}
	if tr.Findings[0].Severity != finding.SeverityUnknown {
		t.Errorf("non-canonical severity normalized to %q, want %q", tr.Findings[0].Severity, finding.SeverityUnknown)
	}
	if tr.Findings[1].Severity != finding.SeverityError {
		t.Errorf("canonical severity was altered to %q", tr.Findings[1].Severity)
	}
	if !hasLimitation(snap.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("snapshot should surface a SOURCE_RECORD_INVALID limitation: %+v", snap.Limitations)
	}
}

func TestBuild_NormalizesNonCanonicalSourceStatus(t *testing.T) {
	src := NewMemorySource("ext", "custom", &Collection{
		Revisions: nil,
		State:     &SourceState{Status: SourceStatus("weird")},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	st := sourceStatus(snap, "ext")
	if !validSourceHealth(string(st)) {
		t.Errorf("source status %q is not a canonical source-health value", st)
	}
	if st == SourceAvailable {
		t.Errorf("a malformed source status must not be silently upgraded to available")
	}
	if !hasLimitation(snap.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("snapshot should surface a SOURCE_RECORD_INVALID limitation for the malformed status: %+v", snap.Limitations)
	}
}

// TestBuild_RecordProblemsDowngradeDeclaredAvailable proves a source that declares
// itself available but emitted an invalid record is downgraded to partial: it is not
// silently presented as healthy.
func TestBuild_RecordProblemsDowngradeDeclaredAvailable(t *testing.T) {
	src := NewMemorySource("ext", "custom", &Collection{
		State:   &SourceState{Status: SourceAvailable},
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "web", Service: "web", Compliance: "Banana"}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	if st := sourceStatus(snap, "ext"); st != SourcePartial {
		t.Errorf("a source that declared available but emitted an invalid record must be partial, got %q", st)
	}
}

// sourceStatus returns the status of the named source in a snapshot.
func sourceStatus(snap *FleetSnapshot, id string) SourceStatus {
	for _, s := range snap.Sources {
		if s.ID == id {
			return s.Status
		}
	}
	return SourceStatus("<missing>")
}
