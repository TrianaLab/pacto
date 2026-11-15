package evidenceingest

import (
	"context"
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// The Evidence Server re-evaluates the operator's EvidenceSet with the same evaluator, so a failing
// health check reaches the same HEALTH_FAILING finding and the same Unknown compliance on both sides.
func TestIngestHealthFailingMatchesOperator(t *testing.T) {
	c := contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "payments"},
		Capabilities: []contract.Capability{{Type: contract.CapabilityHealth}},
	}
	set := testEvidenceSet()
	set.Observations = append(set.Observations, evidence.NewCapabilityFailing(
		evidence.SubjectRef{Kind: "capability", Name: "health"}, set.Observations[0].Provenance))
	a, priv := newTestAcceptor(t, fakeResolver{c: c}, NewMemoryStore())
	rec, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", set))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	operator, cov := validation.Evaluate(c, set)
	if !reflect.DeepEqual(rec.Findings, operator) || !reflect.DeepEqual(rec.Coverage, cov) {
		t.Fatalf("ingest %+v %+v, operator %+v %+v", rec.Findings, rec.Coverage, operator, cov)
	}
	if len(rec.Findings) != 1 || rec.Findings[0].Code != finding.CodeHealthFailing {
		t.Fatalf("findings %+v, want one HEALTH_FAILING", rec.Findings)
	}
	if rec.Compliance != finding.StatusUnknown {
		t.Errorf("compliance %q, want Unknown", rec.Compliance)
	}
}
