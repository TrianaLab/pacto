package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// consumableDashboardQuery is postgresql, a provider accepted under a
// publisher rule with a closed schema that runs in namespace secret, and
// redis, a provider no source checked.
func consumableDashboardQuery(t *testing.T) *fleet.Query {
	t.Helper()
	rev := func(name, offering string, prov *fleet.BundleProvenance) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService, Service: contract.Service{Name: name, Version: "1.0.0", Offering: offering}}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme",
			RequestedRef: "oci://ghcr.io/acme/" + name + ":1.0.0", Digest: validDigest("a"), Provenance: prov}
	}
	pg := rev("pg", "postgresql", &fleet.BundleProvenance{Source: "oci", Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule", Signer: "ci"}})
	pg.Bundle.Contract.Configurations = []contract.Configuration{{Name: "db", Schema: "s.json", Required: true}}
	pg.Bundle.FS = fstest.MapFS{"s.json": {Data: []byte(`{"type":"object","additionalProperties":false,"properties":{"size":{"type":"integer","minimum":1,"x-pacto-tier":"tunable"}}}`)}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{pg, rev("redis", "redis", nil)}}),
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{
			{Scope: "secret", Kind: fleet.SourceKindKubernetes, Name: "pg-0", Service: "pg", Domain: "ghcr.io/acme", Digest: validDigest("a"), Compliance: fleet.StatusCompliant}}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// TestDashboardEntitiesConsumable: the route answers the rows q.Entities
// answers, and asks for offerings when no kind is named.
func TestDashboardEntitiesConsumable(t *testing.T) {
	q := consumableDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	want, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want.Entities[0].Consumable)
	for _, path := range []string{"/api/fleet/entities?consumable=true", "/api/fleet/entities?kinds=offering&consumable=true"} {
		var got struct {
			Entities []struct {
				Consumable json.RawMessage `json:"consumable"`
			} `json:"entities"`
			Meta fleet.ProductMeta `json:"meta"`
		}
		getJSON(t, base+path, http.StatusOK, &got)
		if len(got.Entities) != 1 || string(got.Entities[0].Consumable) != string(wantJSON) || got.Meta.Completeness != fleet.CompletenessPartial {
			t.Errorf("%s = %+v, want %s", path, got, wantJSON)
		}
	}
	expectStatus(t, base+"/api/fleet/entities?kinds=service&consumable=true", http.StatusUnprocessableEntity)
	expectStatus(t, base+"/api/fleet/entities?declaration=inferred&kinds=offering&consumable=true", http.StatusUnprocessableEntity)
}

// TestDashboardConsumableUsesViewerSnapshot: the route answers from the
// viewer's view, so a provider the viewer cannot see is never named.
func TestDashboardConsumableUsesViewerSnapshot(t *testing.T) {
	q := consumableDashboardQuery(t)
	base := serveOptions(t, Options{
		Fleet: func(context.Context) (*fleet.Query, error) { return q, nil },
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(_, ns string) bool { return ns != "secret" }, nil), nil
		},
		Auth: &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }},
	})
	var raw json.RawMessage
	getJSON(t, base+"/api/fleet/entities?consumable=true", http.StatusOK, &raw)
	if strings.Contains(string(raw), `"pg`) || strings.Contains(string(raw), "postgresql") || !strings.Contains(string(raw), `"completeness"`) {
		t.Errorf("a hidden provider is named: %s", raw)
	}
}
