package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// detectedQuery: Kubernetes targets in team-a plus an advisory detection
// source with a cluster-wide cloud row and an exposure row on a Gateway in
// infra, both with a hint.
func detectedQuery(t *testing.T) *fleet.Query {
	t.Helper()
	k8s := fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{
		Targets: []fleet.RawTarget{{Scope: "team-a", Kind: fleet.SourceKindKubernetes, Name: "app", Service: "app"}},
	})
	rows := detect.ProviderRows(detect.Result{SchemaVersion: "1", AsOf: fixedNow, Entries: []detect.Entry{
		{Kind: "cloud", Value: "GKE", Evidence: "on 6 of 6 nodes", Completeness: detect.Complete, Scope: fleet.ScopeCluster, Details: []string{"gke-pool-a"}},
		{Kind: "exposure", Value: "Gateway API", Evidence: "on a Gateway", Completeness: detect.Complete,
			Hint: "--set dashboard.httpRoute.parentRefs[0].namespace=infra", Objects: []detect.Object{{Kind: "Gateway", Namespace: "infra", Name: "public"}}},
	}}, "detection", fixedNow)
	det := fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, Providers: rows})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, k8s, det)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func detectedStatus(t *testing.T, q *fleet.Query) map[string]fleet.Provider {
	t.Helper()
	var out fleet.StatusResult
	decode(t, callHandler(t, fleetStatusTool(), fleetStatusHandler(q), map[string]any{}), &out)
	rows := map[string]fleet.Provider{}
	for _, p := range out.Providers {
		rows[p.ID] = p
	}
	return rows
}

func TestMCPFleetStatusCarriesDetectedRows(t *testing.T) {
	rows := detectedStatus(t, detectedQuery(t))
	for _, id := range []string{"cloud", "exposure"} {
		p := rows[id]
		if p.Provenance != fleet.ProvenanceInferred || p.AsOf == nil || p.Source != "detection" {
			t.Errorf("%s row = %+v, want inferred with asOf from detection", id, p)
		}
	}
	if !strings.Contains(rows["cloud"].Evidence, "GKE") || len(rows["cloud"].Details) != 1 {
		t.Errorf("cloud row = %+v", rows["cloud"])
	}
}

func TestMCPFleetStatusOmitsHint(t *testing.T) {
	q := detectedQuery(t)
	text := resultText(t, callHandler(t, fleetStatusTool(), fleetStatusHandler(q), map[string]any{}))
	if strings.Contains(text, `"hint"`) || strings.Contains(text, "--set") {
		t.Errorf("an agent got a ready-to-paste hint:\n%s", text)
	}
	// The snapshot keeps the hint for the CLI and the dashboard.
	if p := q.Providers().Providers; !strings.Contains(p[0].Hint+p[1].Hint, "--set") {
		t.Errorf("the handler cleared the snapshot's hint: %+v", p)
	}
}

// TestMCPFleetStatusDetectedRestricted: a viewer who may read only team-a
// gets the exposure row (a Gateway in infra) and the cluster-wide cloud row
// redacted.
func TestMCPFleetStatusDetectedRestricted(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, ns string) bool { return ns == "team-a" }, nil), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(detectedQuery(t)), Opts: FleetOptions{View: view}}})
	text := resultText(t, callToolOn(t, server, "pacto_fleet_status", map[string]any{}))
	for _, leak := range []string{"infra", "public", "GKE", "gke-pool-a", "6 of 6"} {
		if strings.Contains(text, leak) {
			t.Errorf("restricted status names %q:\n%s", leak, text)
		}
	}
	for _, want := range []string{"Exposure found in a namespace you cannot read", "Cloud detected cluster-wide; you may read only some namespaces"} {
		if !strings.Contains(text, want) {
			t.Errorf("restricted status lacks %q:\n%s", want, text)
		}
	}
}

func TestFleetStatusDescriptionNamesDetected(t *testing.T) {
	d := fleetStatusTool().Description
	for _, want := range []string{"what the operator detected", "provenance inferred", "asOf", "no provider row clears a gate"} {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q:\n%s", want, d)
		}
	}
}

// TestFleetStatusToolDescribesInstallation: the one sentence on detected rows
// names the declared Installation row and the Pull Secret proposal, and
// replaced the sentence that said every detected row is inferred.
func TestFleetStatusToolDescribesInstallation(t *testing.T) {
	d := fleetStatusTool().Description
	for _, want := range []string{"an Installation row with provenance declared naming the profile", "push prefixes", "a Pull Secret proposal",
		"a Pull Secret is never used until a person sets registryPullSecret"} {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "a detected value never clears a gate") || strings.Count(d, "providers includes") != 1 {
		t.Errorf("the old sentence was joined, not replaced:\n%s", d)
	}
}

// TestMCPFleetStatusOmitsPullSecretHint: an agent gets the Pull Secret
// proposal and the Installation row, never the --set registryPullSecret hint.
func TestMCPFleetStatusOmitsPullSecretHint(t *testing.T) {
	rows := detect.ProviderRows(detect.Result{SchemaVersion: "1", AsOf: fixedNow, Installation: &detect.Installation{Registry: "ghcr.io/acme"},
		Entries: []detect.Entry{{Kind: "pull-secret", Value: "shop/ghcr-pull", Completeness: detect.Complete,
			Hint: "--set registryPullSecret=shop/ghcr-pull", Scope: fleet.ScopeCluster}}}, "detection", fixedNow)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }},
		fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, Providers: rows}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	text := resultText(t, callHandler(t, fleetStatusTool(), fleetStatusHandler(q), map[string]any{}))
	if strings.Contains(text, "--set") || !strings.Contains(text, "shop/ghcr-pull") || !strings.Contains(text, `"provenance": "declared"`) {
		t.Errorf("status:\n%s", text)
	}
}

func TestServerInstructionsNameDetectedInferred(t *testing.T) {
	if !strings.Contains(fleetInstructions, "Pacto uses 'inferred' in five") ||
		!strings.Contains(fleetInstructions, "a detected provider row in pacto_fleet_status providers") {
		t.Errorf("instructions do not name detected rows as inferred:\n%s", fleetInstructions)
	}
}

func TestFleetStatusProvidersIncludeEgressRows(t *testing.T) {
	kernel := fleet.Provider{ID: fleet.ProviderKernelEgress, Name: "Kernel egress", Evidence: "node agent on 3/4 nodes",
		UsedFor: []string{fleet.UseExternalEgress}, Status: fleet.SourcePartial, Scope: fleet.ScopeCluster}
	k8s := fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Providers: []fleet.Provider{kernel}})
	det := fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, Providers: detect.ProviderRows(
		detect.Result{AsOf: fixedNow, Entries: []detect.Entry{detect.IstioEgress(true, nil), detect.CiliumFQDN(false, nil)}}, "detection", fixedNow)})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, k8s, det)
	if err != nil {
		t.Fatal(err)
	}
	rows := detectedStatus(t, fleet.NewQuery(snap))
	for id, status := range map[string]fleet.SourceStatus{"kernel-egress": fleet.SourcePartial, "istio-egress": fleet.SourceAvailable, "cilium-fqdn": fleet.SourceUnavailable} {
		if p, ok := rows[id]; !ok || p.Status != status || !slices.Contains(p.UsedFor, fleet.UseExternalEgress) {
			t.Errorf("%s row = %+v, want %s and used for %s", id, p, status, fleet.UseExternalEgress)
		}
	}
}
