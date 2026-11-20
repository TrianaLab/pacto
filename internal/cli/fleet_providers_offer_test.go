package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func argoProvider() fleet.Provider {
	return fleet.Provider{ID: fleet.ProviderArgoCD, Name: "Argo CD", Evidence: "3 Applications", UsedFor: []string{fleet.UseRepositoryProvenance}}
}

func TestFleetProvidersPrintsOffer(t *testing.T) {
	out, _ := providersText(t, providersQuery(t, clusterSource("k8s", "prod", crdProvider(), argoProvider())).Providers())
	want := "  offer: Gate every Argo CD sync with one repo-server plugin (" + fleet.ArgoSyncGateDoc + ")\n"
	expect(t, strings.Count(out, "offer:") == 1 && strings.Contains(out, want), "one offer line under the Argo CD row:\n%s", out)
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "offer:") {
			expect(t, strings.HasPrefix(lines[i-1], "Argo CD") && strings.Index(l, "offer:")-2 == strings.Index(lines[0], "EVIDENCE"), "offer sits under the Argo CD evidence:\n%s", out)
		}
	}
	two, _ := providersText(t, providersQuery(t, clusterSource("k8s-eu", "eu-prod", argoProvider()), clusterSource("k8s-us", "us-prod", crdProvider())).Providers())
	head := strings.Split(two, "\n")[0]
	expect(t, strings.Contains(two, "\n"+strings.Repeat(" ", strings.Index(head, "EVIDENCE"))+"  offer: "), "with a cluster column the offer still sits under the evidence:\n%s", two)
}

// TestProviderOfferSameOnCLIAndMCP: pacto fleet providers and
// pacto_fleet_status carry byte-identical offers over one snapshot.
func TestProviderOfferSameOnCLIAndMCP(t *testing.T) {
	q := providersQuery(t, clusterSource("k8s", "prod", argoProvider()))
	src := func(context.Context) (*fleet.Query, error) { return q, nil }
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	expect(t, printFleetProviders(cmd, q.Providers(), "json") == nil, "print")
	server, err := pactomcp.New("v", pactomcp.Authoring{}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{Src: src}})
	expect(t, err == nil, "server: %v", err)
	status, _ := callText(t, sessionOn(t, server), "pacto_fleet_status", map[string]any{})
	offers := func(what, body string) string {
		var v struct {
			Providers []struct {
				Offers json.RawMessage `json:"offers"`
			} `json:"providers"`
		}
		expect(t, json.Unmarshal([]byte(body), &v) == nil && len(v.Providers) == 1 && len(v.Providers[0].Offers) > 2, "%s: %s", what, body)
		var compact bytes.Buffer
		expect(t, json.Compact(&compact, v.Providers[0].Offers) == nil, "compact")
		return compact.String()
	}
	cli, mcp := offers("cli", out.String()), offers("mcp", status)
	expect(t, cli == mcp && strings.Contains(cli, `"id":"argocd-repo-server-plugin"`), "cli %s != mcp %s", cli, mcp)
}
