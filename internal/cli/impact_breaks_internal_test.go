package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

func TestImpactPrintsBreakStates(t *testing.T) {
	res := &impact.Result{Service: "ledger", Consumers: []impact.AffectedConsumer{
		{Service: "reports", Direct: true, Break: &impact.BreakState{State: impact.BreakAccepted, By: "ana@acme.com", Record: "r@sha256:ab"}},
		{Service: "web", Direct: true, Break: &impact.BreakState{State: impact.BreakAccepted, Reason: "web's ranges admit 3.0.0"}},
		{Service: "billing", Direct: true, Break: &impact.BreakState{State: impact.BreakAnnounced, Route: "waiting for the owner of billing: bob@acme.com"}},
		{Service: "audit", Direct: true, Break: &impact.BreakState{State: impact.BreakExpired, Expires: "2026-12-14", Reason: "the break expired on 2026-12-14"}},
		{Service: "cli", Direct: true},
	}}
	var w bytes.Buffer
	printImpactConsumers(&w, res)
	for _, want := range []string{
		"    break: accepted for reports (ana@acme.com, r@sha256:ab)\n",
		"    break: accepted for web; web's ranges admit 3.0.0\n",
		"    break: announced for billing, not accepted; waiting for the owner of billing: bob@acme.com\n",
		"    break: expired 2026-12-14; the break expired on 2026-12-14\n",
		"Announced break: 2 unaccepted consumers\n",
	} {
		if !strings.Contains(w.String(), want) {
			t.Errorf("missing %q in\n%s", want, w.String())
		}
	}
	res.Consumers = res.Consumers[2:3]
	w.Reset()
	if printImpactConsumers(&w, res); !strings.Contains(w.String(), "Announced break: 1 unaccepted consumer\n") {
		t.Fatal(w.String())
	}
}
