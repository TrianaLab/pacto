package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestFleetSchemaNamer(t *testing.T) {
	intType := reflect.TypeOf(0)
	cases := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"dashboard type keeps bare name", reflect.TypeOf(Service{}), "Service"},
		{"fleet type is prefixed", reflect.TypeOf(fleet.ServiceRecord{}), "FleetServiceRecord"},
		{"contract type is prefixed (resolves the Service clash)", reflect.TypeOf(contract.Service{}), "ContractService"},
		{"empty pkgpath passes through to the default namer", intType, huma.DefaultSchemaNamer(intType, "int")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fleetSchemaNamer(tc.typ, tc.typ.Name()); got != tc.want {
				t.Errorf("fleetSchemaNamer(%s) = %q, want %q", tc.typ, got, tc.want)
			}
		})
	}
}

// demoFleetQuery builds a small in-memory fleet query: one payment-service
// revision plus a non-compliant target for it.
func demoFleetQuery(t *testing.T) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{{
				Bundle: newPaymentBundle(), ResolvedRef: "oci://ghcr.io/org/payment-service:2.0.0", Digest: "sha256:abc",
			}},
			Targets: []fleet.RawTarget{{
				Scope: "production", Kind: "kubernetes-workload", Name: "pay/payment-service",
				Service: "payment-service", Compliance: fleet.StatusNonCompliant,
			}},
		}))
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	return fleet.NewQuery(snap)
}

// startFleetTestServer starts a dashboard server with an optional fleet provider.
func startFleetTestServer(t *testing.T, provider fleetProvider) (string, context.CancelFunc) {
	t.Helper()
	resolved := BuildResolvedSource(map[string]DataSource{"local": newOrderServiceSource()})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewResolvedServer(resolved, ui, []SourceInfo{{Type: "local", Enabled: true}}, nil)
	if provider != nil {
		srv.SetFleetProvider(provider)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String(), cancel
}

func TestFleetEndpoints_Serve(t *testing.T) {
	q := demoFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil })
	defer cancel()

	// Snapshot.
	var snap fleet.FleetSnapshot
	getJSON(t, base+"/api/fleet/snapshot", http.StatusOK, &snap)
	if len(snap.Services) == 0 {
		t.Fatal("expected services in snapshot")
	}

	// Search.
	var search fleet.SearchResult
	getJSON(t, base+"/api/fleet/services?text=payment", http.StatusOK, &search)
	if search.Count == 0 {
		t.Fatal("expected a search hit for payment")
	}
	if search.Meta.AsOf.IsZero() {
		t.Error("expected search meta asOf to be set")
	}

	// Graph (found + not found + invalid).
	var graph fleet.GraphResult
	getJSON(t, base+"/api/fleet/services/payment-service/graph?direction=dependents&transitive=true", http.StatusOK, &graph)
	if graph.Root != "payment-service" {
		t.Errorf("graph root = %q", graph.Root)
	}
	expectStatus(t, base+"/api/fleet/services/nonexistent/graph", http.StatusNotFound)
	// A bad direction is a malformed query (not a NotFound) → 422.
	expectStatus(t, base+"/api/fleet/services/payment-service/graph?direction=sideways", http.StatusUnprocessableEntity)

	// An invalid search filter → 422.
	expectStatus(t, base+"/api/fleet/services?status=Bogus", http.StatusUnprocessableEntity)

	// Status surfaces the non-compliant target.
	var status fleet.StatusResult
	getJSON(t, base+"/api/fleet/status", http.StatusOK, &status)
	if len(status.Items) == 0 {
		t.Fatal("expected attention items")
	}
}

func TestFleetEndpoints_ProviderError(t *testing.T) {
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) {
		return nil, fmt.Errorf("source down")
	})
	defer cancel()
	expectStatus(t, base+"/api/fleet/snapshot", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/services", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/services/x/graph", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/status", http.StatusServiceUnavailable)
}

func TestFleetEndpoints_NotRegisteredWithoutProvider(t *testing.T) {
	base, cancel := startFleetTestServer(t, nil)
	defer cancel()
	expectStatus(t, base+"/api/fleet/snapshot", http.StatusNotFound)
}

func getJSON(t *testing.T, url string, wantStatus int, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: expected %d, got %d", url, wantStatus, resp.StatusCode)
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
}

func expectStatus(t *testing.T, url string, wantStatus int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: expected %d, got %d", url, wantStatus, resp.StatusCode)
	}
}
