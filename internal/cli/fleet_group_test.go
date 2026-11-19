package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	groupCart   = "k8s:shop/Deployment/cart"
	groupLedger = "k8s:shop/StatefulSet/ledger"
	argoCartKey = "argocd//cart"
)

// groupedQuery: cart and worker are the Argo CD Application cart, whose
// repository Pacto read; ledger is the Flux Kustomization shop/ledger whose
// repository it could not read.
func groupedQuery(t *testing.T) *fleet.Query {
	t.Helper()
	argo := &fleet.InferredGroup{Rule: "argocd", Name: "cart", From: "label argocd.argoproj.io/instance"}
	repo := []fleet.InferredRepository{{URL: "https://github.com/acme/shop", Path: "deploy/cart", From: "Application argocd/cart spec.source.repoURL", Namespace: "argocd", Kind: "Application"}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
		InferredServices: []fleet.RawInferredService{
			{Namespace: "shop", Kind: "Deployment", Name: "cart", Image: &fleet.InferredValue{Value: "registry.example/cart:2.0.1", From: "Deployment spec.template.spec.containers[name=cart].image"}, Group: argo, Repositories: repo},
			{Namespace: "shop", Kind: "Deployment", Name: "worker", Group: argo},
			{Namespace: "shop", Kind: "StatefulSet", Name: "ledger", RepositoryUnknown: "GitRepositories unreadable (forbidden)",
				Group: &fleet.InferredGroup{Rule: "flux-kustomization", Namespace: "shop", Name: "ledger", From: "labels kustomize.toolkit.fluxcd.io/name and namespace"}},
		},
	}))
	expect(t, err == nil, "build: %v", err)
	return fleet.NewQuery(snap)
}

func TestFleetEntitiesGroupFlag(t *testing.T) {
	cmd := newFleetEntitiesCommand(app.NewService(nil, nil), viper.New())
	expect(t, cmd.ParseFlags([]string{"--group", argoCartKey}) == nil, "flags")
	f := entityFilterFromCmd(cmd, "")
	expect(t, f.Group == argoCartKey && len(f.Kinds) == 1 && f.Kinds[0] == fleet.KindService, "filter %+v", f)
	l, err := groupedQuery(t).Entities(f)
	expect(t, err == nil && l.Total == 2, "group entities %v %+v", err, l)
	for _, e := range l.Entities {
		expect(t, e.Group != nil && e.Group.Key == argoCartKey, "row %+v outside the group", e)
	}
}

func TestFleetEntitiesPrintsGroupOnInferredRow(t *testing.T) {
	l, err := groupedQuery(t).Entities(fleet.EntityFilter{Declaration: fleet.DeclarationInferred, Kinds: []fleet.EntityKind{fleet.KindService}, Sort: fleet.SortName})
	expect(t, err == nil, "entities: %v", err)
	out := printed(t, func(c *cobra.Command) error { return printFleetEntities(c, l, "text") })
	want := `3 of 3 entit(ies):
  inferred    k8s:shop/Deployment/cart     k8s:shop%2FDeployment%2Fcart     -
              namespace shop  kind Deployment  image registry.example/cart:2.0.1  group Argo CD Application cart
  inferred    k8s:shop/Deployment/worker   k8s:shop%2FDeployment%2Fworker   -
              namespace shop  kind Deployment  image -  group Argo CD Application cart
  inferred    k8s:shop/StatefulSet/ledger  k8s:shop%2FStatefulSet%2Fledger  -
              namespace shop  kind StatefulSet  image -  group Flux Kustomization shop/ledger
`
	expect(t, out == want, "entities:\n%s\nwant:\n%s", out, want)
	// A row with no group (never one Build makes) prints a dash.
	out = printed(t, func(c *cobra.Command) error {
		return printFleetEntities(c, &fleet.EntityList{Count: 1, Total: 1, Entities: []fleet.EntityRef{{Kind: fleet.KindService, Declaration: fleet.DeclarationInferred}}}, "text")
	})
	expect(t, strings.Contains(out, "  group -\n"), "no group:\n%s", out)
}

func TestFleetEntityPrintsGroupAndRepository(t *testing.T) {
	q := groupedQuery(t)
	d, err := q.EntityDetail(fleet.KindService, string(fleet.NewServiceKey(groupCart)))
	expect(t, err == nil, "detail: %v", err)
	out := printed(t, func(c *cobra.Command) error { return printFleetEntity(c, d, "text") })
	want := "Workload: Deployment shop/cart\n" +
		"  image registry.example/cart:2.0.1 (from k8s-workloads: Deployment spec.template.spec.containers[name=cart].image)\n" +
		"  group Argo CD Application cart (from k8s-workloads: label argocd.argoproj.io/instance)\n" +
		"  group key " + argoCartKey + " (pacto fleet entities --group takes it)\n" +
		"  repository https://github.com/acme/shop path deploy/cart (from Application argocd/cart spec.source.repoURL)\n"
	expect(t, strings.Contains(out, want), "entity lacks the group block:\n%s", out)
	out = printed(t, func(c *cobra.Command) error { return printFleetEntity(c, d, "json") })
	expect(t, strings.Contains(out, `"key": "`+argoCartKey+`"`) && strings.Contains(out, `"repositories"`), "json lacks the group:\n%s", out)
}

func TestFleetEntityPrintsRepositoryUnknown(t *testing.T) {
	d, err := groupedQuery(t).EntityDetail(fleet.KindService, string(fleet.NewServiceKey(groupLedger)))
	expect(t, err == nil, "detail: %v", err)
	out := printed(t, func(c *cobra.Command) error { return printFleetEntity(c, d, "text") })
	for _, want := range []string{
		"  group Flux Kustomization shop/ledger (from k8s-workloads: labels kustomize.toolkit.fluxcd.io/name and namespace)\n",
		"  repository unknown: GitRepositories unreadable (forbidden)\n",
	} {
		expect(t, strings.Contains(out, want), "entity lacks %q:\n%s", want, out)
	}
	expect(t, !strings.Contains(out, "  repository http"), "an unread repository prints a URL:\n%s", out)
}
