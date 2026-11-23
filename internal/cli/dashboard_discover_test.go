package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDashboardRegistryDiscoversGoldenPaths: a local dashboard given
// --registry starts on it alone, names it as a source, and its fleet serves
// the golden paths published under it on /api/fleet/entities.
func TestDashboardRegistryDiscoversGoldenPaths(t *testing.T) {
	e, svc := discoverFixture(t, 0)
	isolateHost(t)
	v := viper.New()
	stderr, err := runDashboard(t, svc, v, t.TempDir(), "--port", "0", "--registry", e.discoverPrefix())
	if err != nil || !strings.Contains(stderr, "Sources: registry") {
		t.Fatalf("%v:\n%s", err, stderr)
	}
	snap, err := svc.Fleet(t.Context(), dashboardFleetOptions("", nil, nil, nil, dashboardSources{registry: v.GetString("dashboard.registry")}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	mux := http.NewServeMux()
	dashboard.NewServer(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }}).
		RegisterOperations(humago.New(mux, dashboard.APIConfig()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fleet/entities?kinds=service&template=only", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "go-http-postgres") || !strings.Contains(body, "node-worker") || strings.Contains(body, "billing") {
		t.Errorf("%d:\n%s", rec.Code, body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fleet/entities?kinds=offering", nil))
	if !strings.Contains(rec.Body.String(), "postgresql") {
		t.Errorf("offerings:\n%s", rec.Body.String())
	}
}

// TestDashboardWithoutRegistryUnchanged: without a registry the dashboard
// discovers nothing and names no registry source.
func TestDashboardWithoutRegistryUnchanged(t *testing.T) {
	fopts := dashboardFleetOptions("", []string{"ghcr.io/acme/a"}, nil, nil, dashboardSources{})
	if fopts.Discover != nil || !slices.Equal(configuredSources(fopts), []string{"oci"}) {
		t.Errorf("Discover %v, sources %v", fopts.Discover, configuredSources(fopts))
	}
}
