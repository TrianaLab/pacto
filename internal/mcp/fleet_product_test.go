package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// buildProductQuery: orders (team commerce, domain shop) depends on payments
// (team billing, domain money, offering payments-api); lonely declares no owner.
func buildProductQuery(t *testing.T) *fleet.Query {
	t.Helper()
	mk := func(name, team, domain, offering string, deps ...string) *contract.Contract {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
		}
		return &contract.Contract{PactoVersion: "2.5", Dependencies: d, Workload: contract.WorkloadService,
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}, Domain: domain, Offering: offering}}
	}
	src := fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: &contract.Bundle{Contract: mk("orders", "commerce", "shop", "", "payments")}, Digest: "sha256:o"},
		{Bundle: &contract.Bundle{Contract: mk("payments", "billing", "money", "payments-api")}, Digest: "sha256:p"},
		{Bundle: &contract.Bundle{Contract: mk("lonely", "", "shop", "")}, Digest: "sha256:l"},
	}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, src)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func entityKeys(l fleet.EntityList) []string {
	var out []string
	for _, e := range l.Entities {
		out = append(out, string(e.Kind)+"/"+e.Key)
	}
	return out
}

func TestFleetEntitiesTool(t *testing.T) {
	q := buildProductQuery(t)
	list := func(args map[string]any) fleet.EntityList {
		var out fleet.EntityList
		decode(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), args), &out)
		return out
	}
	if got := entityKeys(list(map[string]any{"kind": "owner"})); !slices.Equal(got, []string{"owner/team:billing", "owner/team:commerce"}) {
		t.Errorf("kind=owner = %v", got)
	}
	if got := entityKeys(list(map[string]any{"ownership": "unowned"})); !slices.Equal(got, []string{"service/lonely"}) {
		t.Errorf("ownership=unowned = %v", got)
	}
	all := list(map[string]any{})
	for _, e := range all.Entities {
		if e.Kind != fleet.KindService && e.Kind != fleet.KindOwner && e.Kind != fleet.KindDomain && e.Kind != fleet.KindOffering {
			t.Errorf("empty kind returned %s/%s", e.Kind, e.Key)
		}
	}
	if !slices.Contains(entityKeys(all), "domain/shop") || !slices.Contains(entityKeys(all), "offering/payments-api") {
		t.Errorf("empty kind misses domains or offerings: %v", entityKeys(all))
	}
	var search fleet.SearchResult
	decode(t, callHandler(t, fleetSearchTool(), fleetSearchHandler(q), map[string]any{}), &search)
	if all.Meta.SnapshotID == "" || all.Meta.SnapshotID != search.Meta.SnapshotID {
		t.Errorf("snapshotId %q, search %q", all.Meta.SnapshotID, search.Meta.SnapshotID)
	}
}

func TestFleetEntitiesToolPagingAndErrors(t *testing.T) {
	q := buildProductQuery(t)
	var page fleet.EntityList
	decode(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"kind": "service", "limit": 1, "offset": 1, "sort": "name"}), &page)
	if page.Total != 3 || !page.Truncated || entityKeys(page)[0] != "service/orders" {
		t.Errorf("paged = %+v", page)
	}
	if res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"kind": "revision"}); !res.IsError {
		t.Error("kind=revision is outside the enum")
	}
	if res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"offset": -1}); !res.IsError {
		t.Error("a negative offset is an invalid query")
	}
}

func TestFleetEntityTool(t *testing.T) {
	q := buildProductQuery(t)
	detail := func(kind, key string) fleet.EntityDetail {
		var out fleet.EntityDetail
		decode(t, callHandler(t, fleetEntityTool(), fleetEntityHandler(q, false), map[string]any{"kind": kind, "key": key}), &out)
		return out
	}
	if d := detail("domain", "shop"); d.Domain == nil || d.Entity.Kind != fleet.KindDomain {
		t.Errorf("domain detail = %+v", d)
	}
	if d := detail("offering", "payments-api"); d.Offering == nil {
		t.Errorf("offering detail = %+v", d)
	}
	if res := callHandler(t, fleetEntityTool(), fleetEntityHandler(q, false), map[string]any{"kind": "planet", "key": "x"}); !res.IsError {
		t.Error("an unknown kind is rejected by the schema")
	}
	if res := callHandler(t, fleetEntityTool(), fleetEntityHandler(q, false), map[string]any{"kind": "service"}); !res.IsError {
		t.Error("a missing key is an error result")
	}
	if res := callHandler(t, fleetEntityTool(), fleetEntityHandler(q, false), map[string]any{"kind": "service", "key": "ghost"}); !res.IsError {
		t.Error("an unknown key is an error result")
	}
}

func TestFleetAttentionTool(t *testing.T) {
	q := buildProductQuery(t)
	var out fleet.AttentionList
	decode(t, callHandler(t, fleetAttentionTool(), fleetAttentionHandler(q, false), map[string]any{"category": "unowned"}), &out)
	if out.Total != 1 || out.Items[0].Code != "UNOWNED_SERVICE" || out.Items[0].NextStep == "" {
		t.Errorf("category=unowned = %+v", out)
	}
	var page fleet.AttentionList
	decode(t, callHandler(t, fleetAttentionTool(), fleetAttentionHandler(q, false), map[string]any{"limit": 1}), &page)
	if page.Total < 2 || !page.Truncated || page.NextOffset == nil || *page.NextOffset != 1 || page.Count != 1 {
		t.Errorf("limit=1 = %+v", page)
	}
	if res := callHandler(t, fleetAttentionTool(), fleetAttentionHandler(q, false), map[string]any{"category": "bogus"}); !res.IsError {
		t.Error("an unknown category is rejected by the schema")
	}
	if res := callHandler(t, fleetAttentionTool(), fleetAttentionHandler(q, false), map[string]any{"offset": -1}); !res.IsError {
		t.Error("a negative offset is an invalid query")
	}
}

func TestFleetSearchOffset(t *testing.T) {
	q := buildProductQuery(t)
	var all, skipped fleet.SearchResult
	decode(t, callHandler(t, fleetSearchTool(), fleetSearchHandler(q), map[string]any{}), &all)
	decode(t, callHandler(t, fleetSearchTool(), fleetSearchHandler(q), map[string]any{"offset": 1}), &skipped)
	if len(skipped.Services) != len(all.Services)-1 || skipped.Services[0].Key != all.Services[1].Key {
		t.Errorf("offset 1 = %+v, all %+v", skipped.Services, all.Services)
	}
}

func TestFleetStatusUnownedFlag(t *testing.T) {
	q := buildProductQuery(t)
	var out fleet.StatusResult
	decode(t, callHandler(t, fleetStatusTool(), fleetStatusHandler(q), map[string]any{"unowned": true}), &out)
	if out.Total != 1 || out.Items[0].Code != "UNOWNED_SERVICE" {
		t.Errorf("unowned = %+v", out)
	}
	var und fleet.StatusResult
	decode(t, callHandler(t, fleetStatusTool(), fleetStatusHandler(q), map[string]any{"undeclared": true}), &und)
	if und.Total != 0 {
		t.Errorf("undeclared on a fleet with no observation = %+v", und)
	}
}

func TestFleetInstructionsGroundAnswers(t *testing.T) {
	for _, want := range []string{
		"cite asOf, snapshotId and sources",
		"declared in a contract, observed at runtime or inferred",
		"Use pacto_fleet_entities to list owners, business domains and offerings",
	} {
		if !strings.Contains(fleetInstructions, want) {
			t.Errorf("fleetInstructions lack %q", want)
		}
	}
}
