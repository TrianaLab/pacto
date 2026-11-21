package dashboard

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const argoCartKey = "argocd//cart"

// groupedServer serves cart and worker, the Argo CD Application cart whose
// repository Pacto read from the Application in argocd, beside ledger, an
// ungrouped StatefulSet, and orders, a declared service. view narrows the
// query the way a signed-in viewer's permissions would; nil serves it whole.
func groupedServer(t *testing.T, view func(*fleet.Query) *fleet.Query) string {
	t.Helper()
	argo := &fleet.InferredGroup{Rule: "argocd", Name: "cart", From: "label argocd.argoproj.io/instance"}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "shop", Kind: "kubernetes", Name: "orders", Service: "orders"}}}),
		fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{
			{Namespace: "shop", Kind: "Deployment", Name: "cart", Group: argo, Repositories: []fleet.InferredRepository{
				{URL: "https://github.com/acme/shop", Path: "deploy/cart", From: "Application argocd/cart spec.source.repoURL", Namespace: "argocd", Kind: "Application"}}},
			{Namespace: "shop", Kind: "Deployment", Name: "worker", Group: argo},
			{Namespace: "shop", Kind: "StatefulSet", Name: "ledger"},
		}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	if view != nil {
		q = view(q)
	}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	t.Cleanup(cancel)
	return base
}

// groupRows are the entity rows of url with their group key and repository.
func groupRows(t *testing.T, url string) map[string]string {
	t.Helper()
	var l struct {
		Entities []struct {
			Kind, Key, Repository string
			Group                 *fleet.InferredGroup
		}
	}
	getJSON(t, url, http.StatusOK, &l)
	out := map[string]string{}
	for _, e := range l.Entities {
		g := ""
		if e.Group != nil {
			g = e.Group.Key
		}
		out[e.Kind+"/"+e.Key] = g + " " + e.Repository
	}
	return out
}

func TestFleetEntitiesRouteGroup(t *testing.T) {
	base := groupedServer(t, nil)
	cart, worker := "service/"+string(fleet.NewServiceKey(cartName)), "service/"+string(fleet.NewServiceKey("k8s:shop/Deployment/worker"))
	want := map[string]string{cart: argoCartKey + " https://github.com/acme/shop", worker: argoCartKey + " "}
	for _, q := range []string{"group=" + argoCartKey + "&kinds=service", "group=" + argoCartKey} {
		got := groupRows(t, base+"/api/fleet/entities?"+q)
		if len(got) != len(want) || got[cart] != want[cart] || got[worker] != want[worker] {
			t.Errorf("%s: rows %v, want %v", q, got, want)
		}
	}
	expectStatus(t, base+"/api/fleet/entities?group="+strings.Repeat("g", 1025), http.StatusUnprocessableEntity)
	expectStatus(t, base+"/api/fleet/entities?group="+argoCartKey+"&kinds=owner", http.StatusUnprocessableEntity)
}

func TestFleetEntitiesRouteHidesRepository(t *testing.T) {
	noApps := func(q *fleet.Query) *fleet.Query {
		return q.Restrict(func(_, _ string) bool { return true }, func(_, ns, kind string) bool { return ns != "argocd" || kind != "Application" })
	}
	base := groupedServer(t, noApps)
	for key, row := range groupRows(t, base+"/api/fleet/entities?declaration=inferred") {
		if strings.Contains(row, "github.com") {
			t.Errorf("%s keeps the hidden repository: %q", key, row)
		}
	}
	res, err := http.Get(base + "/api/fleet/entities/service?key=" + url.QueryEscape(string(fleet.NewServiceKey(cartName))))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(body), "github.com/acme/shop") || !strings.Contains(string(body), "hidden by your permissions") || !strings.Contains(string(body), argoCartKey) {
		t.Errorf("detail of a viewer who may not list Applications:\n%s", body)
	}
}

func TestFleetEntityRouteCarriesGroup(t *testing.T) {
	base := groupedServer(t, nil)
	var d struct {
		Service struct{ Workload *fleet.InferredWorkload }
	}
	getJSON(t, base+"/api/fleet/entities/service?key="+url.QueryEscape(string(fleet.NewServiceKey(cartName))), http.StatusOK, &d)
	w := d.Service.Workload
	if w == nil || w.Group == nil || w.Group.Key != argoCartKey || w.Group.Label != "Argo CD Application cart" || w.Group.From == "" {
		t.Fatalf("workload group %+v", w)
	}
	if len(w.Repositories) != 1 || w.Repositories[0].URL != "https://github.com/acme/shop" || w.Repositories[0].Path != "deploy/cart" ||
		w.Repositories[0].From != "Application argocd/cart spec.source.repoURL" {
		t.Errorf("repositories %+v", w.Repositories)
	}
}
