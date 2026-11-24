package mcp

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// consumableFleet is postgresql, a provider accepted under a publisher rule
// with a closed schema that runs in namespace secret, and redis, a provider
// no source checked.
func consumableFleet(t *testing.T) *fleet.Query {
	t.Helper()
	digest := "sha256:" + strings.Repeat("4c", 32)
	rev := func(name, offering string, prov *fleet.BundleProvenance) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService, Service: contract.Service{Name: name, Version: "1.0.0", Offering: offering}}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme",
			RequestedRef: "oci://ghcr.io/acme/" + name + ":1.0.0", Digest: digest, Provenance: prov}
	}
	pg := rev("pg", "postgresql", &fleet.BundleProvenance{Source: "oci", Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule", Signer: "ci"}})
	pg.Bundle.Contract.Configurations = []contract.Configuration{{Name: "db", Schema: "s.json", Required: true}}
	pg.Bundle.FS = fstest.MapFS{"s.json": {Data: []byte(`{"type":"object","additionalProperties":false,"properties":{"size":{"type":"integer"}}}`)}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{pg, rev("redis", "redis", nil)}}),
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{
			{Scope: "secret", Kind: fleet.SourceKindKubernetes, Name: "pg-0", Service: "pg", Domain: "ghcr.io/acme", Digest: digest, Compliance: fleet.StatusCompliant}}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestMCPFleetEntitiesConsumable(t *testing.T) {
	q := consumableFleet(t)
	var out fleet.EntityList
	decode(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"consumable": true}), &out)
	want, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entities) != 1 || out.Entities[0].Consumable == nil || !reflect.DeepEqual(out.Entities[0].Consumable.Providers[0].Digest, want.Entities[0].Consumable.Providers[0].Digest) {
		t.Fatalf("consumable = %+v, want %+v", out.Entities, want.Entities)
	}
	if out.Meta.Completeness != fleet.CompletenessPartial {
		t.Errorf("completeness = %s, want partial: redis was not checked", out.Meta.Completeness)
	}
}

func TestMCPFleetEntitiesConsumableRefusesOtherKind(t *testing.T) {
	q := consumableFleet(t)
	for _, c := range []struct {
		args   map[string]any
		reason string
	}{
		{map[string]any{"consumable": true, "kind": "service"}, "consumable keeps offerings; ask kind offering"},
		{map[string]any{"consumable": true, "kind": "offering", "declaration": "inferred"},
			"consumable lists accepted offerings and declaration inferred lists inferred ones; ask for one"},
	} {
		// The reason names the MCP arguments, never a CLI flag.
		if res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), c.args); !res.IsError || !strings.Contains(resultText(t, res), c.reason) {
			t.Errorf("%v = %s, want %q", c.args, resultText(t, res), c.reason)
		}
	}
}

// TestMCPFleetEntitiesConsumableSignedInRestricted: a signed-in caller's
// answer comes from its view, so a provider it cannot see is never named.
func TestMCPFleetEntitiesConsumableSignedInRestricted(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, ns string) bool { return ns != "secret" }, nil), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(consumableFleet(t)), Opts: FleetOptions{View: view}}})
	text := resultText(t, callToolOn(t, server, "pacto_fleet_entities", map[string]any{"consumable": true}))
	if strings.Contains(text, `"pg`) || strings.Contains(text, "postgresql") || !strings.Contains(text, `"completeness"`) {
		t.Errorf("a hidden provider is named:\n%s", text)
	}
}
