package mcp

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestFleetGetHandoverProvenance(t *testing.T) {
	q := testutil.HandoverQuery(t)
	want, err := q.GetService("invoices")
	if err != nil {
		t.Fatal(err)
	}
	var out fleet.ServiceView
	decode(t, callHandler(t, fleetGetTool(), fleetGetHandler(q), map[string]any{"service": "invoices"}), &out)
	if len(out.Replaces) != 1 || out.Replaces[0].Link != "inferred-from" || out.Replaces[0].By != "ana@acme.com" || out.Replaces[0].Workload == nil {
		t.Fatalf("replaces %+v", out.Replaces)
	}
	if out.Fields == nil || out.Fields.Total != len(want.Fields.Items) || out.Fields.Items[1].Provenance != fleet.FieldAccepted || out.Fields.Items[1].By != "ana@acme.com" {
		t.Errorf("fields %+v, want %+v", out.Fields, want.Fields)
	}
	for _, d := range []string{fleetGetTool().Description, fleetExplainTool().Description, fleetEntityTool().Description} {
		for _, w := range []string{"'replacesHidden'", "'fields'", "inferred-from", "accepted", "attested", "'asOf'", "'completeness'", "never declared"} {
			if !strings.Contains(d, w) {
				t.Errorf("description lacks %s:\n%s", w, d)
			}
		}
	}
}

func TestFleetExplainHandoverProvenance(t *testing.T) {
	var out fleet.ExplainResult
	decode(t, callHandler(t, fleetExplainTool(), fleetExplainHandler(testutil.HandoverQuery(t)), map[string]any{"subject": "invoices"}), &out)
	if len(out.Reasons) == 0 || out.Reasons[0].Code != fleet.ReasonReplacesInferred ||
		out.Reasons[0].Message != "Declared by ana@acme.com from inferred k8s:billing/Deployment/invoices (revision invoices@sha256:invoices-0.1.0)" {
		t.Errorf("reasons %+v", out.Reasons)
	}
	if out.Fields == nil || out.Fields.Items[0].Field != "service.owner" || out.Fields.Items[0].Provenance != fleet.FieldDeclared {
		t.Errorf("fields %+v", out.Fields)
	}
}
