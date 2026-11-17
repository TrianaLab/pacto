package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"

	"github.com/spf13/cobra"
)

type downSource struct{ id string }

func (s downSource) ID() string   { return s.id }
func (s downSource) Kind() string { return fleet.SourceKindKubernetes }
func (s downSource) Collect(context.Context) (*fleet.Collection, error) {
	return nil, errors.New("connection refused")
}

func providersQuery(t *testing.T, sources ...fleet.Source) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, sources...)
	expect(t, err == nil, "build: %v", err)
	return fleet.NewQuery(snap)
}

func clusterSource(id, cluster string, rows ...fleet.Provider) fleet.Source {
	return fleet.NewMemorySource(id, fleet.SourceKindKubernetes, &fleet.Collection{Cluster: &fleet.Cluster{ID: "uid-" + cluster, Name: cluster}, Providers: rows})
}

func crdProvider() fleet.Provider {
	return fleet.Provider{ID: fleet.ProviderPactoCRD, Name: "Pacto CRD", Evidence: "served (pacto.trianalab.io/v1alpha1)", UsedFor: []string{fleet.UseDeclaredTargets}}
}

func workloadsProvider() fleet.Provider {
	return fleet.Provider{ID: fleet.ProviderKubernetesWorkloads, Name: "Kubernetes workloads", UsedFor: []string{fleet.UseInferredServices},
		Counts:          []fleet.ProviderCount{{Kind: "Deployment", Namespace: "shop", N: 412}, {Kind: "CronJob", Namespace: "shop", N: 31}},
		UnreadableKinds: []string{"DaemonSet"}, Status: fleet.SourcePartial,
		Limitations: []fleet.Limitation{{Code: "WORKLOAD_LIST_FAILED", Message: "DaemonSets are forbidden"}}}
}

func providersText(t *testing.T, res *fleet.ProvidersResult) (string, string) {
	t.Helper()
	cmd := &cobra.Command{}
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	expect(t, printFleetProviders(cmd, res, "text") == nil, "print")
	return out.String(), errOut.String()
}

func TestFleetProvidersText(t *testing.T) {
	out, _ := providersText(t, providersQuery(t, clusterSource("k8s", "prod", crdProvider(), workloadsProvider())).Providers())
	for _, want := range []string{
		"PROVIDER", "EVIDENCE", "USED FOR",
		"Pacto CRD", "served (pacto.trianalab.io/v1alpha1)", "declared targets",
		"Kubernetes workloads", "412 Deployments, DaemonSets unreadable, 31 CronJobs", "inferred services (partial: DaemonSets are forbidden)",
	} {
		expect(t, strings.Contains(out, want), "providers text lacks %q:\n%s", want, out)
	}
	expect(t, !strings.Contains(out, "CLUSTER") && !strings.Contains(out, "shop"), "one cluster prints no column and no namespace:\n%s", out)
	expect(t, !regexp.MustCompile(`\((partial|stale|unavailable): ?\)`).MatchString(out), "an empty reason:\n%s", out)
	// A restricted view may drop a row's reason: the status still prints.
	expect(t, providerUsedFor(fleet.Provider{UsedFor: []string{"a-b"}, Status: fleet.SourceStale}) == "a b (stale)", "status alone")
	// The bound says it cut the list.
	cut, _ := providersText(t, &fleet.ProvidersResult{Total: 300, Truncated: true, Providers: []fleet.Provider{crdProvider()}})
	expect(t, strings.Contains(cut, "... 1 of 300 rows shown"), "truncated:\n%s", cut)
}

func TestFleetProvidersJSON(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	expect(t, printFleetProviders(cmd, providersQuery(t, clusterSource("k8s", "prod", crdProvider())).Providers(), "json") == nil, "print")
	var res struct {
		Meta struct {
			SchemaVersion string          `json:"schemaVersion"`
			SourceCounts  json.RawMessage `json:"sourceCounts"`
		} `json:"meta"`
		Providers []fleet.Provider `json:"providers"`
	}
	expect(t, json.Unmarshal(out.Bytes(), &res) == nil, "json: %s", out.String())
	expect(t, res.Meta.SchemaVersion == fleet.ProductSchemaVersion && len(res.Meta.SourceCounts) > 0 && len(res.Providers) == 1, "providers json: %s", out.String())
}

func TestFleetProvidersClusterColumnOnlyWithManyClusters(t *testing.T) {
	one, _ := providersText(t, providersQuery(t, clusterSource("k8s", "prod", crdProvider()), clusterSource("k8s-workloads", "prod", workloadsProvider())).Providers())
	expect(t, !strings.Contains(one, "CLUSTER"), "two sources in one cluster print no column:\n%s", one)
	two, _ := providersText(t, providersQuery(t, clusterSource("k8s-eu", "eu-prod", crdProvider()), clusterSource("k8s-us", "us-prod", crdProvider())).Providers())
	expect(t, regexp.MustCompile(`(?m)^CLUSTER +PROVIDER +EVIDENCE +USED FOR$`).MatchString(two) && strings.Contains(two, "eu-prod") && strings.Contains(two, "us-prod"), "two clusters:\n%s", two)
}

func TestFleetProvidersNoKubernetesSource(t *testing.T) {
	res := cliJSON(t, []string{"--local", t.TempDir()}, "fleet", "providers")
	expect(t, strings.Contains(res, `"providers": []`) && strings.Contains(res, fleet.ProductSchemaVersion), "pacto fleet providers json: %s", res)
	out, _ := providersText(t, providersQuery(t, fleet.NewMemorySource("local", "local", &fleet.Collection{})).Providers())
	expect(t, strings.TrimSpace(out) == "No providers detected: this view reads no Kubernetes cluster (add --k8s)", "no k8s:\n%s", out)
}

func TestFleetProvidersUnavailableSourceNamed(t *testing.T) {
	out, stderr := providersText(t, providersQuery(t, downSource{"k8s-down"}, fleet.NewMemorySource("local", "local", &fleet.Collection{})).Providers())
	expect(t, strings.Contains(out, "k8s-down  not read (") && !strings.Contains(out, "PROVIDER") && !strings.Contains(out, "No providers detected"), "unread:\n%s", out)
	expect(t, !strings.Contains(out, "not installed"), "unreachable is never not installed:\n%s", out)
	expect(t, strings.Contains(stderr, "warning: answer is"), "the partial answer warns: %s", stderr)
}

// TestFleetProvidersParity: one query, read through the CLI, the dashboard
// route and pacto_fleet_status, carries the same rows.
func TestFleetProvidersParity(t *testing.T) {
	q := providersQuery(t, clusterSource("k8s", "prod", crdProvider(), workloadsProvider()))
	src := func(context.Context) (*fleet.Query, error) { return q, nil }
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	expect(t, printFleetProviders(cmd, q.Providers(), "json") == nil, "print")
	server, err := pactomcp.New("v", pactomcp.Authoring{}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{Src: src}})
	expect(t, err == nil, "server: %v", err)
	status, _ := callText(t, sessionOn(t, server), "pacto_fleet_status", map[string]any{})
	route := routeBody(t, dashboard.NewServer(dashboard.Options{Fleet: src}).APIHandler(), context.Background(), "/api/fleet/providers")
	providers := func(what, body string) string {
		var v struct {
			Providers json.RawMessage `json:"providers"`
		}
		expect(t, json.Unmarshal([]byte(body), &v) == nil && len(v.Providers) > 2, "%s: %s", what, body)
		return string(v.Providers)
	}
	sameOnSurfaces(t, "providers", map[string]string{"cli": providers("cli", out.String()), "mcp": providers("mcp", status), "dashboard": providers("dashboard", route)})
}
