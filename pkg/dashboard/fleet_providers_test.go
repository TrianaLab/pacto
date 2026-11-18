package dashboard

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// providerDashboardQuery: a Kubernetes source with targets in shop and
// hidden-ns and one provider row whose object and limitation name hidden-ns.
func providerDashboardQuery(t *testing.T) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "app", Service: "app"},
			{Scope: "hidden-ns", Kind: fleet.SourceKindKubernetes, Name: "vault", Service: "vault"},
		},
		Providers: []fleet.Provider{{ID: "prometheus", Name: "Prometheus", Evidence: "served (monitoring.coreos.com/v1)", UsedFor: []string{"observed-calls"},
			Status: fleet.SourcePartial, Objects: []fleet.ProviderObject{{Kind: "Prometheus", Namespace: "hidden-ns", Name: "prom"}, {Kind: "Prometheus", Namespace: "shop", Name: "shop-prom"}},
			Limitations: []fleet.Limitation{{Code: "SCRAPE", Message: "hidden-ns scrape failed"}}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestFleetProvidersRoute(t *testing.T) {
	q := providerDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()
	var res fleet.ProvidersResult
	getJSON(t, base+"/api/fleet/providers", http.StatusOK, &res)
	if res.Meta.SchemaVersion != fleet.ProductSchemaVersion || res.Total != 1 || len(res.Providers) != 1 || len(res.Providers[0].Objects) != 2 {
		t.Errorf("providers = %+v", res)
	}
	errBase, errCancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return nil, context.Canceled }, nil)
	defer errCancel()
	expectStatus(t, errBase+"/api/fleet/providers", http.StatusServiceUnavailable)
}

// TestFleetProvidersRouteRestricted: the row stays for a viewer who may not
// read hidden-ns; its hidden-ns object and limitation are gone.
func TestFleetProvidersRouteRestricted(t *testing.T) {
	q := providerDashboardQuery(t)
	base := serveOptions(t, Options{
		Fleet: func(context.Context) (*fleet.Query, error) { return q, nil },
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(ns string) bool { return ns == "shop" }, nil), nil
		},
		Auth: &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }},
	})
	resp, err := http.Get(base + "/api/fleet/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "hidden-ns") || !strings.Contains(string(body), `"prometheus"`) || !strings.Contains(string(body), "shop-prom") {
		t.Errorf("restricted providers = %d %s", resp.StatusCode, body)
	}
}
