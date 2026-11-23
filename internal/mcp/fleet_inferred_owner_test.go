package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// pacto_fleet_get hands an agent the owner and domain a source outside every
// contract reports, beside the declared fields, with where and when it read
// them; the owner stays empty.
func TestMCPFleetGetCarriesInferredOwner(t *testing.T) {
	ledger := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "ledger", Version: "1.0.0"}, Workload: contract.WorkloadService}
	at := fixedNow.Add(-time.Hour)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }},
		fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: ledger}, Digest: "sha256:l"}}}),
		fleet.NewMemorySource("catalog-a", "idp-catalog", &fleet.Collection{Advisory: true, ServiceFacts: []fleet.RawServiceFact{{
			Name: "ledger", Entity: "component:default/ledger", Owner: contract.Owner{Team: "team-ledger"}, BusinessDomain: "billing", ObservedAt: &at,
		}}}))
	if err != nil {
		t.Fatal(err)
	}
	res := callHandler(t, fleetGetTool(), fleetGetHandler(fleet.NewQuery(snap)), map[string]any{"service": "ledger"})
	var out fleet.ServiceView
	decode(t, res, &out)
	s := out.Service
	if s == nil || !s.Owner.IsEmpty() || s.OwnerProvenance != fleet.OwnerProvenanceInferred {
		t.Fatalf("service %+v", s)
	}
	if s.InferredOwner == nil || s.InferredOwner.Value != "team:team-ledger" || s.InferredOwner.Source != "catalog-a" ||
		s.InferredOwner.Where != "component:default/ledger" || s.InferredOwner.AsOf == nil {
		t.Errorf("inferred owner %+v", s.InferredOwner)
	}
	if s.BusinessDomain != "" || s.InferredBusinessDomain == nil || s.InferredBusinessDomain.Value != "billing" {
		t.Errorf("domain %q inferred %+v", s.BusinessDomain, s.InferredBusinessDomain)
	}
	if d := fleetGetTool().Description; !strings.Contains(d, "'inferredOwner' and 'inferredBusinessDomain' are hints") || !strings.Contains(d, "never an owner or a domain") {
		t.Errorf("description: %s", d)
	}
}
