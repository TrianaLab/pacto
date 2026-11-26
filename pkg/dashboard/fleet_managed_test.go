package dashboard

import (
	"context"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestFleetEntitiesOutsideContract: outsideContract keeps the external
// systems with a managed resource no offering produced and, like class,
// lists externals when no kind is asked.
func TestFleetEntitiesOutsideContract(t *testing.T) {
	pay := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "payments-service", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "redis", External: true, Version: "7"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("local", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: &contract.Bundle{Contract: pay, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme", RequestedRef: "oci://ghcr.io/acme/payments-service:1.0.0", Digest: validDigest("d")}}}),
		fleet.NewMemorySource("managed", fleet.SourceKindDetection, &fleet.Collection{ManagedResources: []fleet.RawManagedResource{{
			Source: "crossplane", Identity: "aws/rds/123456789012/eu-west-1/orders-db", Type: "rds.aws.upbound.io/Instance", Name: "orders-db"}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	var all, list ProductEntityList
	getJSON(t, base+"/api/fleet/entities?kinds=external", http.StatusOK, &all)
	if all.Total != 2 {
		t.Fatalf("externals = %+v", all.Entities)
	}
	getJSON(t, base+"/api/fleet/entities?outsideContract=true", http.StatusOK, &list)
	if list.Total != 1 || list.Entities[0].Key != "aws/rds/123456789012/eu-west-1/orders-db" {
		t.Errorf("outside contract = %+v", list.Entities)
	}
}
