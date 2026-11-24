package app

import (
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestCIBreakNotes: each consumer a break names gets one note saying where
// the break stands and where its expiry comes from.
func TestCIBreakNotes(t *testing.T) {
	t.Parallel()
	rec := acceptRecordOf(contract.Inferred{Field: "breaks[1].expires", Value: "2026-12-14", Source: "commit:0123456789ab +30d", Confidence: contract.ConfidenceLikely})
	b := &contract.Bundle{FS: contract.WithInferredRecord(nil, rec.Marshal()), Contract: &contract.Contract{Breaks: []contract.Break{
		{Consumers: []string{"web"}, Expires: "2027-01-01"}, {Consumers: []string{"reports"}}, {Consumers: []string{"api"}}}}}
	res := &impact.Result{Consumers: []impact.AffectedConsumer{
		{Service: "web", Break: &impact.BreakState{State: impact.BreakAnnounced, Expires: "2027-01-01"}},
		{Service: "web", Break: &impact.BreakState{State: impact.BreakAnnounced, Expires: "2027-01-01"}},
		{Service: "reports", Break: &impact.BreakState{State: impact.BreakAccepted, By: "ana@acme.com", Record: "r@sha256:ab", Expires: "2026-12-14"}},
		{Service: "api", Break: &impact.BreakState{State: impact.BreakAnnounced, Expires: "2026-12-20"}},
		{Service: "cli", Break: &impact.BreakState{State: impact.BreakExpired}},
		{Service: "billing"},
	}}
	want := []string{
		"break announced for web, not accepted; expires 2027-01-01 (declared)",
		"break accepted for reports (ana@acme.com, r@sha256:ab); expires 2026-12-14 (commit 0123456789ab + 30d, inferred)",
		"break announced for api, not accepted; expires 2026-12-20 (commit time + 30d, inferred)",
		"break expired -",
	}
	if got := breakNotes(b, res); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	if breakNotes(nil, res) != nil || breakNotes(b, nil) != nil {
		t.Fatal("no bundle or no answer is no note")
	}
	if got := expirySource(nil, nil, "web"); got != "commit time + 30d, inferred" {
		t.Fatal(got)
	}
}
