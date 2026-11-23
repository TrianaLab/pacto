package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// externalFleet: app (namespace shop) and vault (namespace secretns) both
// declare Stripe; only vault declares legacy-mq.
func externalFleet(t *testing.T) *fleet.Query {
	t.Helper()
	svc := func(name string, deps ...string) *contract.Contract {
		c := &contract.Contract{PactoVersion: "2.0", Workload: contract.WorkloadService, Service: contract.Service{Name: name, Version: "1.0.0"}}
		for _, d := range deps {
			c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, External: true})
		}
		return c
	}
	k := fleet.SourceKindKubernetes
	src := fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: svc("app", "Stripe")}, Digest: "sha256:a"},
			{Bundle: &contract.Bundle{Contract: svc("vault", "STRIPE", "legacy-mq")}, Digest: "sha256:v"},
		},
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: k, Name: "app", Service: "app", Compliance: fleet.StatusCompliant},
			{Scope: "secretns", Kind: k, Name: "vault", Service: "vault", Compliance: fleet.StatusCompliant},
		},
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, src)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestMCPFleetGetExternal(t *testing.T) {
	var v fleet.ExternalView
	decode(t, callHandler(t, fleetGetTool(), fleetGetHandler(externalFleet(t)), map[string]any{"service": "external:stripe"}), &v)
	if v.External.Key != "stripe" || v.External.ConsumersTotal != 2 || v.External.Class != "vendor-api" || v.Catalog == nil {
		t.Errorf("view = %+v", v)
	}
	if res := callHandler(t, fleetGetTool(), fleetGetHandler(externalFleet(t)), map[string]any{"service": "external:nobody"}); !res.IsError {
		t.Errorf("an unknown external = %s, want an error result", resultText(t, res))
	}
}

// TestMCPFleetGetExternalRestricted: a signed-in view counts the consumer it
// hides and never names it; an external only hidden services reach is not
// found.
func TestMCPFleetGetExternalRestricted(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, ns string) bool { return ns != "secretns" }, nil), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(externalFleet(t)), Opts: FleetOptions{View: view}}})
	res := callToolOn(t, server, "pacto_fleet_get", map[string]any{"service": "external:stripe"})
	if text := resultText(t, res); strings.Contains(text, "vault") || strings.Contains(text, "STRIPE") {
		t.Errorf("a restricted answer names a hidden consumer:\n%s", text)
	}
	var v fleet.ExternalView
	decode(t, res, &v)
	if v.External.ConsumersTotal != 1 || v.External.HiddenConsumers != 1 {
		t.Errorf("consumers %d hidden %d, want 1 and 1", v.External.ConsumersTotal, v.External.HiddenConsumers)
	}
	if res := callToolOn(t, server, "pacto_fleet_get", map[string]any{"service": "external:legacy-mq"}); !res.IsError {
		t.Errorf("an external only hidden services reach = %s, want not found", resultText(t, res))
	}
}

func TestMCPFleetEntitiesClass(t *testing.T) {
	q := externalFleet(t)
	got := listedKeys(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"kind": "external", "class": "unclassified"}))
	if len(got) != 1 || got[0] != "external/legacy-mq" {
		t.Errorf("class unclassified = %v, want legacy-mq", got)
	}
	if res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"class": "bogus"}); !res.IsError {
		t.Errorf("an unknown class = %s, want an error result", resultText(t, res))
	}
}

func TestMCPFleetGraphExternalUnsetDirection(t *testing.T) {
	var g fleet.GraphResult
	decode(t, callHandler(t, fleetGraphTool(), fleetGraphHandler(externalFleet(t)), map[string]any{"service": "external:stripe"}), &g)
	if g.External != "stripe" || g.Direction != fleet.DirectionDependents || len(g.Nodes) != 2 {
		t.Errorf("an unset direction on an external root means dependents: %+v", g)
	}
	if res := callHandler(t, fleetGraphTool(), fleetGraphHandler(externalFleet(t)), map[string]any{"service": "external:stripe", "direction": "dependencies"}); !res.IsError {
		t.Error("an explicit dependencies graph of an external system is not an error")
	}
}

func TestMCPFleetSearchExternals(t *testing.T) {
	var res fleet.SearchResult
	decode(t, callHandler(t, fleetSearchTool(), fleetSearchHandler(externalFleet(t)), map[string]any{"text": "stripe"}), &res)
	if res.ExternalsTotal != 1 || len(res.Externals) != 1 || res.Externals[0].Key != "stripe" {
		t.Errorf("externals = %+v (total %d)", res.Externals, res.ExternalsTotal)
	}
	res = fleet.SearchResult{}
	decode(t, callHandler(t, fleetSearchTool(), fleetSearchHandler(externalFleet(t)), map[string]any{"text": "stripe", "offset": 5}), &res)
	if res.ExternalsTotal != 0 || len(res.Externals) != 0 {
		t.Errorf("a later page repeats the externals: %+v", res.Externals)
	}
	if !strings.Contains(fleetInstructions, "kind external") || !strings.Contains(fleetSearchTool().Description, "externals") {
		t.Error("the instructions or the search tool never mention external systems")
	}
}

// inferredExternalFleet: checkout in shop and billing in secretns both call stripe;
// checkout's pod spec names it too.
func inferredExternalFleet(t *testing.T) *fleet.Query {
	t.Helper()
	col := &fleet.Collection{
		ExternalUses: []fleet.RawExternalUse{{Service: "checkout", Namespace: "shop", Identity: "stripe", Class: "vendor-api", Confidence: "likely",
			Kind: "env", Where: "deployment/checkout env STRIPE_API_BASE", Host: "api.stripe.com", Entry: "stripe"}},
		Observed: []fleet.ObservedEdge{{From: "checkout", To: "api.stripe.com", Count: 3}, {From: "billing", To: "api.stripe.com", Count: 1}},
	}
	for name, ns := range map[string]string{"checkout": "shop", "billing": "secretns"} {
		digest := "sha256:" + name
		col.Revisions = append(col.Revisions, fleet.RawRevision{Digest: digest, Bundle: &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.4", Workload: contract.WorkloadService, Service: contract.Service{Name: name, Version: "1.0.0"}}}})
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Digest: digest})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestFleetGetExternalInferredSources(t *testing.T) {
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(inferredExternalFleet(t))}})
	text := resultText(t, callToolOn(t, server, "pacto_fleet_entity", map[string]any{"kind": "external", "key": "stripe"}))
	for _, want := range []string{`"inferredSources"`, `"where": "deployment/checkout env STRIPE_API_BASE"`, `"host": "api.stripe.com"`,
		`"confidence": "likely"`, `"entry": "stripe"`, `"observedSources"`, `"billing"`} {
		if !strings.Contains(text, want) {
			t.Errorf("external entity lacks %s:\n%s", want, text)
		}
	}
	explain := resultText(t, callToolOn(t, server, "pacto_fleet_explain", map[string]any{"subject": "external:stripe"}))
	if !strings.Contains(explain, "inferred from env deployment/checkout env STRIPE_API_BASE of checkout (likely, host api.stripe.com, entry stripe") {
		t.Errorf("explain lacks the inferred source:\n%s", explain)
	}
}

func TestFleetGetExternalRestrictedCountsHidden(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, ns string) bool { return ns != "secretns" }, nil), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(inferredExternalFleet(t)), Opts: FleetOptions{View: view}}})
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"pacto_fleet_entity", map[string]any{"kind": "external", "key": "stripe"}},
		{"pacto_fleet_explain", map[string]any{"subject": "external:stripe"}},
	} {
		text := resultText(t, callToolOn(t, server, c.tool, c.args))
		if strings.Contains(text, "billing") || strings.Contains(text, "secretns") {
			t.Errorf("%s names the hidden caller:\n%s", c.tool, text)
		}
		if c.tool == "pacto_fleet_entity" && (!strings.Contains(text, `"hiddenConsumers": 1`) || !strings.Contains(text, `"inferredSources"`)) {
			t.Errorf("entity does not count the hidden caller or lost the visible one:\n%s", text)
		}
	}
}

// TestInferredRecordEdgesDescribed: the instructions and the graph and impact
// tools name the service dependency an inferred record names, so an agent
// never reports it as a configuration read or a transitive consumer.
func TestInferredRecordEdgesDescribed(t *testing.T) {
	for name, text := range map[string]string{
		"instructions":      fleetInstructions,
		"pacto_fleet_graph": fleetGraphTool().Description,
		"pacto_impact":      impactTool().Description,
	} {
		if !strings.Contains(text, "inferred record") {
			t.Errorf("%s does not name the inferred record", name)
		}
	}
	if !strings.Contains(fleetInstructions, "Pacto uses 'inferred' in five places") || !strings.Contains(fleetInstructions, "inferredRecord") {
		t.Error("the instructions miss the inferred record meaning of 'inferred'")
	}
	for _, want := range []string{"kind repository", "the revision", "sdk or terraform match is possible only"} {
		if !strings.Contains(fleetInstructions, want) {
			t.Errorf("the instructions miss %q", want)
		}
	}
}

// TestFleetGetExternalRepositorySource: the entity and explain tools show an
// external the consumer's inferred record names, as a repository source
// with its revision.
func TestFleetGetExternalRepositorySource(t *testing.T) {
	path := "requirements.txt"
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{
			{Path: path, Digest: "sha256:" + hex.EncodeToString(sha256Of("stripe==11.4.0\n"))}}},
		Fields: []contract.Inferred{{Field: "dependencies[stripe]", Value: "stripe", Source: path + ":1", Confidence: contract.ConfidencePossible,
			External: &contract.InferredExternal{Identity: "stripe", Catalog: "default", Sources: []contract.InferredExternalSource{{Kind: "sdk", Path: path, Line: 1}}}}}}
	fsys := fstest.MapFS{path: {Data: []byte("stripe==11.4.0\n")}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	src := fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Revisions: []fleet.RawRevision{{Digest: "sha256:c", Bundle: &contract.Bundle{FS: fsys,
		Contract: &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "checkout", Version: "1.0.0"}}}}}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(fleet.NewQuery(snap))}})
	text := resultText(t, callToolOn(t, server, "pacto_fleet_entity", map[string]any{"kind": "external", "key": "stripe"}))
	for _, want := range []string{`"kind": "repository"`, `"provenance": "inferred"`, `"where": "requirements.txt:1"`, `"revision": "checkout@sha256:c"`, `"confidence": "possible"`} {
		if !strings.Contains(text, want) {
			t.Errorf("external entity lacks %s:\n%s", want, text)
		}
	}
	explain := resultText(t, callToolOn(t, server, "pacto_fleet_explain", map[string]any{"subject": "external:stripe"}))
	if !strings.Contains(explain, "inferred from sdk requirements.txt:1 of checkout (possible, entry stripe, revision checkout@sha256:c)") {
		t.Errorf("explain lacks the repository source:\n%s", explain)
	}
}

func sha256Of(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// TestMCPFleetGetExternalProvider: an agent reads the provider of
// external:stripe and the pinning consumer's edge as unknown with
// INFERRED_PROVIDER, and the instructions say why.
func TestMCPFleetGetExternalProvider(t *testing.T) {
	const repo = "ghcr.io/acme/pacto-inferred/stripe"
	pinner := &contract.Contract{PactoVersion: "2.0", Workload: contract.WorkloadService, Service: contract.Service{Name: "checkout", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "stripe-pinned", Ref: "oci://" + repo, Compatibility: "^0.0.1"}}}
	inferred := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "stripe", Version: "0.0.1", External: "stripe"}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: inferred, FS: fstest.MapFS{}}, ResolvedRef: repo + ":0.0.1", Digest: "sha256:s"},
			{Bundle: &contract.Bundle{Contract: pinner, FS: fstest.MapFS{}}, Digest: "sha256:c"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	var v fleet.ExternalView
	decode(t, callHandler(t, fleetGetTool(), fleetGetHandler(q), map[string]any{"service": "external:stripe"}), &v)
	if p := v.External.Provider; p == nil || p.Repository != repo || p.RangeStatus != fleet.RangeUnknown || p.RangeReason != fleet.RangeReasonInferredProvider {
		t.Fatalf("provider = %+v", v.External.Provider)
	}
	var sv fleet.ServiceView
	decode(t, callHandler(t, fleetGetTool(), fleetGetHandler(q), map[string]any{"service": "checkout"}), &sv)
	if len(sv.Dependencies) != 1 || sv.Dependencies[0].RangeStatus != fleet.RangeUnknown || sv.Dependencies[0].RangeReason != fleet.RangeReasonInferredProvider {
		t.Errorf("edge = %+v", sv.Dependencies)
	}
	for _, text := range []string{fleetInstructions, fleetGetTool().Description, fleetVersionsTool().Description} {
		if !strings.Contains(text, "INFERRED_PROVIDER") {
			t.Errorf("lacks INFERRED_PROVIDER: %s", text)
		}
	}
}

// pacto_fleet_status sorts by severity, code and name: only
// pacto_fleet_attention ranks UNOWNED_EXTERNAL by consumers.
func TestFleetStatusDescriptionClaimsNoRank(t *testing.T) {
	if d := fleetStatusTool().Description; strings.Contains(d, "UNOWNED_EXTERNAL, ranked") || !strings.Contains(d, "pacto_fleet_attention ranks it") {
		t.Errorf("description = %q", d)
	}
}

// TestMCPEntitiesOutsideContract: outside_contract keeps the external
// systems with a managed resource no offering produced and, like class,
// lists externals when no kind is asked.
func TestMCPEntitiesOutsideContract(t *testing.T) {
	src := fleet.NewMemorySource("managed", fleet.SourceKindDetection, &fleet.Collection{ManagedResources: []fleet.RawManagedResource{{
		Source: "crossplane", Identity: "aws/rds/123456789012/eu-west-1/orders-db", Type: "rds.aws.upbound.io/Instance", Name: "orders-db"}}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, src)
	if err != nil {
		t.Fatal(err)
	}
	got := listedKeys(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(fleet.NewQuery(snap), false), map[string]any{"outside_contract": true}))
	if len(got) != 1 || got[0] != "external/aws/rds/123456789012/eu-west-1/orders-db" {
		t.Errorf("outside_contract = %v", got)
	}
	if got := listedKeys(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(externalFleet(t), false), map[string]any{"outside_contract": true})); len(got) != 0 {
		t.Errorf("no managed resource, yet %v", got)
	}
}
