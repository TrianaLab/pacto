package cli

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// meshPartialSource is an advisory mesh source whose Istio row is partial.
func meshPartialSource() fleet.Source {
	return fleet.NewMemorySource("uid-prod-mesh", fleet.SourceKindMesh, &fleet.Collection{
		Advisory: true, Cluster: &fleet.Cluster{ID: "uid-prod", Name: "prod"}, ObservedUndeclaredOnly: true,
		State: &fleet.SourceState{Status: fleet.SourcePartial},
		Providers: []fleet.Provider{{ID: fleet.ProviderIstio, Name: "Istio", Evidence: "serves networking.istio.io; calls read from Prometheus",
			UsedFor: []string{fleet.UseObservedEdges}, Status: fleet.SourcePartial,
			Limitations: []fleet.Limitation{{Code: fleet.LimitationMeshCoveragePartial, Message: "3 namespaces without sidecars"}}}},
	})
}

func TestFleetProvidersPrintsMeshRow(t *testing.T) {
	out, _ := providersText(t, providersQuery(t, meshPartialSource()).Providers())
	evidence := "serves networking.istio.io; calls read from Prometheus"
	want := fmt.Sprintf("%-10s%-*s%s\n%-10s%-*s%s\n", "PROVIDER", len(evidence)+2, "EVIDENCE", "USED FOR",
		"Istio", len(evidence)+2, evidence, "observed edges (partial: 3 namespaces without sidecars)")
	expect(t, out == want, "providers text:\n%q\nwant\n%q", out, want)
}

func TestPrintAdvisorySourcesSkipsNonDetection(t *testing.T) {
	var buf bytes.Buffer
	mesh := []fleet.SourceState{{ID: "uid-prod-mesh", Kind: fleet.SourceKindMesh, Status: fleet.SourcePartial, Advisory: true}}
	expect(t, !printAdvisorySources(&buf, mesh) && buf.Len() == 0, "a mesh source printed %q", buf.String())
	expect(t, printAdvisorySources(&buf, advisorySource()) && buf.String() == "Detected: detection not read (not found: the operator has not published detection yet)\n",
		"detection line: %q", buf.String())
	var overview bytes.Buffer
	printIncompleteSources(&overview, mesh, false)
	expect(t, overview.Len() == 0, "overview printed %q for a partial mesh source", overview.String())
}
