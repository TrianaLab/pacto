package impact

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// A draft of newsletter requests notification-worker ^2.0.0, a range that
// admits the breaking 2.0.0, in its contract and its record. The draft is a
// request waiting for a person: impact reads the published newsletter's
// declared unknown range and stays unverified.
func TestDraftRequestedRangeNeverDeclared(t *testing.T) {
	const field, requested = "dependencies[notification-worker].compatibility", "^2.0.0"
	draft := (&contract.InferredRecord{Format: contract.InferredRecordFormat,
		Fields: []contract.Inferred{{Field: field, Value: "compatibility unknown", Proposed: requested, Confidence: contract.ConfidenceLikely,
			Source: "snapshot:ghcr.io/acme/pacto-targets@sha256:" + strings.Repeat("a", 64)}},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDraft,
			Asker:     contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent},
			Requested: []contract.InferredAcceptance{{Field: field, Range: requested}}},
	}).Marshal()
	if _, err := contract.ParseInferredRecord(draft); err != nil {
		t.Fatalf("the draft fixture must parse: %v", err)
	}
	published := newsletterRev("sha256:news", nil, contract.Dependency{Name: "notification-worker", Required: true, Compatibility: contract.CompatibilityUnknown})
	drafted := newsletterRev("sha256:draft", draft, contract.Dependency{Name: "notification-worker", Required: true, Compatibility: requested})
	drafted.Bundle.Contract.Service.Version = "1.1.0"
	snap := workerFleet(t, published, drafted)
	if s := snap.Services["newsletter"]; s == nil || len(s.Revisions) != 1 {
		t.Fatalf("newsletter = %+v; want the published revision only", s)
	}
	res := breakWorker(snap)
	if res.Gate != GateUnverified || len(res.Consumers) != 1 || res.Consumers[0].CompatibilityVerdict != CompatibilityUnknown ||
		res.Consumers[0].Compatibility != contract.CompatibilityUnknown {
		t.Fatalf("gate %s (%s), consumers %+v; want unverified over the declared unknown range", res.Gate, res.GateReason, res.Consumers)
	}
	// With no published revision the draft alone never makes newsletter a
	// consumer its requested range would call compatible.
	for _, c := range breakWorker(workerFleet(t, drafted)).Consumers {
		if c.Service == "newsletter" {
			t.Fatalf("the draft alone made newsletter a consumer: %+v", c)
		}
	}
}
