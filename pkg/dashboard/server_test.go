package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"testing/fstest"
	"time"
)

type mockSource struct {
	services []Service
	details  map[string]*ServiceDetails
	versions map[string][]Version
}

func (m *mockSource) ListServices(_ context.Context) ([]Service, error) {
	return m.services, nil
}

func (m *mockSource) GetService(_ context.Context, name string) (*ServiceDetails, error) {
	if d, ok := m.details[name]; ok {
		return d, nil
	}
	return nil, context.Canceled
}

func (m *mockSource) GetVersions(_ context.Context, name string) ([]Version, error) {
	if m.versions != nil {
		if v, ok := m.versions[name]; ok {
			return v, nil
		}
	}
	return []Version{{Version: "1.0.0"}}, nil
}

func (m *mockSource) GetDiff(_ context.Context, a, b Ref) (*DiffResult, error) {
	if a.Name == "" || b.Name == "" {
		return nil, fmt.Errorf("missing name")
	}
	return &DiffResult{From: a, To: b, Classification: "NON_BREAKING"}, nil
}

// versions field added to mockSource
func newMockWithDetails(details map[string]*ServiceDetails) *mockSource {
	var services []Service
	for name, d := range details {
		svc := Service{Name: name, Version: d.Version, Phase: d.Phase, Source: d.Source}
		services = append(services, svc)
	}
	return &mockSource{services: services, details: details}
}

func TestServerListServices(t *testing.T) {
	source := &mockSource{
		services: []Service{
			{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond) // let server start

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var services []Service
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Name != "svc-a" {
		t.Errorf("expected 'svc-a', got %q", services[0].Name)
	}
}

func TestServerGetService(t *testing.T) {
	source := &mockSource{
		details: map[string]*ServiceDetails{
			"my-svc": {
				Service: Service{Name: "my-svc", Version: "2.0.0", Phase: PhaseHealthy, Source: "local"},
			},
		},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/services/my-svc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var details ServiceDetails
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		t.Fatal(err)
	}
	if details.Name != "my-svc" {
		t.Errorf("expected 'my-svc', got %q", details.Name)
	}
}

func startTestServer(t *testing.T, source DataSource) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String()
}

func TestServerGetVersions(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0"}}},
		versions: map[string][]Version{
			"svc": {{Version: "1.0.0"}, {Version: "2.0.0"}},
		},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc/versions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var versions []Version
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
}

func TestServerGetDiff_OK(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0"}}},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/diff?from_name=svc&from_version=1.0.0&to_name=svc&to_version=2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result DiffResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING, got %q", result.Classification)
	}
}

func TestServerGetDiff_MissingParams(t *testing.T) {
	source := &mockSource{}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/diff?from_name=svc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestServerGetGraph_OK(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service:      Service{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
			Dependencies: []DependencyInfo{{Ref: "svc-b", Required: true}},
		},
		"svc-b": {
			Service: Service{Name: "svc-b", Version: "2.0.0", Phase: PhaseHealthy, Source: "local"},
		},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc-a/graph")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var graph DependencyGraph
	if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
		t.Fatal(err)
	}
	if graph.Root == nil {
		t.Fatal("expected non-nil root")
	}
	if graph.Root.Name != "svc-a" {
		t.Errorf("expected root name 'svc-a', got %q", graph.Root.Name)
	}
}

func TestServerGetGraph_NotFound(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a", Version: "1.0.0", Source: "local"}},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/nonexistent/graph")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServerGetGlobalGraph(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a", Version: "1.0.0", Source: "local"}},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/graph")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var graph GlobalGraph
	if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) == 0 {
		t.Error("expected at least one node")
	}
}

func TestServerGetDependents(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service:      Service{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
			Dependencies: []DependencyInfo{{Ref: "svc-b", Required: true, Compatibility: "^2.0.0"}},
		},
		"svc-b": {
			Service: Service{Name: "svc-b", Version: "2.0.0", Phase: PhaseHealthy, Source: "local"},
		},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc-b/dependents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var dependents []DependentInfo
	if err := json.NewDecoder(resp.Body).Decode(&dependents); err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 1 {
		t.Fatalf("expected 1 dependent, got %d", len(dependents))
	}
	if dependents[0].Name != "svc-a" {
		t.Errorf("expected dependent 'svc-a', got %q", dependents[0].Name)
	}
	if !dependents[0].Required {
		t.Error("expected required=true")
	}
}

func TestServerGetDependents_NoDependents(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a", Version: "1.0.0", Source: "local"}},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc-a/dependents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServerGetCrossRefs(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service:       Service{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
			Configuration: &ConfigurationInfo{Ref: "config-svc"},
			Policy:        &PolicyInfo{Ref: "policy-svc"},
		},
		"config-svc": {
			Service: Service{Name: "config-svc", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
		},
		"other": {
			Service:       Service{Name: "other", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
			Configuration: &ConfigurationInfo{Ref: "svc-a"},
		},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc-a/refs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var refs CrossReferences
	if err := json.NewDecoder(resp.Body).Decode(&refs); err != nil {
		t.Fatal(err)
	}
	if len(refs.References) != 2 {
		t.Errorf("expected 2 outgoing references, got %d", len(refs.References))
	}
	if len(refs.ReferencedBy) != 1 {
		t.Errorf("expected 1 incoming reference, got %d", len(refs.ReferencedBy))
	}
	// Incoming reference must include the Ref field.
	if len(refs.ReferencedBy) == 1 && refs.ReferencedBy[0].Ref != "svc-a" {
		t.Errorf("expected incoming ref 'svc-a', got %q", refs.ReferencedBy[0].Ref)
	}
}

func TestServerGetCrossRefs_NotInIndex(t *testing.T) {
	source := &mockSource{
		services: []Service{},
		details:  map[string]*ServiceDetails{},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/nonexistent/refs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (empty refs), got %d", resp.StatusCode)
	}
}

func TestServerGetServiceSources_NonAggregated(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a", Version: "1.0.0", Source: "local"}},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc-a/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var agg AggregatedService
	if err := json.NewDecoder(resp.Body).Decode(&agg); err != nil {
		t.Fatal(err)
	}
	if agg.Name != "svc-a" {
		t.Errorf("expected name 'svc-a', got %q", agg.Name)
	}
	if len(agg.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(agg.Sources))
	}
}

func TestServerGetServiceSources_NotFound(t *testing.T) {
	source := &mockSource{
		details: map[string]*ServiceDetails{},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/missing/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServerGetSources(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	source := &mockSource{}
	srv := NewServer(source, ui)
	srv.sourceInfo = []SourceInfo{
		{Type: "local", Enabled: true, Reason: "found"},
		{Type: "k8s", Enabled: false, Reason: "no kubectl"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var sources []SourceInfo
	if err := json.NewDecoder(resp.Body).Decode(&sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
}

func TestCORSMiddleware_Options(t *testing.T) {
	source := &mockSource{}
	base := startTestServer(t, source)

	req, err := http.NewRequest(http.MethodOptions, base+"/api/services", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS origin '*', got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Errorf("expected CORS methods 'GET, OPTIONS', got %q", got)
	}
}

func TestServerListServices_Enriched(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service:       Service{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
			Dependencies:  []DependencyInfo{{Ref: "svc-b", Required: true}},
			ChecksSummary: &ChecksSummary{Total: 5, Passed: 3, Failed: 2},
			Insights:      []Insight{{Severity: "warning", Title: "something wrong"}},
		},
		"svc-b": {
			Service: Service{Name: "svc-b", Version: "2.0.0", Phase: PhaseHealthy, Source: "local"},
		},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var entries []ServiceListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}

	svcA := findEntry(t, entries, "svc-a")
	if svcA.DependencyCount != 1 {
		t.Errorf("expected dependencyCount=1, got %d", svcA.DependencyCount)
	}
	if svcA.ChecksTotal != 5 {
		t.Errorf("expected checksTotal=5, got %d", svcA.ChecksTotal)
	}
	if svcA.ChecksPassed != 3 {
		t.Errorf("expected checksPassed=3, got %d", svcA.ChecksPassed)
	}
	if svcA.ChecksFailed != 2 {
		t.Errorf("expected checksFailed=2, got %d", svcA.ChecksFailed)
	}
	if svcA.TopInsight != "something wrong" {
		t.Errorf("expected topInsight, got %q", svcA.TopInsight)
	}
	// svc-b is a dependency of svc-a with required=true, so svc-b has blast radius of 1
	svcB := findEntry(t, entries, "svc-b")
	if svcB.BlastRadius != 1 {
		t.Errorf("expected blastRadius=1 for svc-b, got %d", svcB.BlastRadius)
	}
}

func TestServerGetService_NotFound(t *testing.T) {
	source := &mockSource{
		details: map[string]*ServiceDetails{},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServerDebugEndpoints(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a", Version: "1.0.0", Source: "local"}},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}

	// Create server with diagnostics enabled
	agg := NewAggregatedSource(map[string]DataSource{"local": source})
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true, Reason: "found"}}
	diag := &SourceDiagnostics{
		Local: LocalDiagnostics{Dir: ".", PactoYamlFound: true},
	}
	srv := NewAggregatedServer(agg, ui, sourceInfo, diag)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	base := "http://" + ln.Addr().String()

	// Test /api/debug/sources
	resp, err := http.Get(base + "/api/debug/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("debug/sources: expected 200, got %d", resp.StatusCode)
	}

	// Test /api/debug/services
	resp2, err := http.Get(base + "/api/debug/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close() //nolint:errcheck
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("debug/services: expected 200, got %d", resp2.StatusCode)
	}
}

func TestServerDebugEndpoints_NotRegisteredWithoutDiagnostics(t *testing.T) {
	source := &mockSource{services: []Service{}}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/debug/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	// Should return 404 since diagnostics is nil
	if resp.StatusCode == http.StatusOK {
		t.Error("expected debug endpoints to not be registered without diagnostics")
	}
}

func TestServe_CancelledContext(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := srv.Serve(ctx, 0)
	// Port 0 lets the OS pick a free port, so Listen succeeds.
	// The cancelled context causes ServeOnListener to return quickly.
	// Either way, Serve should return without hanging.
	_ = err
}

func TestServe_ListenError(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)

	// Bind a port first, then try to Serve on the same port to trigger a listen error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck

	port := ln.Addr().(*net.TCPAddr).Port
	err = srv.Serve(context.Background(), port)
	if err == nil {
		t.Error("expected listen error for already-bound port")
	}
}

func TestEmbeddedUI(t *testing.T) {
	fsys := EmbeddedUI()
	if fsys == nil {
		t.Fatal("expected non-nil embedded FS")
	}
	// The embedded ui directory should contain at least index.html.
	f, err := fsys.Open("ui/index.html")
	if err != nil {
		t.Fatalf("expected ui/index.html to exist: %v", err)
	}
	_ = f.Close()
}

func TestCORSMiddleware_GetRequest(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS origin '*' on GET, got %q", got)
	}
}

// errorSource is a DataSource that returns errors for specific operations.
type errorSource struct {
	services   []Service
	details    map[string]*ServiceDetails
	listErr    error
	versionErr map[string]error
	diffErr    bool
}

func (e *errorSource) ListServices(_ context.Context) ([]Service, error) {
	if e.listErr != nil {
		return nil, e.listErr
	}
	return e.services, nil
}

func (e *errorSource) GetService(_ context.Context, name string) (*ServiceDetails, error) {
	if d, ok := e.details[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("not found: %s", name)
}

func (e *errorSource) GetVersions(_ context.Context, name string) ([]Version, error) {
	if e.versionErr != nil {
		if err, ok := e.versionErr[name]; ok {
			return nil, err
		}
	}
	return []Version{{Version: "1.0.0"}}, nil
}

func (e *errorSource) GetDiff(_ context.Context, _, _ Ref) (*DiffResult, error) {
	if e.diffErr {
		return nil, fmt.Errorf("diff failed")
	}
	return &DiffResult{Classification: "NON_BREAKING"}, nil
}

func TestServerGetVersions_Error(t *testing.T) {
	source := &errorSource{
		services:   []Service{{Name: "svc", Version: "1.0.0"}},
		details:    map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc"}}},
		versionErr: map[string]error{"svc": fmt.Errorf("versions unavailable")},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc/versions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestServerGetDiff_Error(t *testing.T) {
	source := &errorSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc"}}},
		diffErr:  true,
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/diff?from_name=svc&from_version=1.0.0&to_name=svc&to_version=2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestServerGetServiceSources_Aggregated(t *testing.T) {
	localSource := &stubSource{
		name:     "local",
		services: []Service{{Name: "svc-a", Version: "1.0.0", Source: "local"}},
		details: map[string]*ServiceDetails{
			"svc-a": {Service: Service{Name: "svc-a", Version: "1.0.0", Source: "local"}},
		},
	}
	agg := NewAggregatedSource(map[string]DataSource{"local": localSource})
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true}}
	diag := &SourceDiagnostics{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewAggregatedServer(agg, ui, sourceInfo, diag)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	base := "http://" + ln.Addr().String()

	resp, err := http.Get(base + "/api/services/svc-a/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var aggs AggregatedService
	if err := json.NewDecoder(resp.Body).Decode(&aggs); err != nil {
		t.Fatal(err)
	}
	if aggs.Name != "svc-a" {
		t.Errorf("expected name 'svc-a', got %q", aggs.Name)
	}
}

func TestServerGetServiceSources_AggregatedNotFound(t *testing.T) {
	localSource := &stubSource{
		name:     "local",
		services: []Service{},
		details:  map[string]*ServiceDetails{},
	}
	agg := NewAggregatedSource(map[string]DataSource{"local": localSource})
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true}}
	diag := &SourceDiagnostics{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewAggregatedServer(agg, ui, sourceInfo, diag)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	base := "http://" + ln.Addr().String()

	resp, err := http.Get(base + "/api/services/nonexistent/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestGetCachedIndex_ListServicesError_NoPriorCache(t *testing.T) {
	source := &errorSource{
		listErr: fmt.Errorf("list failed"),
	}
	srv := NewServer(source, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}})

	cached := srv.getCachedIndex(context.Background())
	if cached == nil {
		t.Fatal("expected non-nil cache")
	}
	if len(cached.index) != 0 {
		t.Errorf("expected empty index, got %d entries", len(cached.index))
	}
}

func TestGetCachedIndex_ListServicesError_WithStaleCache(t *testing.T) {
	// First build a cache with a working source.
	workingSource := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0"}}},
	}
	srv := NewServer(workingSource, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}})

	// Build initial cache.
	cached := srv.getCachedIndex(context.Background())
	if len(cached.services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(cached.services))
	}

	// Force cache to be stale and switch to an erroring source.
	srv.indexCache.builtAt = time.Now().Add(-10 * time.Second)
	srv.source = &errorSource{listErr: fmt.Errorf("list failed")}

	// Should return stale cache.
	cached = srv.getCachedIndex(context.Background())
	if len(cached.services) != 1 {
		t.Fatalf("expected stale cache with 1 service, got %d", len(cached.services))
	}
}

func TestServerGetCrossRefs_PolicyRefLookup(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service: Service{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
			Policy:  &PolicyInfo{Ref: "policy-svc"},
		},
		"policy-svc": {
			Service: Service{Name: "policy-svc", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
		},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc-a/refs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var refs CrossReferences
	if err := json.NewDecoder(resp.Body).Decode(&refs); err != nil {
		t.Fatal(err)
	}
	if len(refs.References) != 1 {
		t.Fatalf("expected 1 reference, got %d", len(refs.References))
	}
	if refs.References[0].RefType != "policy" {
		t.Errorf("expected refType 'policy', got %q", refs.References[0].RefType)
	}
	if refs.References[0].Phase != string(PhaseHealthy) {
		t.Errorf("expected phase 'Healthy', got %q", refs.References[0].Phase)
	}
}

func TestServerDebugSources_SourceError(t *testing.T) {
	// Create a server where source.ListServices returns an error directly.
	errSrc := &errorSource{listErr: fmt.Errorf("list failed")}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := &Server{
		source:      errSrc,
		ui:          ui,
		diagnostics: &SourceDiagnostics{},
		sourceInfo:  []SourceInfo{{Type: "local", Enabled: true}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	base := "http://" + ln.Addr().String()

	resp, err := http.Get(base + "/api/debug/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var debug map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&debug); err != nil {
		t.Fatal(err)
	}
	live, ok := debug["live"].(map[string]any)
	if !ok {
		t.Fatal("expected live field")
	}
	if live["error"] == nil || live["error"] == "" {
		t.Error("expected error in live debug info")
	}
}

func TestServerDebugSources_NilSource(t *testing.T) {
	// Create a server with source=nil and diagnostics set.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := &Server{
		source:      nil,
		ui:          ui,
		diagnostics: &SourceDiagnostics{},
		sourceInfo:  []SourceInfo{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	base := "http://" + ln.Addr().String()
	resp, err := http.Get(base + "/api/debug/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var debug map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&debug); err != nil {
		t.Fatal(err)
	}
	// When source is nil, live should not be present.
	if debug["live"] != nil {
		t.Error("expected nil live when source is nil")
	}
}

func TestServeOnListener_ServerError(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Close the listener so that srv.Serve fails immediately with an error
	// through the errCh path.
	_ = ln.Close()

	err = srv.ServeOnListener(context.Background(), ln)
	if err == nil {
		t.Error("expected error from closed listener")
	}
}

func TestGetCachedIndex_FreshCacheReturn(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0"}}},
	}
	srv := NewServer(source, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}})

	// First call builds cache.
	cached1 := srv.getCachedIndex(context.Background())
	if len(cached1.services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(cached1.services))
	}

	// Second call within TTL should return same cache object.
	cached2 := srv.getCachedIndex(context.Background())
	if cached1 != cached2 {
		t.Error("expected same cache object for fresh cache")
	}
}

func TestServerGetCrossRefs_PolicyReferencedBy(t *testing.T) {
	// Service "other" has a Policy.Ref pointing to "svc-a", so svc-a should have
	// "other" in its ReferencedBy list with refType "policy".
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service: Service{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
		},
		"other": {
			Service: Service{Name: "other", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
			Policy:  &PolicyInfo{Ref: "svc-a"},
		},
	})
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc-a/refs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var refs CrossReferences
	if err := json.NewDecoder(resp.Body).Decode(&refs); err != nil {
		t.Fatal(err)
	}
	if len(refs.ReferencedBy) != 1 {
		t.Fatalf("expected 1 referenced-by entry, got %d", len(refs.ReferencedBy))
	}
	if refs.ReferencedBy[0].RefType != "policy" {
		t.Errorf("expected refType 'policy', got %q", refs.ReferencedBy[0].RefType)
	}
	if refs.ReferencedBy[0].Name != "other" {
		t.Errorf("expected name 'other', got %q", refs.ReferencedBy[0].Name)
	}
	if refs.ReferencedBy[0].Ref != "svc-a" {
		t.Errorf("expected ref 'svc-a', got %q", refs.ReferencedBy[0].Ref)
	}
}

func TestServerDebugServices_ListServicesError(t *testing.T) {
	// Direct error source (not aggregated) so ListServices actually fails.
	errSrc := &errorSource{listErr: fmt.Errorf("list failed")}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := &Server{
		source:      errSrc,
		ui:          ui,
		diagnostics: &SourceDiagnostics{},
		sourceInfo:  []SourceInfo{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	base := "http://" + ln.Addr().String()
	resp, err := http.Get(base + "/api/debug/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestServerDebugServices_PerSourceError(t *testing.T) {
	errSrc := &errorSource{listErr: fmt.Errorf("source broken")}
	workingSrc := &stubSource{
		name:     "local",
		services: []Service{{Name: "svc", Version: "1.0.0", Source: "local"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0", Source: "local"}}},
	}

	agg := NewAggregatedSource(map[string]DataSource{
		"k8s":   errSrc,
		"local": workingSrc,
	})
	sourceInfo := []SourceInfo{{Type: "k8s", Enabled: true}, {Type: "local", Enabled: true}}
	diag := &SourceDiagnostics{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewAggregatedServer(agg, ui, sourceInfo, diag)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	base := "http://" + ln.Addr().String()
	resp, err := http.Get(base + "/api/debug/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func findEntry(t *testing.T, entries []ServiceListEntry, name string) *ServiceListEntry {
	t.Helper()
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i]
		}
	}
	t.Fatalf("%s not found in entries", name)
	return nil
}
