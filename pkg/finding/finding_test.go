package finding

import "testing"

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
		Code:         CodeCapabilityNotObserved,
		Severity:     SeverityWarning,
		Category:     CategoryRuntimeDrift,
		Subject:      SubjectRef{Kind: "capability", Name: "health"},
		ContractPath: "capabilities[type=health]",
		Message:      "capability not observed",
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
