package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var inferredAt = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

func printedService(t *testing.T, s *fleet.ServiceRecord) string {
	t.Helper()
	return printed(t, func(c *cobra.Command) error { return printFleetService(c, &fleet.ServiceView{Service: s}, "text") })
}

func TestPrintFleetServiceInferredOwner(t *testing.T) {
	out := printedService(t, &fleet.ServiceRecord{Name: "ledger", OwnerProvenance: fleet.OwnerProvenanceInferred,
		InferredOwner: &fleet.InferredFact{Value: "team:team-ledger", Source: "backstage-catalog", Where: "component:default/ledger", AsOf: &inferredAt}})
	want := "Service: ledger\nOwner: unknown\n  inferred: team-ledger (backstage-catalog component:default/ledger, asOf 2026-11-14T09:00:00Z)\nStatus: -\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("got:\n%s\nwant prefix:\n%s", out, want)
	}
	if strings.Count(out, "team-ledger") != 1 {
		t.Errorf("the value must print once:\n%s", out)
	}
	// No time and no reference: the source alone; a value that is no owner
	// key prints as it is.
	out = printedService(t, &fleet.ServiceRecord{Name: "ledger", OwnerProvenance: fleet.OwnerProvenanceInferred,
		InferredOwner: &fleet.InferredFact{Value: "odd", Source: "catalog-a"}})
	inOrder(t, out, "Owner: unknown", "  inferred: odd (catalog-a)")
	// Inferred provenance without the fact keeps today's line.
	inOrder(t, printedService(t, &fleet.ServiceRecord{Name: "ledger", OwnerProvenance: fleet.OwnerProvenanceInferred}), "Owner: - (inferred)")
}

func TestPrintFleetServiceInferredDomain(t *testing.T) {
	domain := &fleet.InferredFact{Value: "billing", Source: "backstage-catalog", Where: "component:default/ledger", AsOf: &inferredAt}
	inOrder(t, printedService(t, &fleet.ServiceRecord{Name: "ledger", InferredBusinessDomain: domain}),
		"Owner: -", "Domain: none", "  inferred: billing (backstage-catalog component:default/ledger, asOf 2026-11-14T09:00:00Z)", "Status: -")
	inOrder(t, printedService(t, &fleet.ServiceRecord{Name: "ledger", BusinessDomainUnknown: true, InferredBusinessDomain: domain}), "Domain: unknown")
	if out := printedService(t, &fleet.ServiceRecord{Name: "ledger", BusinessDomain: "commerce", InferredBusinessDomain: domain}); strings.Contains(out, "Domain:") || strings.Contains(out, "billing") {
		t.Errorf("a declared domain prints no inferred one:\n%s", out)
	}
}
