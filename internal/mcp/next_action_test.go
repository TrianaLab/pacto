package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// nextActionFleet holds every adoption attention code but INFERRED_REPLACED
// (testutil.HandoverQuery holds that one): uncontracted callers of an
// offering's provider, with and without a workload, an inferred workload and
// a range pinned on an inferred external contract.
func nextActionFleet(t *testing.T) *fleet.Query {
	t.Helper()
	rev := func(name, offering string, deps ...contract.Dependency) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}, Offering: offering}, Dependencies: deps}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name}
	}
	const repo = "ghcr.io/acme/pacto-inferred/stripe"
	inferred := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "stripe", Version: "0.0.1", External: "stripe"}}
	k := fleet.SourceKindKubernetes
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }},
		fleet.NewMemorySource("k8s", k, &fleet.Collection{
			Revisions: []fleet.RawRevision{rev("pg", "postgresql")},
			Targets:   []fleet.RawTarget{{Scope: "shop", Kind: k, Name: "pg", Service: "pg", Compliance: fleet.StatusCompliant}},
			Observed:  []fleet.ObservedEdge{{From: "k8s:shop/Deployment/legacy", To: "pg", Count: 1}, {From: "cron-x", To: "pg", Count: 1}},
		}),
		fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
			InferredServices: []fleet.RawInferredService{{Namespace: "shop", Kind: "Deployment", Name: "cart"}}}),
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: inferred, FS: fstest.MapFS{}}, ResolvedRef: repo + ":0.0.1", Digest: "sha256:s"},
			rev("checkout", "", contract.Dependency{Name: "stripe-pinned", Ref: "oci://" + repo, Compatibility: "^0.0.1"}),
		}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// nextActions collects every nextAction object anywhere in a JSON answer.
func nextActions(v any, out *[]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if a, ok := c.(map[string]any); ok && k == "nextAction" {
				*out = append(*out, a)
			}
			nextActions(c, out)
		}
	case []any:
		for _, c := range x {
			nextActions(c, out)
		}
	}
}

// TestNextActionToolRegisteredOnServer: every nextAction a server answers
// names a tool that server lists, or none, and always a CLI command. Over
// HTTP pacto_edit never appears.
func TestNextActionToolRegisteredOnServer(t *testing.T) {
	codes := map[string]bool{}
	tools := map[string]map[string]bool{"stdio": {}, "http": {}}
	for _, q := range []*fleet.Query{nextActionFleet(t), testutil.HandoverQuery(t)} {
		l, err := q.Attention(fleet.AttentionFilter{Limit: fleet.MaxAttentionLimit})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range l.Items {
			codes[it.Code] = codes[it.Code] || it.NextAction != nil
		}
		for name, a := range map[string]Authoring{"stdio": {Declare: stubDeclarer{}}, "http": {Remote: true, Declare: stubDeclarer{}}} {
			checkActionTools(t, name, mustNew("test", a, Modes{Fleet: &FleetMode{Src: staticFleet(q)}}), tools[name])
		}
	}
	for _, code := range []string{"UNCONTRACTED_CALLER", "INFERRED_REPLACED", "INFERRED_PROVIDER"} {
		if !codes[code] {
			t.Errorf("the fixtures hold no %s with a nextAction", code)
		}
	}
	if tools["http"]["pacto_edit"] || !tools["http"]["pacto_declare"] || !tools["http"][""] {
		t.Errorf("http tools %v", tools["http"])
	}
	if !tools["stdio"]["pacto_edit"] || !tools["stdio"]["pacto_declare"] {
		t.Errorf("stdio tools %v", tools["stdio"])
	}
}

// checkActionTools fails each nextAction server answers with no cli or with
// a tool server does not list, and records each tool (or "") in seen.
func checkActionTools(t *testing.T, name string, server *mcpsdk.Server, seen map[string]bool) {
	t.Helper()
	listed := listTools(t, server)
	for _, a := range answeredActions(t, server) {
		tool, _ := a["tool"].(string)
		if cli, _ := a["cli"].(string); cli == "" || tool != "" && listed[tool] == nil {
			t.Errorf("%s: nextAction %v names an unlisted tool or no cli", name, a)
		}
		seen[tool] = true
	}
}

// answeredActions are the nextActions of the attention, overview and entity
// answers server gives; an entity the fixture lacks answers nothing.
func answeredActions(t *testing.T, server *mcpsdk.Server) []map[string]any {
	t.Helper()
	var got []map[string]any
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"pacto_fleet_attention", map[string]any{"limit": fleet.MaxAttentionLimit}},
		{"pacto_fleet_overview", map[string]any{}},
		{"pacto_fleet_entity", map[string]any{"kind": "service", "key": string(fleet.NewServiceKey("k8s:shop/Deployment/cart"))}},
		{"pacto_fleet_entity", map[string]any{"kind": "offering", "key": "postgresql"}},
	} {
		res := callToolOn(t, server, c.tool, c.args)
		if res.IsError {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(resultText(t, res)), &v); err != nil {
			t.Fatal(err)
		}
		nextActions(v, &got)
	}
	return got
}

func TestFleetInstructionsNameCLI(t *testing.T) {
	if !strings.Contains(fleetInstructions, "nextAction names the operation, the CLI command and, when this server registers it, the tool") {
		t.Errorf("fleetInstructions: %s", fleetInstructions)
	}
}
