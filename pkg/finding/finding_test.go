package finding

import (
	"encoding/json"
	"testing"
)

func TestFinding_Fields(t *testing.T) {
	f := Finding{
		Code:         CodeStatelessPersistent,
		Severity:     SeverityError,
		Category:     CategoryStateMismatch,
		Subject:      SubjectRef{Kind: "service", Name: "payments"},
		ContractPath: "state.persistence.durability",
		Message:      "stateless services must use ephemeral durability",
	}
	if f.Severity != SeverityError {
		t.Fatalf("severity = %q, want error", f.Severity)
	}
	if f.Category != CategoryStateMismatch {
		t.Fatalf("category = %q", f.Category)
	}
}

func TestFinding_EvidenceRefs(t *testing.T) {
	f := Finding{
		Code:         CodeCapabilityAbsent,
		Severity:     SeverityError,
		Category:     CategoryRuntimeDrift,
		Subject:      SubjectRef{Kind: "capability", Name: "health"},
		ContractPath: "capabilities[type=health]",
		Message:      "capability absent at runtime",
		EvidenceRefs: []EvidenceRef{
			{Source: "kubernetes", ObservedAt: "2026-07-24T12:00:00Z"},
		},
	}
	if len(f.EvidenceRefs) != 1 {
		t.Fatalf("expected 1 evidence ref, got %d", len(f.EvidenceRefs))
	}
	if f.EvidenceRefs[0].Source != "kubernetes" {
		t.Errorf("source = %q, want kubernetes", f.EvidenceRefs[0].Source)
	}
}

// An evidence ref with no matched observation must serialize exactly as it did before Collector and
// DetectedAt existed.
func TestEvidenceRef_ProvenanceOmittedWhenEmpty(t *testing.T) {
	data, err := json.Marshal(EvidenceRef{Source: "k8s", ObservedAt: "2026-10-17T00:00:00Z"})
	if err != nil || string(data) != `{"Source":"k8s","ObservedAt":"2026-10-17T00:00:00Z"}` {
		t.Errorf("set-level ref = %s (%v)", data, err)
	}
	data, _ = json.Marshal(EvidenceRef{Source: "k8s", ObservedAt: "t", Collector: "ebpf", DetectedAt: "d"})
	if string(data) != `{"Source":"k8s","ObservedAt":"t","Collector":"ebpf","DetectedAt":"d"}` {
		t.Errorf("observation ref = %s", data)
	}
}
