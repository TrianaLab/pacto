package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/oci"
)

type mockSource struct {
	services    []Service
	details     map[string]*ServiceDetails
	versions    map[string][]Version
	versionsErr map[string]error
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
	if m.versionsErr != nil {
		if err, ok := m.versionsErr[name]; ok {
			return nil, err
		}
	}
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
		svc := Service{Name: name, Version: d.Version, ContractStatus: d.ContractStatus, Source: d.Source}
		services = append(services, svc)
	}
	return &mockSource{services: services, details: details}
}

func TestServerListServices(t *testing.T) {
	source := &mockSource{
		services: []Service{
			{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
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
				Service: Service{Name: "my-svc", Version: "2.0.0", ContractStatus: StatusCompliant, Source: "local"},
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

	// Huma returns 422 (Unprocessable Entity) for missing required query params.
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
}

func TestServerGetGraph_OK(t *testing.T) {
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service:      Service{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
			Dependencies: []DependencyInfo{{Ref: "svc-b", Required: true}},
		},
		"svc-b": {
			Service: Service{Name: "svc-b", Version: "2.0.0", ContractStatus: StatusCompliant, Source: "local"},
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
			Service:      Service{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
			Dependencies: []DependencyInfo{{Ref: "svc-b", Required: true, Compatibility: "^2.0.0"}},
		},
		"svc-b": {
			Service: Service{Name: "svc-b", Version: "2.0.0", ContractStatus: StatusCompliant, Source: "local"},
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
			Service:       Service{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
			Configuration: &ConfigurationInfo{Ref: "config-svc"},
			Policy:        &PolicyInfo{Ref: "policy-svc"},
		},
		"config-svc": {
			Service: Service{Name: "config-svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
		},
		"other": {
			Service:       Service{Name: "other", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
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
		{Type: "k8s", Enabled: false, Reason: "no kubeconfig"},
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

	var body struct {
		Sources     []SourceInfo `json:"sources"`
		Discovering bool         `json:"discovering"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(body.Sources))
	}
	if body.Discovering {
		t.Fatal("expected discovering=false when no OCI source is set")
	}
}

func TestServerGetSources_WithOCIDiscovering(t *testing.T) {
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
		{Type: "oci", Enabled: true, Reason: "configured"},
	}
	// Create an OCISource that is "discovering" (started but not done).
	ociSrc := NewOCISource(nil, nil)
	ociSrc.started = true // simulate started state
	srv.SetOCISource(ociSrc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var body struct {
		Sources     []SourceInfo `json:"sources"`
		Discovering bool         `json:"discovering"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Discovering {
		t.Fatal("expected discovering=true when OCI source is actively discovering")
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
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
		t.Errorf("expected CORS methods 'GET, POST, OPTIONS', got %q", got)
	}
}

func fetchEnrichedEntries(t *testing.T) []ServiceListEntry {
	t.Helper()
	score80 := 80
	source := newMockWithDetails(map[string]*ServiceDetails{
		"svc-a": {
			Service:       Service{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
			Dependencies:  []DependencyInfo{{Ref: "svc-b", Required: true}},
			ChecksSummary: &ChecksSummary{Total: 5, Passed: 3, Failed: 2},
			Insights:      []Insight{{Severity: "warning", Title: "something wrong"}},
			Compliance: &ComplianceInfo{
				Status:  ComplianceWarning,
				Score:   &score80,
				Summary: &ComplianceCounts{Total: 5, Passed: 4, Failed: 1, Errors: 0, Warnings: 1},
			},
		},
		"svc-b": {
			Service: Service{Name: "svc-b", Version: "2.0.0", ContractStatus: StatusCompliant, Source: "local"},
			Conditions: []Condition{
				{Type: "ContractValid", Status: "True"},
				{Type: "ServiceExists", Status: "False"},
			},
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
	return entries
}

func TestServerListServices_Enriched(t *testing.T) {
	entries := fetchEnrichedEntries(t)

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

func TestServerListServices_Compliance(t *testing.T) {
	entries := fetchEnrichedEntries(t)

	// svc-a has pre-computed compliance
	svcA := findEntry(t, entries, "svc-a")
	if svcA.ComplianceStatus != ComplianceWarning {
		t.Errorf("expected compliance WARNING, got %q", svcA.ComplianceStatus)
	}
	if svcA.ComplianceScore == nil || *svcA.ComplianceScore != 80 {
		t.Errorf("expected compliance score 80, got %v", svcA.ComplianceScore)
	}
	if svcA.ComplianceWarns != 1 {
		t.Errorf("expected 1 compliance warning, got %d", svcA.ComplianceWarns)
	}

	// svc-b has no pre-computed compliance, computed from conditions
	svcB := findEntry(t, entries, "svc-b")
	if svcB.ComplianceStatus != ComplianceError {
		t.Errorf("expected compliance ERROR for svc-b, got %q", svcB.ComplianceStatus)
	}
	if svcB.ComplianceScore == nil || *svcB.ComplianceScore != 50 {
		t.Errorf("expected compliance score 50 for svc-b, got %v", svcB.ComplianceScore)
	}
	if svcB.ComplianceErrors != 1 {
		t.Errorf("expected 1 compliance error for svc-b, got %d", svcB.ComplianceErrors)
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
	agg := BuildResolvedSource(map[string]DataSource{"local": source})
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true, Reason: "found"}}
	diag := &SourceDiagnostics{
		Local: LocalDiagnostics{Dir: ".", PactoYamlFound: true},
	}
	srv := NewResolvedServer(agg, ui, sourceInfo, diag)

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

func TestSetListenAddr(t *testing.T) {
	srv := &Server{}

	srv.SetListenAddr("192.168.1.1", 8080)
	if srv.listenAddr != "http://192.168.1.1:8080" {
		t.Errorf("expected http://192.168.1.1:8080, got %s", srv.listenAddr)
	}

	srv.SetListenAddr("0.0.0.0", 3000)
	if srv.listenAddr != "http://localhost:3000" {
		t.Errorf("expected http://localhost:3000 for 0.0.0.0, got %s", srv.listenAddr)
	}

	srv.SetListenAddr("", 3000)
	if srv.listenAddr != "http://localhost:3000" {
		t.Errorf("expected http://localhost:3000 for empty host, got %s", srv.listenAddr)
	}
}

func TestSetListenAddr_OpenAPIServer(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)
	srv.SetListenAddr("10.0.0.1", 9090)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = srv.ServeOnListener(ctx, ln)
	}()
	defer cancel()

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/openapi.json", ln.Addr()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var spec map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		t.Fatal(err)
	}
	servers, ok := spec["servers"].([]any)
	if !ok || len(servers) == 0 {
		t.Fatal("expected servers in OpenAPI spec")
	}
	serverObj := servers[0].(map[string]any)
	if serverObj["url"] != "http://10.0.0.1:9090" {
		t.Errorf("expected server URL http://10.0.0.1:9090, got %v", serverObj["url"])
	}
}

func TestServe_CustomHost(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = srv.Serve(ctx, 0, "0.0.0.0")
}

func TestServe_EmptyHost(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = srv.Serve(ctx, 0, "")
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
	// EmbeddedUI returns the ui/ subdir, so index.html is at root.
	f, err := fsys.Open("index.html")
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

	// No version history is a valid state; server returns 200 with empty list.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var versions []Version
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected empty versions, got %d", len(versions))
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
	agg := BuildResolvedSource(map[string]DataSource{"local": localSource})
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true}}
	diag := &SourceDiagnostics{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewResolvedServer(agg, ui, sourceInfo, diag)

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
	agg := BuildResolvedSource(map[string]DataSource{"local": localSource})
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true}}
	diag := &SourceDiagnostics{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewResolvedServer(agg, ui, sourceInfo, diag)

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
	srv.indexCache.builtAt = time.Now().Add(-20 * time.Second)
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
			Service: Service{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
			Policy:  &PolicyInfo{Ref: "policy-svc"},
		},
		"policy-svc": {
			Service: Service{Name: "policy-svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
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
	if refs.References[0].ContractStatus != string(StatusCompliant) {
		t.Errorf("expected status 'Compliant', got %q", refs.References[0].ContractStatus)
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
			Service: Service{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
		},
		"other": {
			Service: Service{Name: "other", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
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

	agg := BuildResolvedSource(map[string]DataSource{
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
	srv := NewResolvedServer(agg, ui, sourceInfo, diag)

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

func TestServerHealth(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)
	srv.SetVersion("1.2.3")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	base := "http://" + ln.Addr().String()

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", body["status"])
	}
	if body["version"] != "1.2.3" {
		t.Errorf("expected version '1.2.3', got %v", body["version"])
	}
}

func TestServerMetrics(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)
	srv.sourceInfo = []SourceInfo{{Type: "local", Enabled: true}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + ln.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["sourceCount"] != float64(1) {
		t.Errorf("expected sourceCount=1, got %v", body["sourceCount"])
	}
	if body["serviceCount"] != float64(1) {
		t.Errorf("expected serviceCount=1, got %v", body["serviceCount"])
	}
}

func TestServerMetrics_NilSource(t *testing.T) {
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

	resp, err := http.Get("http://" + ln.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestExportOpenAPI(t *testing.T) {
	data, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty OpenAPI spec")
	}
	if !json.Valid(data) {
		t.Fatal("expected valid JSON")
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Errorf("expected OpenAPI 3.1.0, got %v", spec["openapi"])
	}
	info, _ := spec["info"].(map[string]any)
	if info["title"] != "Pacto Dashboard API" {
		t.Errorf("expected title 'Pacto Dashboard API', got %v", info["title"])
	}
	paths, _ := spec["paths"].(map[string]any)
	if paths["/api/services"] == nil {
		t.Error("expected /api/services path in spec")
	}
}

func TestExportConfigSchema(t *testing.T) {
	data, err := ExportConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("expected valid JSON")
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["title"] != "Pacto Dashboard Configuration" {
		t.Errorf("title = %v", schema["title"])
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"PACTO_DASHBOARD_HOST", "PACTO_DASHBOARD_PORT", "PACTO_DASHBOARD_NAMESPACE", "PACTO_DASHBOARD_REPO", "PACTO_DASHBOARD_DIAGNOSTICS", "PACTO_NO_CACHE", "PACTO_NO_UPDATE_CHECK", "PACTO_REGISTRY_USERNAME", "PACTO_REGISTRY_PASSWORD", "PACTO_REGISTRY_TOKEN"} {
		if props[key] == nil {
			t.Errorf("missing property %s", key)
		}
	}
	port, _ := props["PACTO_DASHBOARD_PORT"].(map[string]any)
	if port["default"] != float64(3000) {
		t.Errorf("port default = %v", port["default"])
	}
	if port["description"] != "HTTP server port" {
		t.Errorf("port description = %v", port["description"])
	}
}

// ── Resolve endpoint tests ──────────────────────────────────────────

func startTestServerWithResolver(t *testing.T, source DataSource, store oci.BundleStore) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
	srv := NewServer(source, ui)
	if store != nil {
		srv.SetResolver(oci.NewResolver(store))
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String()
}

// resolveStore implements oci.BundleStore for resolve endpoint tests.
type resolveStore struct {
	bundle  *contract.Bundle
	pullErr error
}

func (s *resolveStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *resolveStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (s *resolveStore) Pull(_ context.Context, _ string) (*contract.Bundle, error) {
	if s.pullErr != nil {
		return nil, s.pullErr
	}
	return s.bundle, nil
}
func (s *resolveStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }

func newResolveTestBundle() *contract.Bundle {
	port := 8080
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "remote-svc", Version: "1.0.0"},
			Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
			Runtime: &contract.Runtime{
				Workload: "service",
				State:    contract.State{Type: "stateless", Persistence: contract.Persistence{Scope: "local", Durability: "ephemeral"}, DataCriticality: "low"},
			},
		},
		RawYAML: []byte("pactoVersion: \"1.0\"\nservice:\n  name: remote-svc\n  version: \"1.0.0\"\ninterfaces:\n  - name: api\n    type: http\n    port: 8080\nruntime:\n  workload: service\n  state:\n    type: stateless\n    persistence:\n      scope: local\n      durability: ephemeral\n    dataCriticality: low\n"),
	}
}

func TestServerResolveRef_Success(t *testing.T) {
	source := &mockSource{services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &resolveStore{bundle: newResolveTestBundle()}
	base := startTestServerWithResolver(t, source, store)

	resp, err := http.Post(base+"/api/resolve", "application/json", strings.NewReader(`{"ref":"ghcr.io/org/remote-svc-pacto:1.0.0"}`))
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
	if details.Name != "remote-svc" {
		t.Errorf("expected name 'remote-svc', got %q", details.Name)
	}
	if details.Source != "oci" {
		t.Errorf("expected source 'oci', got %q", details.Source)
	}
}

func TestServerResolveRef_AuthFailure(t *testing.T) {
	source := &mockSource{services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &resolveStore{pullErr: &oci.AuthenticationError{Ref: "ghcr.io/org/svc:1.0.0", Err: fmt.Errorf("401")}}
	base := startTestServerWithResolver(t, source, store)

	resp, err := http.Post(base+"/api/resolve", "application/json", strings.NewReader(`{"ref":"ghcr.io/org/svc:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestServerResolveRef_NotFound(t *testing.T) {
	source := &mockSource{services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &resolveStore{pullErr: &oci.ArtifactNotFoundError{Ref: "ghcr.io/org/svc:1.0.0", Err: fmt.Errorf("404")}}
	base := startTestServerWithResolver(t, source, store)

	resp, err := http.Post(base+"/api/resolve", "application/json", strings.NewReader(`{"ref":"ghcr.io/org/svc:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestServerResolveRef_RegistryUnreachable(t *testing.T) {
	source := &mockSource{services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &resolveStore{pullErr: &oci.RegistryUnreachableError{Ref: "ghcr.io/org/svc:1.0.0", Err: fmt.Errorf("dns error")}}
	base := startTestServerWithResolver(t, source, store)

	resp, err := http.Post(base+"/api/resolve", "application/json", strings.NewReader(`{"ref":"ghcr.io/org/svc:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
}

func TestServerResolveRef_NoResolver(t *testing.T) {
	source := &mockSource{services: []Service{}, details: map[string]*ServiceDetails{}}
	base := startTestServerWithResolver(t, source, nil)

	resp, err := http.Post(base+"/api/resolve", "application/json", strings.NewReader(`{"ref":"ghcr.io/org/svc:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Without a resolver, the endpoint is not registered.
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected endpoint to not be registered without resolver")
	}
}

func TestServerRefreshCacheSources(t *testing.T) {
	// Verify that refreshCacheSources invalidates memory cache and index cache.
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0"}}},
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	memCache := NewMemoryCache()
	memCache.Set("test-key", "test-value", time.Hour)

	root := t.TempDir()
	cacheSource := NewCacheSource(root)
	srv.SetCacheSource(cacheSource, memCache)

	// Build initial index cache.
	_ = srv.getCachedIndex(context.Background())
	if srv.indexCache == nil {
		t.Fatal("expected index cache to be built")
	}

	// Verify memory cache has the entry.
	if _, ok := memCache.Get("test-key"); !ok {
		t.Fatal("expected test-key in memory cache")
	}

	// Call refreshCacheSources.
	srv.RefreshCacheSources()

	// Index cache should be nil.
	srv.indexMu.Lock()
	indexNil := srv.indexCache == nil
	srv.indexMu.Unlock()
	if !indexNil {
		t.Error("expected index cache to be nil after refresh")
	}

	// Memory cache should be invalidated.
	if _, ok := memCache.Get("test-key"); ok {
		t.Error("expected test-key to be invalidated from memory cache")
	}
}

func TestServerSetCacheDir(t *testing.T) {
	srv := NewServer(&mockSource{}, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}})
	srv.SetCacheDir("/tmp/test-cache")
	if srv.cacheDir != "/tmp/test-cache" {
		t.Errorf("expected cacheDir to be set, got %q", srv.cacheDir)
	}
}

func TestServerRefreshCacheSources_OnTheFly(t *testing.T) {
	// Test the on-the-fly CacheSource creation path when cacheSource == nil
	// but cacheDir has data.
	cacheDir := t.TempDir()
	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/svc/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`)

	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	// Set up OCI source so the wiring branch is exercised.
	ociSource := NewOCISource(newMockBundleStore(), []string{"ghcr.io/org/svc"})
	srv.SetOCISource(ociSource)
	srv.SetCacheDir(cacheDir)

	memCache := NewMemoryCache()
	memCache.Set("test-key", "value", time.Hour)
	srv.memCache = memCache

	// No cacheSource yet — refreshCacheSources should create one on-the-fly.
	srv.RefreshCacheSources()

	if srv.cacheSource == nil {
		t.Error("expected cacheSource to be created on-the-fly")
	}

	// Verify OCI source has internal cache wired.
	ociSource.mu.RLock()
	hasCache := ociSource.cache != nil
	ociSource.mu.RUnlock()
	if !hasCache {
		t.Error("expected OCI source to have internal cache wired")
	}

	// Memory cache should be invalidated.
	if _, ok := memCache.Get("test-key"); ok {
		t.Error("expected memory cache to be invalidated")
	}
}

func TestMemoryCache_InvalidateAll(t *testing.T) {
	cache := NewMemoryCache()
	cache.Set("a", 1, time.Hour)
	cache.Set("b", 2, time.Hour)

	if _, ok := cache.Get("a"); !ok {
		t.Fatal("expected 'a' to exist")
	}

	cache.InvalidateAll()

	if _, ok := cache.Get("a"); ok {
		t.Error("expected 'a' to be gone after InvalidateAll")
	}
	if _, ok := cache.Get("b"); ok {
		t.Error("expected 'b' to be gone after InvalidateAll")
	}
}

func TestServerSetLazyEnrich(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0"}}},
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	called := false
	srv.SetLazyEnrich(func(_ context.Context) bool {
		called = true
		return true
	})

	// ensureOCIEnriched should invoke the callback.
	srv.ensureOCIEnriched(context.Background())
	if !called {
		t.Error("expected lazy enrich callback to be called")
	}
	if !srv.enrichDone {
		t.Error("expected enrichDone=true after successful enrichment")
	}

	// Second call should not invoke callback again (enrichDone=true).
	called = false
	srv.ensureOCIEnriched(context.Background())
	if called {
		t.Error("expected callback NOT called after enrichDone")
	}
}

func TestServerSetLazyEnrich_Failure(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	callCount := 0
	srv.SetLazyEnrich(func(_ context.Context) bool {
		callCount++
		return false // enrichment fails
	})

	srv.ensureOCIEnriched(context.Background())
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
	if srv.enrichDone {
		t.Error("expected enrichDone=false after failed enrichment")
	}

	// Second call within cooldown should NOT invoke callback.
	srv.ensureOCIEnriched(context.Background())
	if callCount != 1 {
		t.Errorf("expected still 1 call (cooldown), got %d", callCount)
	}
}

func TestServerSetLazyEnrich_NilCallback(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	// Should not panic when lazyEnrich is nil.
	srv.ensureOCIEnriched(context.Background())
}

func TestServerEnsureOCIEnriched_ConcurrentDoubleCheck(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	// Goroutine A holds the lock inside the callback while B waits at the lock.
	// When A finishes (enrichDone=true) and releases, B enters the lock and
	// hits the inner enrichDone check (line 143), returning without calling
	// the callback again.
	entered := make(chan struct{})
	gate := make(chan struct{})
	callCount := 0
	srv.SetLazyEnrich(func(_ context.Context) bool {
		callCount++
		close(entered) // signal that A is inside the callback (holding lock)
		<-gate         // block until released
		return true
	})

	// Goroutine A: acquires lock, enters callback, blocks.
	go srv.ensureOCIEnriched(context.Background())

	// Wait for A to be inside the callback (holding the lock).
	<-entered

	// Goroutine B: passes fast-path (enrichDone is still false), blocks at lock.
	done := make(chan struct{})
	go func() {
		srv.ensureOCIEnriched(context.Background())
		close(done)
	}()

	// Give B time to reach the lock.
	time.Sleep(50 * time.Millisecond)

	// Release A — sets enrichDone=true, releases lock. B then acquires lock
	// and sees enrichDone=true in the inner check.
	close(gate)
	<-done

	if callCount != 1 {
		t.Errorf("expected callback called exactly once, got %d", callCount)
	}
}

func TestServerEnsureOCIEnriched_Cooldown(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	called := false
	srv.SetLazyEnrich(func(_ context.Context) bool {
		called = true
		return false
	})

	srv.enrichLastTry = time.Now() // just tried

	// Should not call callback due to cooldown.
	srv.ensureOCIEnriched(context.Background())
	if called {
		t.Error("expected callback NOT called during cooldown")
	}
}

func TestServerUpdateSourceInfo(t *testing.T) {
	source := &mockSource{services: []Service{}}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	// Provide duplicated source info (e.g., OCI detected twice).
	srv.UpdateSourceInfo([]SourceInfo{
		{Type: "oci", Enabled: false, Reason: "no repos"},
		{Type: "local", Enabled: true, Reason: "found"},
		{Type: "oci", Enabled: true, Reason: "discovered from K8s"},
	})

	if len(srv.sourceInfo) != 2 {
		t.Fatalf("expected 2 deduplicated sources, got %d", len(srv.sourceInfo))
	}

	// OCI should have the last (updated) entry.
	for _, si := range srv.sourceInfo {
		if si.Type == "oci" {
			if !si.Enabled {
				t.Error("expected oci to be enabled (last occurrence wins)")
			}
			if si.Reason != "discovered from K8s" {
				t.Errorf("expected reason 'discovered from K8s', got %q", si.Reason)
			}
		}
	}
}

func TestNoCache_AllowsSameSessionEnrichment(t *testing.T) {
	// Full end-to-end test of --no-cache same-session enrichment:
	// 1. Start with --no-cache (no CacheSource at startup)
	// 2. Call GetVersions → bare versions (no hash, no createdAt)
	// 3. Simulate fetch-all-versions writing bundles to disk
	// 4. RefreshCacheSources → on-the-fly CacheSource creation
	// 5. Call GetVersions again → enriched versions (hash, createdAt, classification)

	cacheDir := t.TempDir()
	ctx := context.Background()

	// ── Step 1: Set up OCI source with mock store (--no-cache mode) ──

	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/svc", "1.0.0", "svc", "1.0.0")
	store.addBundle("ghcr.io/org/svc", "2.0.0", "svc", "2.0.0")

	ociSource := NewOCISource(store, []string{"ghcr.io/org/svc"})
	// Trigger discovery so repoMap is populated.
	_, _ = ociSource.ListServices(ctx)
	<-ociSource.done // wait for background discovery

	// Build resolved source with only OCI (no cache — --no-cache mode).
	memCache := NewMemoryCache()
	cachedOCI := NewCachedDataSource(ociSource, memCache, 5*time.Minute, "oci:")
	resolved := BuildResolvedSource(map[string]DataSource{"oci": cachedOCI})

	// Build server.
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewResolvedServer(resolved, ui, nil, nil)
	srv.SetCacheSource(nil, memCache) // nil CacheSource, non-nil memCache
	srv.SetCacheDir(cacheDir)
	srv.SetOCISource(ociSource)

	// ── Step 2: GetVersions returns bare versions ──

	versions := getVersions(t, resolved, "svc", 2)
	assertBareVersions(t, versions)

	// ── Step 3: Simulate fetch-all-versions writing bundles to disk ──

	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/svc/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/svc/2.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: svc
  version: 2.0.0
`)

	// ── Step 4: RefreshCacheSources creates CacheSource on-the-fly ──

	srv.RefreshCacheSources()

	if srv.cacheSource == nil {
		t.Fatal("expected CacheSource to be created on-the-fly after materialization")
	}

	// OCI source should have internal cache wired.
	ociSource.mu.RLock()
	hasCache := ociSource.cache != nil
	ociSource.mu.RUnlock()
	if !hasCache {
		t.Fatal("expected OCI source to have internal cache wired for enrichment")
	}

	// Memory cache should be invalidated so next GetVersions fetches fresh data.
	if _, ok := memCache.Get("oci:versions:svc"); ok {
		t.Error("expected memCache to be invalidated after RefreshCacheSources")
	}

	// ── Step 5: GetVersions returns enriched versions ──

	versions = getVersions(t, resolved, "svc", 2)
	assertEnrichedVersions(t, versions)
}

func getVersions(t *testing.T, rs *ResolvedSource, name string, expectedCount int) []Version {
	t.Helper()
	versions, err := rs.GetVersions(context.Background(), name)
	if err != nil {
		t.Fatalf("GetVersions(%s): %v", name, err)
	}
	if len(versions) != expectedCount {
		t.Fatalf("expected %d versions, got %d", expectedCount, len(versions))
	}
	return versions
}

func assertBareVersions(t *testing.T, versions []Version) {
	t.Helper()
	for _, v := range versions {
		if v.ContractHash != "" {
			t.Errorf("version %s should have no hash before enrichment, got %q", v.Version, v.ContractHash)
		}
		if v.CreatedAt != nil {
			t.Errorf("version %s should have no createdAt before enrichment", v.Version)
		}
	}
}

func assertEnrichedVersions(t *testing.T, versions []Version) {
	t.Helper()
	for _, v := range versions {
		if v.ContractHash == "" {
			t.Errorf("version %s: ContractHash should be populated after enrichment", v.Version)
		}
		if v.CreatedAt == nil {
			t.Errorf("version %s: CreatedAt should be populated after enrichment", v.Version)
		}
	}
	hasClassification := false
	for _, v := range versions {
		if v.Classification != "" {
			hasClassification = true
			break
		}
	}
	if !hasClassification {
		t.Error("expected at least one version to have Classification after enrichment")
	}
}

func TestServerRefresh_Endpoint(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", ContractStatus: StatusCompliant, Source: "local"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", ContractStatus: StatusCompliant, Source: "local"}}},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)

	// Set up a k8s redetect callback that records whether it was called.
	redetectCalled := false
	srv.SetK8sRedetect(func(_ context.Context) (DataSource, error) {
		redetectCalled = true
		return nil, fmt.Errorf("no change")
	})

	memCache := NewMemoryCache()
	srv.SetCacheSource(nil, memCache)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	// POST /api/refresh should return 200.
	resp, err := http.Post("http://"+ln.Addr().String()+"/api/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", body.Status)
	}

	// K8s redetect should have been triggered (cooldown reset by refresh).
	if !redetectCalled {
		t.Error("expected k8s redetect to be called on POST /api/refresh")
	}
}

func TestServerRefresh_K8sSourceSwap(t *testing.T) {
	k8sOld := &mockSource{
		services: []Service{{Name: "svc", ContractStatus: StatusWarning, Source: "k8s"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", ContractStatus: StatusWarning, Source: "k8s"}},
		},
	}
	local := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Source: "local"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", Version: "1.0.0", Source: "local"}},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s":   k8sOld,
		"local": local,
	})

	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewResolvedServer(resolved, ui, nil, nil)
	memCache := NewMemoryCache()
	srv.SetCacheSource(nil, memCache)

	// Redetect callback that returns a new k8s source.
	k8sNew := &mockSource{
		services: []Service{{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"}},
		},
	}
	srv.SetK8sRedetect(func(_ context.Context) (DataSource, error) {
		return k8sNew, nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	// POST /api/refresh to trigger source swap.
	resp, err := http.Post("http://"+ln.Addr().String()+"/api/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck

	// Verify the swap: GET /api/services should show Compliant (from new k8s).
	time.Sleep(50 * time.Millisecond)
	resp2, err := http.Get("http://" + ln.Addr().String() + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close() //nolint:errcheck

	var entries []ServiceListEntry
	if err := json.NewDecoder(resp2.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 service")
	}
	if entries[0].ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant from new k8s after refresh, got %q", entries[0].ContractStatus)
	}
}

func TestServerRedetect_NilSource(t *testing.T) {
	// Cover the newSource == nil return path in redetectK8sIfNeeded.
	source := &mockSource{
		services: []Service{{Name: "svc", ContractStatus: StatusCompliant, Source: "local"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", ContractStatus: StatusCompliant, Source: "local"}},
		},
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(source, ui)
	memCache := NewMemoryCache()
	srv.SetCacheSource(nil, memCache)

	// Redetect callback returns nil source (context changed but k8s unreachable).
	srv.SetK8sRedetect(func(_ context.Context) (DataSource, error) {
		return nil, nil
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	// POST /api/refresh triggers redetect which returns nil.
	resp, err := http.Post("http://"+ln.Addr().String()+"/api/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServerVersionTracking_ListServices(t *testing.T) {
	source := &mockSource{
		services: []Service{
			{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
		},
		details: map[string]*ServiceDetails{
			"svc-a": {
				Service:     Service{Name: "svc-a", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
				ResolvedRef: "ghcr.io/org/svc-a:1.0.0",
			},
		},
		versions: map[string][]Version{
			"svc-a": {{Version: "2.0.0"}, {Version: "1.0.0"}},
		},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var entries []ServiceListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].UpdateAvailable {
		t.Error("expected updateAvailable=true when newer version exists")
	}
}

func TestServerVersionTracking_ServiceDetail(t *testing.T) {
	source := &mockSource{
		services: []Service{
			{Name: "svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
		},
		details: map[string]*ServiceDetails{
			"svc": {
				Service:     Service{Name: "svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
				ResolvedRef: "ghcr.io/org/svc@sha256:abc123",
			},
		},
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0"}, {Version: "1.0.0"}},
		},
	}
	base := startTestServer(t, source)

	// First hit list to populate the cached index.
	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck

	// Now get the detail — should pick up version tracking from cached index.
	resp, err = http.Get(base + "/api/services/svc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var detail ServiceDetails
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.VersionPolicy != VersionPolicyPinnedDigest {
		t.Errorf("expected versionPolicy=%q, got %q", VersionPolicyPinnedDigest, detail.VersionPolicy)
	}
	if detail.LatestAvailable != "2.0.0" {
		t.Errorf("expected latestAvailable=2.0.0, got %q", detail.LatestAvailable)
	}
	if !detail.UpdateAvailable {
		t.Error("expected updateAvailable=true")
	}
}

func TestServerVersionTracking_Versions_IsCurrent(t *testing.T) {
	source := &mockSource{
		services: []Service{{Name: "svc", Version: "1.0.0"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0"}}},
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0"}, {Version: "1.0.0"}, {Version: "0.9.0"}},
		},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc/versions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var versions []Version
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}

	for _, v := range versions {
		if v.Version == "1.0.0" && !v.IsCurrent {
			t.Error("expected 1.0.0 to be marked as current")
		}
		if v.Version != "1.0.0" && v.IsCurrent {
			t.Errorf("expected %s NOT to be marked as current", v.Version)
		}
	}
}

func TestServerVersionTracking_NoUpdateWhenCurrent(t *testing.T) {
	source := &mockSource{
		services: []Service{
			{Name: "svc", Version: "2.0.0", ContractStatus: StatusCompliant, Source: "local"},
		},
		details: map[string]*ServiceDetails{
			"svc": {
				Service:     Service{Name: "svc", Version: "2.0.0", ContractStatus: StatusCompliant, Source: "local"},
				ResolvedRef: "ghcr.io/org/svc:2.0.0",
			},
		},
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0"}, {Version: "1.0.0"}},
		},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var entries []ServiceListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].UpdateAvailable {
		t.Error("expected updateAvailable=false when already on latest")
	}
}

func TestServerVersionTracking_PolicyFallback(t *testing.T) {
	// When cached index is empty, getService uses conservative fallback.
	// :latest is ambiguous → returns empty string (not "tracking").
	source := &mockSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:     Service{Name: "svc", Version: "1.0.0", Source: "local"},
				ResolvedRef: "ghcr.io/org/svc:latest",
			},
		},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var detail ServiceDetails
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.VersionPolicy != "" {
		t.Errorf("expected empty versionPolicy for ambiguous :latest ref, got %q", detail.VersionPolicy)
	}
}

func TestServerVersionTracking_OperatorPolicy(t *testing.T) {
	// When operator provides resolutionPolicy, it takes precedence over fallback.
	source := &mockSource{
		services: []Service{
			{Name: "svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
		},
		details: map[string]*ServiceDetails{
			"svc": {
				Service:       Service{Name: "svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
				ResolvedRef:   "ghcr.io/org/svc:1.0.0",
				VersionPolicy: VersionPolicyTracking, // operator says tracking despite semver tag in ref
			},
		},
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0"}, {Version: "1.0.0"}},
		},
	}
	base := startTestServer(t, source)

	// Build cached index via list.
	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck

	// Get detail — should use operator-provided "tracking", not fallback "pinned-tag".
	resp, err = http.Get(base + "/api/services/svc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var detail ServiceDetails
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.VersionPolicy != VersionPolicyTracking {
		t.Errorf("expected operator-provided versionPolicy=%q, got %q", VersionPolicyTracking, detail.VersionPolicy)
	}
}

func TestServerVersionTracking_FallbackPinnedDigest(t *testing.T) {
	// Conservative fallback correctly identifies digest refs.
	source := &mockSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:     Service{Name: "svc", Version: "1.0.0", Source: "oci"},
				ResolvedRef: "ghcr.io/org/svc@sha256:abc123",
			},
		},
	}
	base := startTestServer(t, source)

	resp, err := http.Get(base + "/api/services/svc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var detail ServiceDetails
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.VersionPolicy != VersionPolicyPinnedDigest {
		t.Errorf("expected versionPolicy=%q for digest ref, got %q", VersionPolicyPinnedDigest, detail.VersionPolicy)
	}
}

func pinnedOlderSource() *mockSource {
	return &mockSource{
		services: []Service{
			{Name: "payments", Version: "1.2.0", ContractStatus: StatusCompliant, Source: "oci"},
		},
		details: map[string]*ServiceDetails{
			"payments": {
				Service:       Service{Name: "payments", Version: "1.2.0", ContractStatus: StatusCompliant, Source: "oci"},
				ResolvedRef:   "ghcr.io/org/payments:1.2.0",
				VersionPolicy: VersionPolicyPinnedTag,
			},
		},
		versions: map[string][]Version{
			"payments": {{Version: "2.0.0"}, {Version: "1.2.0"}, {Version: "1.0.0"}},
		},
	}
}

func TestServerVersionTracking_PinnedOlderThanLatest_List(t *testing.T) {
	base := startTestServer(t, pinnedOlderSource())
	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	var entries []ServiceListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Version != "1.2.0" {
		t.Errorf("expected version=1.2.0, got %q", entries[0].Version)
	}
	if !entries[0].UpdateAvailable {
		t.Error("expected updateAvailable=true")
	}
}

func TestServerVersionTracking_PinnedOlderThanLatest_Detail(t *testing.T) {
	base := startTestServer(t, pinnedOlderSource())
	// Warm service index cache (version tracking runs on list).
	warmResp, _ := http.Get(base + "/api/services") //nolint:errcheck
	if warmResp != nil {
		warmResp.Body.Close() //nolint:errcheck
	}
	resp, err := http.Get(base + "/api/services/payments")
	if err != nil {
		t.Fatal(err)
	}
	var detail ServiceDetails
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if detail.Version != "1.2.0" {
		t.Errorf("expected version=1.2.0, got %q", detail.Version)
	}
	if detail.LatestAvailable != "2.0.0" {
		t.Errorf("expected latestAvailable=2.0.0, got %q", detail.LatestAvailable)
	}
	if !detail.UpdateAvailable {
		t.Error("expected updateAvailable=true")
	}
	if detail.VersionPolicy != VersionPolicyPinnedTag {
		t.Errorf("expected versionPolicy=%q, got %q", VersionPolicyPinnedTag, detail.VersionPolicy)
	}
}

func TestServerVersionTracking_PinnedOlderThanLatest_Versions(t *testing.T) {
	base := startTestServer(t, pinnedOlderSource())
	// Warm service index cache (version tracking runs on list).
	warmResp, _ := http.Get(base + "/api/services") //nolint:errcheck
	if warmResp != nil {
		warmResp.Body.Close() //nolint:errcheck
	}
	resp, err := http.Get(base + "/api/services/payments/versions")
	if err != nil {
		t.Fatal(err)
	}
	var versions []Version
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	for _, v := range versions {
		if v.Version == "1.2.0" && !v.IsCurrent {
			t.Error("expected 1.2.0 to be marked as current")
		}
		if v.Version == "2.0.0" && v.IsCurrent {
			t.Error("expected 2.0.0 NOT to be marked as current")
		}
	}
}

func TestServerVersionTracking_VersionsError(t *testing.T) {
	// When GetVersions fails, enrichVersionTracking should skip the service
	// without latestAvailable or updateAvailable.
	source := &mockSource{
		services: []Service{
			{Name: "broken", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "oci"},
		},
		details: map[string]*ServiceDetails{
			"broken": {
				Service: Service{Name: "broken", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "oci"},
			},
		},
		versionsErr: map[string]error{
			"broken": fmt.Errorf("versions unavailable"),
		},
	}
	base := startTestServer(t, source)
	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	var entries []ServiceListEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].UpdateAvailable {
		t.Error("expected updateAvailable=false when versions fail")
	}
}

func TestServerVersionTracking_FreshVersionRecomputesUpdate(t *testing.T) {
	// The cached index may have computed updateAvailable against an old version.
	// When getService returns a fresh version (e.g. operator upgraded), the
	// handler must recompute updateAvailable against the fresh version.
	source := &mockSource{
		services: []Service{
			{Name: "svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
		},
		details: map[string]*ServiceDetails{
			"svc": {
				Service:     Service{Name: "svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "local"},
				ResolvedRef: "ghcr.io/org/svc:1.0.0",
			},
		},
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0"}, {Version: "1.0.0"}},
		},
	}
	base := startTestServer(t, source)

	// Build index (caches updateAvailable=true against version 1.0.0).
	resp, err := http.Get(base + "/api/services")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck

	// Simulate operator upgrading to 2.0.0: mutate the mock's detail.
	source.details["svc"] = &ServiceDetails{
		Service:     Service{Name: "svc", Version: "2.0.0", ContractStatus: StatusCompliant, Source: "local"},
		ResolvedRef: "ghcr.io/org/svc:2.0.0",
	}

	// getService should recompute: version=2.0.0, latestAvailable=2.0.0 → updateAvailable=false.
	resp, err = http.Get(base + "/api/services/svc")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var detail ServiceDetails
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Version != "2.0.0" {
		t.Errorf("expected fresh version=2.0.0, got %q", detail.Version)
	}
	if detail.UpdateAvailable {
		t.Error("expected updateAvailable=false after upgrade to latest, but got true (stale cache)")
	}
}

func TestServer_UnresolvedReasonFn(t *testing.T) {
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewServer(newMockWithDetails(nil), ui)

	// Without OCI source, should return nil.
	if fn := srv.unresolvedReasonFn(); fn != nil {
		t.Error("expected nil when ociSource is nil")
	}

	// With OCI source, should return the method reference.
	ociSrc := NewOCISource(newMockBundleStore(), []string{"ghcr.io/org/svc"})
	srv.SetOCISource(ociSrc)
	fn := srv.unresolvedReasonFn()
	if fn == nil {
		t.Fatal("expected non-nil function when ociSource is set")
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
