package impact

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestProposalNeverClearsGate: a consumer whose range is unknown and whose
// record proposes a range admitting the new version, and a ref from the
// index, still reads unknown: a proposal never reaches a verdict.
func TestProposalNeverClearsGate(t *testing.T) {
	rec := (&contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: []contract.Inferred{
		{Field: "dependencies[notification-worker].compatibility", Value: "compatibility unknown", Proposed: "^2.0.0", Confidence: contract.ConfidenceLikely,
			Source: "snapshot:ghcr.io/acme/pacto-targets@sha256:" + strings.Repeat("a", 64)},
		{Field: "dependencies[notification-worker].ref", Value: "oci://ghcr.io/acme/notification-worker", Confidence: contract.ConfidenceLikely,
			Source: "index:ghcr.io/acme/pacto-index:0123456789abcdef-2.0.0"},
	}}).Marshal()
	byName := contract.Dependency{Name: "notification-worker", Required: true, Compatibility: contract.CompatibilityUnknown}
	res := breakWorker(workerFleet(t, newsletterRev("sha256:news", rec, byName)))
	if res.Classification != "BREAKING" || res.Gate != GateUnverified || len(res.Consumers) != 1 || res.Consumers[0].CompatibilityVerdict != CompatibilityUnknown {
		t.Fatalf("gate %s (%s), consumers %+v", res.Gate, res.GateReason, res.Consumers)
	}
}
