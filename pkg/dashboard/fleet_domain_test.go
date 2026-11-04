package dashboard

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// domainDashboardQuery is a fleet with business domains, a capability and a
// target environment label: pay (commerce, declares health), orders (commerce, no
// owner) and web (no domain).
func domainDashboardQuery(t *testing.T) *fleet.Query {
	t.Helper()
	rev := func(name, domain string, owner contract.Owner, caps ...contract.Capability) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0", Domain: domain, Owner: owner}, Capabilities: caps}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme", Digest: validDigest(name[:1])}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "local", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			rev("pay", "commerce", contract.Owner{Team: "pay-team"}, contract.Capability{Type: contract.CapabilityHealth}),
			rev("orders", "commerce", contract.Owner{}),
			rev("web", "", contract.Owner{Team: "web-team"}),
		},
		Targets: []fleet.RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "pay", Service: "pay", Domain: "ghcr.io/acme", Digest: validDigest("p"), Compliance: fleet.StatusNonCompliant,
				Labels: map[string]string{fleet.TargetEnvironmentLabel: "staging"}},
			{Scope: "prod", Kind: "k8s", Name: "web", Service: "web", Domain: "ghcr.io/acme", Digest: validDigest("w"), Compliance: fleet.StatusCompliant},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestProductDomains_OverTheWire(t *testing.T) {
	q := domainDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	var det ProductEntityDetail
	getJSON(t, base+"/api/fleet/entities/domain?key=commerce", http.StatusOK, &det)
	if det.Domain == nil || det.Entity.Href != "/fleet/domains/commerce" {
		t.Fatalf("domain detail = %+v", det)
	}
	if det.Domain.Summary.Services != 2 || det.Domain.Services.Items[0].Href == "" || det.Domain.Owners.Items[0].Href != "/fleet/owners/team:pay-team" {
		t.Errorf("domain previews = %+v / %+v", det.Domain.Services, det.Domain.Owners)
	}
	if len(det.Domain.Deployments.Items) != 1 || det.Domain.Deployments.Items[0].Href == "" {
		t.Errorf("domain deployments = %+v", det.Domain.Deployments)
	}
	expectStatus(t, base+"/api/fleet/entities/domain?key=nope", http.StatusNotFound)

	var tgt ProductEntityDetail
	getJSON(t, base+"/api/fleet/entities/target?key=prod/k8s/pay", http.StatusOK, &tgt)
	if e := tgt.Target.Environment; e.Name != "staging" || e.Declared != "not-declared" || tgt.Entity.Environment != "staging" || tgt.Entity.BusinessDomain != "commerce" {
		t.Errorf("target environment = %+v, entity = %+v", e, tgt.Entity)
	}
}

func TestProductDomainFilters_ReachTheQuery(t *testing.T) {
	q := domainDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"entities?kinds=domain", 1},
		{"entities?kinds=service&businessDomain=commerce", 2},
		{"entities?kinds=service&businessDomain=(none)", 1},
		{"entities?kinds=service&capability=health", 1},
		{"entities?kinds=target&environment=staging", 1},
		{"entities?kinds=service&domain=ghcr.io/acme", 3},
		{"attention?businessDomain=(none)&category=unowned", 0},
		{"attention?category=unowned&businessDomain=commerce", 1},
	} {
		var list struct {
			Total int `json:"total"`
		}
		getJSON(t, base+"/api/fleet/"+strings.ReplaceAll(tc.query, "(none)", "%28none%29"), http.StatusOK, &list)
		if list.Total != tc.want {
			t.Errorf("%s matched %d, want %d", tc.query, list.Total, tc.want)
		}
	}
	expectStatus(t, base+"/api/fleet/entities?kinds=bogus", http.StatusUnprocessableEntity)
	expectStatus(t, base+"/api/fleet/entities?kinds=target&capability=health", http.StatusUnprocessableEntity)

	var ov ProductOverview
	getJSON(t, base+"/api/fleet/overview", http.StatusOK, &ov)
	if len(ov.Summary.ByDomain) != 2 || ov.Summary.Domains != 1 {
		t.Errorf("overview by domain = %+v", ov.Summary.ByDomain)
	}
}
