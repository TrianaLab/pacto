package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

func TestFleetSchemaNamer(t *testing.T) {
	intType := reflect.TypeOf(0)
	cases := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		// Both entries in unqualifiedSchemaPkgs are asserted: Service is declared
		// in pkg/contractview and only aliased here, so it would not cover the
		// "/pkg/dashboard" entry on its own.
		{"contract-view type keeps bare name", reflect.TypeOf(Service{}), "Service"},
		{"dashboard-declared type keeps bare name", reflect.TypeOf(SourceInfo{}), "SourceInfo"},
		{"fleet type is prefixed", reflect.TypeOf(fleet.ServiceRecord{}), "Fleet.ServiceRecord"},
		{"impact type is prefixed (no collision with dashboard/fleet)", reflect.TypeOf(impact.AffectedConsumer{}), "Impact.AffectedConsumer"},
		{"pointer body type dereferences to its package", reflect.TypeOf(&impact.Result{}), "Impact.Result"},
		{"contract type is prefixed (resolves the Service clash)", reflect.TypeOf(contract.Service{}), "Contract.Service"},
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

// newPaymentBundle is the shared contract fixture the fleet, impact and document
// tests build their snapshots from.
func newPaymentBundle() *contract.Bundle {
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: "payment-service", Version: "2.0.0"},
			Interfaces:   []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI}},
			Workload:     contract.WorkloadService,
			State: &contract.State{
				Type:            contract.StateStateless,
				Persistence:     contract.Persistence{Scope: contract.ScopeLocal, Durability: contract.DurabilityEphemeral},
				DataCriticality: contract.DataCriticalityLow,
			},
		},
		RawYAML: []byte("pactoVersion: \"2.0\"\nservice:\n  name: payment-service\n  version: \"2.0.0\"\ninterfaces:\n  - name: api\n    type: openapi\nworkload: service\nstate:\n  type: stateless\n  persistence:\n    scope: local\n    durability: ephemeral\n  dataCriticality: low\n"),
	}
}

// demoFleetQuery builds a small in-memory fleet query: one payment-service
// revision plus a non-compliant target for it.
func demoFleetQuery(t *testing.T) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{{
				Bundle: newPaymentBundle(), ResolvedRef: "oci://ghcr.io/org/payment-service@" + validDigest("a"), Digest: validDigest("a"),
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

// startFleetTestServer starts a dashboard server with an optional fleet provider
// and an optional impact provider.
func startFleetTestServer(t *testing.T, provider fleetProvider, impactFn impactProviderFunc) (string, context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(testUI())
	if provider != nil {
		srv.SetFleetProvider(provider)
	}
	if impactFn != nil {
		srv.SetImpactProvider(impactFn)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String(), cancel
}

func TestFleetEndpoints_Serve(t *testing.T) {
	q := demoFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
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
	// maxNodes reaches the engine: the answer is cut to the budget and says so,
	// rather than growing with the fleet.
	var bounded fleet.GraphResult
	getJSON(t, base+"/api/fleet/services/payment-service/graph?direction=dependents&transitive=true&maxNodes=1", http.StatusOK, &bounded)
	if len(bounded.Nodes) > 1 {
		t.Errorf("maxNodes=1 returned %d nodes", len(bounded.Nodes))
	}
	if len(graph.Nodes) > 1 && !bounded.Truncated {
		t.Error("a graph cut short by maxNodes must report truncation")
	}
	expectStatus(t, base+"/api/fleet/services/payment-service/graph?maxNodes=-1", http.StatusUnprocessableEntity)

	// An invalid search filter → 422.
	expectStatus(t, base+"/api/fleet/services?status=Bogus", http.StatusUnprocessableEntity)

	// Status surfaces the non-compliant target.
	var status fleet.StatusResult
	getJSON(t, base+"/api/fleet/status", http.StatusOK, &status)
	if len(status.Items) == 0 {
		t.Fatal("expected attention items")
	}
}

func TestCapabilitiesEndpoint(t *testing.T) {
	// With both providers set, capabilities reports them enabled. Observed stays
	// off unless a host explicitly declares an observation source.
	q := demoFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil },
		func(context.Context, string, string, bool) (*impact.Result, error) { return &impact.Result{}, nil })
	defer cancel()
	var caps struct {
		Fleet    bool `json:"fleet"`
		Impact   bool `json:"impact"`
		Observed bool `json:"observed"`
	}
	getJSON(t, base+"/api/capabilities", http.StatusOK, &caps)
	if !caps.Fleet || !caps.Impact || caps.Observed {
		t.Errorf("expected fleet+impact enabled, observed off, got %+v", caps)
	}

	// With no providers, capabilities reports them disabled (so the frontend can
	// hide the corresponding navigation).
	baseOff, cancelOff := startFleetTestServer(t, nil, nil)
	defer cancelOff()
	getJSON(t, baseOff+"/api/capabilities", http.StatusOK, &caps)
	if caps.Fleet || caps.Impact {
		t.Errorf("expected fleet+impact disabled, got %+v", caps)
	}
}

func TestCapabilities_ObservedDerivedFromSnapshot(t *testing.T) {
	// observed=true is DERIVED from the published snapshot carrying an observed
	// relationship, not a hardcoded flag, so it can never be a placebo (review S4).
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{{
		FromService: "eu/a", ToService: "eu/b", Type: fleet.RelationshipDependency,
		Provenance: fleet.ProvenanceObserved, Resolved: true, ObservedCount: 1,
	}}}
	srv := NewServer(nil)
	srv.SetFleetProvider(func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(snap), nil })
	out, err := srv.capabilities(context.Background(), nil)
	if err != nil || !out.Body.Observed {
		t.Errorf("expected observed=true derived from the snapshot, got %+v err=%v", out.Body, err)
	}
}

func TestFleetDetailEndpoints(t *testing.T) {
	q := demoFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	// Service detail by key (default-domain key == name): revisions + targets.
	var sv fleet.ServiceView
	getJSON(t, base+"/api/fleet/service?key=payment-service", http.StatusOK, &sv)
	if sv.Service == nil || sv.Service.Name != "payment-service" {
		t.Fatalf("service detail: %+v", sv.Service)
	}
	if len(sv.Revisions) == 0 || len(sv.Targets) == 0 {
		t.Errorf("service detail must carry revisions and targets: %+v", sv)
	}
	expectStatus(t, base+"/api/fleet/service?key=nope", http.StatusNotFound)

	// Target detail by name (unique; the name carries a slash, so URL-encoded):
	// exact linked revision included.
	var tv fleet.TargetView
	getJSON(t, base+"/api/fleet/target?key=pay%2Fpayment-service", http.StatusOK, &tv)
	if tv.Target == nil || tv.Target.Service != "payment-service" {
		t.Fatalf("target detail: %+v", tv.Target)
	}
	expectStatus(t, base+"/api/fleet/target?key=nope", http.StatusNotFound)

	// The detail endpoints are gated on the provider like the rest.
	baseOff, cancelOff := startFleetTestServer(t, nil, nil)
	defer cancelOff()
	expectStatus(t, baseOff+"/api/fleet/service?key=payment-service", http.StatusNotFound)
	expectStatus(t, baseOff+"/api/fleet/target?key=x", http.StatusNotFound)
}

// TestFleetServiceDetail_DomainIsolation proves section 3 at the API boundary: two
// same-named services in different domains are distinct records; a bare name is
// ambiguous (422, qualify it) while the domain-qualified key resolves exactly one.
func TestFleetServiceDetail_DomainIsolation(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("a", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: newPaymentBundle(), Domain: "domain-a", ResolvedRef: "oci://a/payment-service@sha256:a", Digest: "sha256:a",
		}}}),
		fleet.NewMemorySource("b", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: newPaymentBundle(), Domain: "domain-b", ResolvedRef: "oci://b/payment-service@sha256:b", Digest: "sha256:b",
		}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	expectStatus(t, base+"/api/fleet/service?key=payment-service", http.StatusUnprocessableEntity)

	var sv fleet.ServiceView
	getJSON(t, base+"/api/fleet/service?key=domain-a%2Fpayment-service", http.StatusOK, &sv)
	if sv.Service == nil || sv.Service.Domain != "domain-a" {
		t.Fatalf("qualified key must resolve exactly one domain: %+v", sv.Service)
	}
}

func TestFleetEndpoints_ProviderError(t *testing.T) {
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) {
		return nil, fmt.Errorf("source down")
	}, nil)
	defer cancel()
	expectStatus(t, base+"/api/fleet/snapshot", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/services", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/service?key=x", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/target?key=x", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/services/x/graph", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/status", http.StatusServiceUnavailable)
}

func TestFleetEndpoints_NotRegisteredWithoutProvider(t *testing.T) {
	base, cancel := startFleetTestServer(t, nil, nil)
	defer cancel()
	expectStatus(t, base+"/api/fleet/snapshot", http.StatusNotFound)
	// Impact is independently gated: no impact provider → not registered.
	expectStatus(t, base+"/api/fleet/impact?old=a&new=b", http.StatusNotFound)
}

func TestFleetImpactEndpoint(t *testing.T) {
	want := &impact.Result{
		SchemaVersion:  impact.SchemaVersion,
		Service:        "payment-service",
		Classification: "BREAKING",
		Consumers: []impact.AffectedConsumer{
			{Service: "order-service", Depth: 1, Direct: true, Confidence: impact.ConfidenceContractual},
		},
	}
	var gotObserved bool
	base, cancel := startFleetTestServer(t, nil, func(_ context.Context, oldRef, newRef string, includeObserved bool) (*impact.Result, error) {
		gotObserved = includeObserved
		if oldRef == "" || newRef == "" {
			t.Errorf("expected old/new refs, got %q/%q", oldRef, newRef)
		}
		return want, nil
	})
	defer cancel()

	var out impact.Result
	getJSON(t, base+"/api/fleet/impact?old=oci://x/svc:1.0.0&new=oci://x/svc:2.0.0&includeObserved=true", http.StatusOK, &out)
	if out.Service != "payment-service" || out.Classification != "BREAKING" {
		t.Fatalf("unexpected impact result: %+v", out)
	}
	if len(out.Consumers) != 1 || out.Consumers[0].Service != "order-service" {
		t.Fatalf("unexpected consumers: %+v", out.Consumers)
	}
	if !gotObserved {
		t.Error("expected includeObserved=true to reach the provider")
	}
}

func TestFleetImpactEndpoint_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"invalid ref → 422", &oci.InvalidRefError{Ref: "bad", Err: fmt.Errorf("parse")}, http.StatusUnprocessableEntity},
		{"no matching version → 422", &oci.NoMatchingVersionError{Ref: "r", Constraint: "^9", Err: fmt.Errorf("none")}, http.StatusUnprocessableEntity},
		{"invalid bundle → 422", &oci.InvalidBundleError{Ref: "r", Err: fmt.Errorf("bad")}, http.StatusUnprocessableEntity},
		{"artifact not found → 404", &oci.ArtifactNotFoundError{Ref: "r"}, http.StatusNotFound},
		{"other → 503", fmt.Errorf("build fleet snapshot: down"), http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := startFleetTestServer(t, nil, func(context.Context, string, string, bool) (*impact.Result, error) {
				return nil, tc.err
			})
			defer cancel()
			expectStatus(t, base+"/api/fleet/impact?old=a&new=b", tc.want)
		})
	}
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

func TestSourcesFromFleet(t *testing.T) {
	query := func(ss ...fleet.SourceState) *fleet.Query {
		return fleet.NewQuery(&fleet.FleetSnapshot{Sources: ss})
	}
	cases := []struct {
		name string
		q    *fleet.Query
		want []SourceInfo
	}{
		{"nil query yields no sources", nil, []SourceInfo{}},
		{"snapshot with no sources yields no sources", query(), []SourceInfo{}},
		{
			"available kubernetes is remapped to the k8s the frontend knows, and says what it contributed",
			query(fleet.SourceState{ID: "cluster", Kind: "kubernetes", Status: fleet.SourceAvailable, RevisionCount: 3, TargetCount: 7}),
			[]SourceInfo{{Type: "k8s", Enabled: true, Reason: "3 revisions, 7 targets"}},
		},
		{
			"partial stays enabled and names the status plus its cause",
			query(fleet.SourceState{
				ID: "ghcr", Kind: "oci", Status: fleet.SourcePartial, RevisionCount: 2,
				Error: &fleet.SourceError{Code: "PARTIAL_LIST", Message: "some repositories could not be listed"},
			}),
			[]SourceInfo{{Type: "oci", Enabled: true, Reason: "partial: some repositories could not be listed"}},
		},
		{
			"stale stays enabled and falls back to its counts when there is no error",
			query(fleet.SourceState{ID: "disk", Kind: "cache", Status: fleet.SourceStale, RevisionCount: 4, TargetCount: 1}),
			[]SourceInfo{{Type: "cache", Enabled: true, Reason: "stale, 4 revisions, 1 targets"}},
		},
		{
			"unavailable is disabled and carries the sanitized message",
			query(fleet.SourceState{
				ID: "ghcr", Kind: "oci", Status: fleet.SourceUnavailable,
				Error: &fleet.SourceError{Code: "AUTH_FAILED", Message: "authentication with the source failed"},
			}),
			[]SourceInfo{{Type: "oci", Enabled: false, Reason: "authentication with the source failed"}},
		},
		{
			"unavailable with no error still gets a reason (the nav renders it unconditionally)",
			query(fleet.SourceState{ID: "ghcr", Kind: "oci", Status: fleet.SourceUnavailable}),
			[]SourceInfo{{Type: "oci", Enabled: false, Reason: "source unavailable"}},
		},
		{
			// The kinds a Source can report are its Kind(): kubernetes, oci, cache,
			// local, evidence-http, observation and target-state. The last three have
			// no entry in the frontend's initial/colour/description tables, so they are
			// exactly the pass-through cases worth pinning.
			"the kinds outside the remap pass through unchanged",
			query(
				fleet.SourceState{ID: "evidence", Kind: "evidence-http", Status: fleet.SourceAvailable, RevisionCount: 1},
				fleet.SourceState{ID: "otel", Kind: "observation", Status: fleet.SourceStale},
				fleet.SourceState{ID: "argo", Kind: "target-state", Status: fleet.SourceAvailable, TargetCount: 5},
			),
			[]SourceInfo{
				{Type: "evidence-http", Enabled: true, Reason: "1 revisions, 0 targets"},
				{Type: "observation", Enabled: true, Reason: "stale, 0 revisions, 0 targets"},
				{Type: "target-state", Enabled: true, Reason: "0 revisions, 5 targets"},
			},
		},
		{
			"every kind is projected, in first-appearance order",
			query(
				fleet.SourceState{ID: "local", Kind: "local", Status: fleet.SourceAvailable, RevisionCount: 2, TargetCount: 1},
				fleet.SourceState{ID: "cluster", Kind: "kubernetes", Status: fleet.SourceUnavailable},
			),
			[]SourceInfo{
				{Type: "local", Enabled: true, Reason: "2 revisions, 1 targets"},
				{Type: "k8s", Enabled: false, Reason: "source unavailable"},
			},
		},
		{
			// Two sources of one kind collapse into one pill, and the healthy one must
			// not hide the broken one: the reason counts the group and its degraded
			// members instead of reporting whichever came first.
			"several sources of one kind collapse into one honest pill",
			query(
				fleet.SourceState{ID: "ghcr", Kind: "oci", Status: fleet.SourceAvailable, RevisionCount: 6, TargetCount: 2},
				fleet.SourceState{ID: "local", Kind: "local", Status: fleet.SourceAvailable, RevisionCount: 1},
				fleet.SourceState{
					ID: "quay", Kind: "oci", Status: fleet.SourceUnavailable,
					Error: &fleet.SourceError{Code: "UNREACHABLE", Message: "the registry could not be reached"},
				},
			),
			[]SourceInfo{
				{Type: "oci", Enabled: true, Reason: "2 sources, 1 degraded, 6 revisions, 2 targets"},
				{Type: "local", Enabled: true, Reason: "1 revisions, 0 targets"},
			},
		},
		{
			// Grouping on the raw kind would emit two pills both typed "k8s", which is
			// the duplicate the aggregation exists to prevent -- so the remap has to
			// happen before the group key, not after it.
			"kubernetes and k8s are one kind, not two pills with one name",
			query(
				fleet.SourceState{ID: "cluster", Kind: "kubernetes", Status: fleet.SourceAvailable, RevisionCount: 3},
				fleet.SourceState{ID: "other", Kind: "k8s", Status: fleet.SourceUnavailable},
			),
			[]SourceInfo{{Type: "k8s", Enabled: true, Reason: "2 sources, 1 degraded, 3 revisions, 0 targets"}},
		},
		{
			"a fully healthy group reports its size without a degraded count",
			query(
				fleet.SourceState{ID: "ghcr", Kind: "oci", Status: fleet.SourceAvailable, RevisionCount: 6, TargetCount: 2},
				fleet.SourceState{ID: "quay", Kind: "oci", Status: fleet.SourceAvailable, RevisionCount: 3, TargetCount: 4},
			),
			[]SourceInfo{{Type: "oci", Enabled: true, Reason: "2 sources, 9 revisions, 6 targets"}},
		},
		{
			"a group whose every member is down is disabled",
			query(
				fleet.SourceState{ID: "ghcr", Kind: "oci", Status: fleet.SourceUnavailable},
				fleet.SourceState{ID: "quay", Kind: "oci", Status: fleet.SourceUnavailable},
			),
			[]SourceInfo{{Type: "oci", Enabled: false, Reason: "2 sources, 2 degraded, 0 revisions, 0 targets"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sourcesFromFleet(tc.q)
			if got == nil {
				t.Fatal("sourcesFromFleet returned nil, want a non-nil slice")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sourcesFromFleet() = %+v, want %+v", got, tc.want)
			}
			// The nav renders "{type}: {reason}" unconditionally, so an empty reason
			// ships a dangling colon. It must be impossible for every input.
			for _, info := range got {
				if info.Reason == "" {
					t.Errorf("source %q has an empty reason", info.Type)
				}
			}
		})
	}
}
