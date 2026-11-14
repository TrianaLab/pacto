package validation

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

func healthOnly() contract.Contract {
	return contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1.0.0"},
		Capabilities: []contract.Capability{{Type: contract.CapabilityHealth}},
	}
}

func healthObservation(healthy bool) evidence.Observation {
	if healthy {
		return evidence.Observation{Kind: evidence.CapabilityObserved, Subject: sr("capability", "health"),
			Outcome: evidence.Observed, Value: []byte(`{"present":true,"healthy":true}`), Provenance: prov()}
	}
	return evidence.NewCapabilityFailing(sr("capability", "health"), prov())
}

func TestEvaluateHealthFailingIsUnknown(t *testing.T) {
	fs, cov := Evaluate(healthOnly(), es(healthObservation(false)))
	if len(fs) != 1 || fs[0].Code != finding.CodeHealthFailing || fs[0].Severity != finding.SeverityUnknown {
		t.Fatalf("want one unknown HEALTH_FAILING finding, got %+v", fs)
	}
	if fs[0].Category != finding.CategoryInconclusive || fs[0].ContractPath != "capabilities[type=health]" {
		t.Errorf("finding %+v", fs[0])
	}
	if cov.Required != 1 || cov.Evaluated != 0 {
		t.Errorf("coverage = %+v, want {Required: 1, Evaluated: 0}", cov)
	}
	if got := DeriveStatus(fs, cov); got != finding.StatusUnknown {
		t.Errorf("status %q, want Unknown", got)
	}
}

func TestEvaluateHealthyTrueSatisfied(t *testing.T) {
	fs, cov := Evaluate(healthOnly(), es(healthObservation(true)))
	if len(fs) != 0 {
		t.Fatalf("want no finding, got %+v", fs)
	}
	if cov.Required != 1 || cov.Evaluated != 1 {
		t.Errorf("coverage = %+v, want {Required: 1, Evaluated: 1}", cov)
	}
}

func TestEvaluateHealthFailingMessage(t *testing.T) {
	fs, _ := Evaluate(healthOnly(), es(healthObservation(false)))
	if len(fs) != 1 || !strings.Contains(fs[0].Message, `capability "health" answered but reports it is not healthy`) {
		t.Fatalf("message: %+v", fs)
	}
}

func TestEvaluateCollectionFailedStaysForCollectorErrors(t *testing.T) {
	failed := unobs(t, evidence.CapabilityObserved, "capability", "health", evidence.Failed)
	fs, cov := Evaluate(healthOnly(), es(failed))
	if len(fs) != 1 || fs[0].Code != finding.CodeCollectionFailed {
		t.Fatalf("a collector error stays COLLECTION_FAILED, got %+v", fs)
	}
	if cov.Required != 1 || cov.Evaluated != 0 {
		t.Errorf("coverage = %+v", cov)
	}
}
