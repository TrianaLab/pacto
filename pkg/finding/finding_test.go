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
