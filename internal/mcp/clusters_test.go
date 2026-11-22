package mcp

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func clusterModes(t *testing.T, p ClustersProvider) Modes {
	t.Helper()
	return Modes{Fleet: &FleetMode{Src: staticFleet(buildFleetQuery(t)), Opts: FleetOptions{Clusters: p}}}
}

func TestClusterListToolReadOnly(t *testing.T) {
	list := delivery.ClusterJoinList{Repository: "ghcr.io/acme/pacto-targets", AsOf: "2026-11-20T10:00:00Z", Completeness: fleet.CompletenessComplete,
		Clusters: []delivery.ClusterJoinState{{UID: "77ad31b0", Name: "us-west", State: delivery.JoinPending}}}
	server := mustNew("test", Authoring{}, clusterModes(t, func(context.Context) (delivery.ClusterJoinList, error) { return list, nil }))
	tool := listTools(t, server)["pacto_cluster_list"]
	if tool == nil {
		t.Fatal("pacto_cluster_list not listed")
	}
	if a := tool.Annotations; a == nil || !a.ReadOnlyHint {
		t.Errorf("annotations = %+v", tool.Annotations)
	}
	var got delivery.ClusterJoinList
	decode(t, callToolOn(t, server, "pacto_cluster_list", map[string]any{}), &got)
	if len(got.Clusters) != 1 || got.Clusters[0].State != delivery.JoinPending || got.Repository != list.Repository {
		t.Errorf("answer = %+v", got)
	}
	caps := capabilitiesOf(t, Authoring{}, clusterModes(t, func(context.Context) (delivery.ClusterJoinList, error) { return list, nil }))
	clusters := caps["installation"].(map[string]any)["clusters"].(map[string]any)
	if clusters["pending"] != 1.0 {
		t.Errorf("installation.clusters = %+v", clusters)
	}
	failing := mustNew("test", Authoring{}, clusterModes(t, func(context.Context) (delivery.ClusterJoinList, error) {
		return delivery.ClusterJoinList{}, errors.New("401 from ghcr.io")
	}))
	if r := callToolOn(t, failing, "pacto_cluster_list", map[string]any{}); !r.IsError {
		t.Error("an unread list answered as a list")
	}
}

func TestClusterListToolAbsentWithoutProvider(t *testing.T) {
	m := clusterModes(t, nil)
	if listTools(t, mustNew("test", Authoring{}, m))["pacto_cluster_list"] != nil {
		t.Error("pacto_cluster_list registered without a provider")
	}
	if strings.Contains(instructionsFor(Authoring{}, m), "pacto_cluster_list") {
		t.Error("instructions name pacto_cluster_list without a provider")
	}
}

// TestNoClusterWriteTool: joining and removing a member is a person's
// signed decision; no server registers a tool that makes it (#452).
func TestNoClusterWriteTool(t *testing.T) {
	for name, server := range instructionCombos(t) {
		for tool := range listTools(t, server) {
			if strings.HasPrefix(tool, "pacto_cluster_") && tool != "pacto_cluster_list" {
				t.Errorf("%s registers %s", name, tool)
			}
		}
	}
}

// agentCluster is a cluster called an agent: "agent" names an AI agent.
var agentCluster = regexp.MustCompile(`(?i)\b(agent cluster|cluster agent|agent role|joined agent|member agent)\b`)

func TestMCPTextNeverCallsAClusterAnAgent(t *testing.T) {
	m := composedModes(t, true)
	server := mustNew("test", Authoring{}, m)
	texts := []string{instructionsFor(Authoring{}, m)}
	for _, tool := range listTools(t, server) {
		texts = append(texts, tool.Description)
	}
	// meta.clusters[].link reaches every pacto_fleet_* answer with these docs.
	for _, typ := range []reflect.Type{reflect.TypeFor[fleet.ClusterSummary](), reflect.TypeFor[fleet.ClusterLink](), reflect.TypeFor[fleet.IssuerModel]()} {
		for f := range typ.Fields() {
			texts = append(texts, f.Tag.Get("doc"))
		}
	}
	for _, s := range texts {
		if loc := agentCluster.FindString(s); loc != "" {
			t.Errorf("MCP text calls a cluster an agent (%q): %.120s", loc, s)
		}
	}
	if !strings.Contains(clusterListTool().Description, "member") {
		t.Error("pacto_cluster_list does not say member")
	}
}

// TestClusterListToolShowsMappings: an approved member's group mappings
// reach the answer as issuer, rules, approvedBy and approvedAt.
func TestClusterListToolShowsMappings(t *testing.T) {
	list := delivery.ClusterJoinList{Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{{UID: "aaaa0000", Name: "eu-prod",
		State: delivery.JoinApproved, Mappings: []delivery.GroupMappingState{{Issuer: "https://login.acme.dev",
			Rules: []delivery.GroupRule{{From: "platform", To: "readers"}}, ApprovedBy: "oidc:alice@acme.com", ApprovedAt: "2026-11-20T10:00:00Z"}}}}}
	server := mustNew("test", Authoring{}, clusterModes(t, func(context.Context) (delivery.ClusterJoinList, error) { return list, nil }))
	var got struct {
		Clusters []struct {
			Mappings []map[string]any `json:"mappings"`
		} `json:"clusters"`
	}
	decode(t, callToolOn(t, server, "pacto_cluster_list", map[string]any{}), &got)
	if len(got.Clusters) != 1 || len(got.Clusters[0].Mappings) != 1 {
		t.Fatalf("%+v", got)
	}
	for _, k := range []string{"issuer", "rules", "approvedBy", "approvedAt"} {
		if _, ok := got.Clusters[0].Mappings[0][k]; !ok {
			t.Errorf("no %s in %+v", k, got.Clusters[0].Mappings[0])
		}
	}
	if !strings.Contains(clusterListTool().Description, "pacto cluster map-groups`; this server never writes them") {
		t.Error("the description does not say who writes group mappings")
	}
}
