package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// serviceFromK8sStatus
// ---------------------------------------------------------------------------

func TestK8s_serviceFromK8sStatus_Minimal(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "my-svc"
	r.Status.Phase = "Healthy"

	svc := serviceFromK8sStatus(r)

	if svc.Name != "my-svc" {
		t.Errorf("expected name 'my-svc', got %q", svc.Name)
	}
	if svc.Phase != PhaseHealthy {
		t.Errorf("expected phase Healthy, got %q", svc.Phase)
	}
	if svc.Source != "k8s" {
		t.Errorf("expected source 'k8s', got %q", svc.Source)
	}
	if svc.Version != "" {
		t.Errorf("expected empty version, got %q", svc.Version)
	}
	if svc.Owner != "" {
		t.Errorf("expected empty owner, got %q", svc.Owner)
	}
}

func TestK8s_serviceFromK8sStatus_WithContract(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "k8s-name"
	r.Status.Phase = "Degraded"
	r.Status.Contract = &k8sContractInfo{
		ServiceName: "api-gateway",
		Version:     "2.0.0",
		Owner:       "platform-team",
	}

	svc := serviceFromK8sStatus(r)

	if svc.Name != "api-gateway" {
		t.Errorf("expected name 'api-gateway', got %q", svc.Name)
	}
	if svc.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", svc.Version)
	}
	if svc.Owner != "platform-team" {
		t.Errorf("expected owner 'platform-team', got %q", svc.Owner)
	}
}

func TestK8s_serviceFromK8sStatus_ContractVersionOverride(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "svc"
	r.Status.Contract = &k8sContractInfo{Version: "1.0.0"}
	r.Status.ContractVersion = "3.0.0"

	svc := serviceFromK8sStatus(r)

	// ContractVersion takes precedence over Contract.Version.
	if svc.Version != "3.0.0" {
		t.Errorf("expected version '3.0.0', got %q", svc.Version)
	}
}

func TestK8s_serviceFromK8sStatus_EmptyPhaseDefaultsToUnknown(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "svc"

	svc := serviceFromK8sStatus(r)

	if svc.Phase != PhaseUnknown {
		t.Errorf("expected phase Unknown, got %q", svc.Phase)
	}
}

// ---------------------------------------------------------------------------
// serviceDetailsFromK8sStatus — comprehensive
// ---------------------------------------------------------------------------

func TestK8s_serviceDetailsFromK8sStatus_Comprehensive(t *testing.T) {
	d := buildComprehensiveK8sDetails(t)
	assertDetailsServiceFields(t, d)
	assertDetailsInterfaces(t, d)
	assertDetailsConfig(t, d)
	assertDetailsPolicy(t, d)
	assertDetailsDeps(t, d)
	assertDetailsRuntime(t, d)
	assertDetailsScaling(t, d)
	assertDetailsValidation(t, d)
	assertDetailsResources(t, d)
	assertDetailsPorts(t, d)
	assertDetailsConditions(t, d)
	assertDetailsEndpoints(t, d)
	assertDetailsInsights(t, d)
	assertDetailsChecksSummary(t, d)
}

func assertDetailsServiceFields(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Name != "billing" {
		t.Errorf("name: got %q", d.Name)
	}
	if d.Version != "1.2.3" {
		t.Errorf("version: got %q", d.Version)
	}
	if d.Owner != "payments" {
		t.Errorf("owner: got %q", d.Owner)
	}
	if d.Phase != PhaseHealthy {
		t.Errorf("phase: got %q", d.Phase)
	}
	if d.ImageRef != "ghcr.io/org/billing:1.2.3" {
		t.Errorf("imageRef: got %q", d.ImageRef)
	}
	if d.Metadata["team"] != "platform" {
		t.Errorf("metadata team: got %q", d.Metadata["team"])
	}
	if d.LastReconciledAt == "" || !strings.HasSuffix(d.LastReconciledAt, "ago") {
		t.Errorf("lastReconciledAt: got %q", d.LastReconciledAt)
	}
}

func assertDetailsInterfaces(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(d.Interfaces))
	}
	iface := d.Interfaces[0]
	if iface.Name != "http" || iface.Type != "http" || *iface.Port != 8080 || iface.Visibility != "public" || !iface.HasContractFile {
		t.Errorf("interface mismatch: %+v", iface)
	}
}

func assertDetailsConfig(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Configuration == nil {
		t.Fatal("expected configuration")
	}
	if !d.Configuration.HasSchema || d.Configuration.Ref != "config-ref" {
		t.Errorf("configuration mismatch: %+v", d.Configuration)
	}
	if len(d.Configuration.ValueKeys) != 1 || d.Configuration.ValueKeys[0] != "key1" {
		t.Errorf("config valueKeys: %v", d.Configuration.ValueKeys)
	}
}

func assertDetailsPolicy(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Policy == nil {
		t.Fatal("expected policy")
	}
	if !d.Policy.HasSchema || d.Policy.Schema != "policy.json" || d.Policy.Ref != "policy-ref" {
		t.Errorf("policy mismatch: %+v", d.Policy)
	}
}

func assertDetailsDeps(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(d.Dependencies))
	}
	dep := d.Dependencies[0]
	if dep.Ref != "auth@^1.0.0" || !dep.Required || dep.Compatibility != "strict" {
		t.Errorf("dependency mismatch: %+v", dep)
	}
}

func assertDetailsRuntime(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Runtime == nil {
		t.Fatal("expected runtime")
	}
	if d.Runtime.Workload != "service" || d.Runtime.StateType != "stateless" || d.Runtime.HealthPath != "/healthz" {
		t.Errorf("runtime mismatch: %+v", d.Runtime)
	}
	if d.Runtime.GracefulShutdownSeconds == nil || *d.Runtime.GracefulShutdownSeconds != 30 {
		t.Errorf("graceful shutdown: %v", d.Runtime.GracefulShutdownSeconds)
	}
}

func assertDetailsScaling(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Scaling == nil {
		t.Fatal("expected scaling")
	}
	if *d.Scaling.Replicas != 3 || *d.Scaling.Min != 1 || *d.Scaling.Max != 5 {
		t.Errorf("scaling mismatch: %+v", d.Scaling)
	}
}

func assertDetailsValidation(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Validation == nil {
		t.Fatal("expected validation")
	}
	if d.Validation.Valid {
		t.Error("expected valid=false")
	}
	if len(d.Validation.Errors) != 1 || d.Validation.Errors[0].Code != "E001" {
		t.Errorf("errors: %v", d.Validation.Errors)
	}
	if len(d.Validation.Warnings) != 1 || d.Validation.Warnings[0].Code != "W001" {
		t.Errorf("warnings: %v", d.Validation.Warnings)
	}
}

func assertDetailsResources(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Resources == nil {
		t.Fatal("expected resources")
	}
	if d.Resources.ServiceExists == nil || !*d.Resources.ServiceExists {
		t.Error("expected serviceExists=true")
	}
	if d.Resources.WorkloadExists == nil || *d.Resources.WorkloadExists {
		t.Error("expected workloadExists=false")
	}
}

func assertDetailsPorts(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Ports == nil {
		t.Fatal("expected ports")
	}
	if len(d.Ports.Unexpected) != 1 || d.Ports.Unexpected[0] != 9090 {
		t.Errorf("unexpected ports: %v", d.Ports.Unexpected)
	}
}

func assertDetailsConditions(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Conditions) != 1 {
		t.Fatalf("expected 1, got %d", len(d.Conditions))
	}
	cond := d.Conditions[0]
	if cond.Type != "Ready" || cond.Status != "True" || cond.Reason != "AllChecks" {
		t.Errorf("condition mismatch: %+v", cond)
	}
	if cond.LastTransitionAgo == "" || !strings.HasSuffix(cond.LastTransitionAgo, "ago") {
		t.Errorf("lastTransitionAgo: got %q", cond.LastTransitionAgo)
	}
}

func assertDetailsEndpoints(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Endpoints) != 1 {
		t.Fatalf("expected 1, got %d", len(d.Endpoints))
	}
	ep := d.Endpoints[0]
	if ep.Interface != "http" || ep.Type != "health" || ep.URL != "http://billing:8080/healthz" {
		t.Errorf("endpoint mismatch: %+v", ep)
	}
	if ep.Healthy == nil || !*ep.Healthy {
		t.Error("expected healthy=true")
	}
	if ep.StatusCode == nil || *ep.StatusCode != 200 {
		t.Error("expected statusCode=200")
	}
	if ep.LatencyMs == nil || *ep.LatencyMs != 42 {
		t.Error("expected latencyMs=42")
	}
}

func assertDetailsInsights(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Insights) != 1 {
		t.Fatalf("expected 1, got %d", len(d.Insights))
	}
	if d.Insights[0].Severity != "warning" || d.Insights[0].Title != "High latency" {
		t.Errorf("insight mismatch: %+v", d.Insights[0])
	}
}

func assertDetailsChecksSummary(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.ChecksSummary == nil {
		t.Fatal("expected checksSummary")
	}
	if d.ChecksSummary.Total != 10 || d.ChecksSummary.Passed != 8 || d.ChecksSummary.Failed != 2 {
		t.Errorf("checksSummary mismatch: %+v", d.ChecksSummary)
	}
}

func buildComprehensiveK8sDetails(t *testing.T) *ServiceDetails {
	t.Helper()
	port := 8080
	replicas := 3
	minR := 1
	maxR := 5
	graceful := 30
	healthy := true
	statusCode := 200
	latency := int64(42)

	r := &pactoResource{}
	r.Metadata.Name = "k8s-name"
	r.Status.Phase = "Healthy"
	r.Status.ContractVersion = "1.2.3"
	r.Status.LastReconciledAt = time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
	r.Status.Contract = &k8sContractInfo{ServiceName: "billing", Version: "1.0.0", Owner: "payments", ImageRef: "ghcr.io/org/billing:1.2.3", ResolvedRef: "sha256:abc"}
	r.Status.Metadata = map[string]string{"team": "platform", "env": "prod"}
	r.Status.Interfaces = flexSlice[k8sInterface]{{Name: "http", Type: "http", Port: &port, Visibility: "public", HasContractFile: true}}
	r.Status.Configuration = &k8sConfig{HasSchema: true, Ref: "config-ref", ValueKeys: []string{"key1"}, SecretKeys: []string{"secret1"}}
	r.Status.Policy = &k8sPolicy{HasSchema: true, Schema: "policy.json", Ref: "policy-ref"}
	r.Status.Dependencies = flexSlice[k8sDependency]{{Ref: "auth@^1.0.0", Required: true, Compatibility: "strict"}}
	r.Status.Runtime = &k8sRuntime{Workload: "service", StateType: "stateless", PersistenceScope: "none", PersistenceDurability: "ephemeral", DataCriticality: "low", UpgradeStrategy: "rolling", GracefulShutdownSeconds: &graceful, HealthInterface: "http", HealthPath: "/healthz", MetricsInterface: "http", MetricsPath: "/metrics"}
	r.Status.Scaling = &k8sScaling{Replicas: &replicas, Min: &minR, Max: &maxR}
	r.Status.Validation = &k8sValidation{Valid: false, Errors: []k8sIssue{{Code: "E001", Path: "/service/name", Message: "name is required"}}, Warnings: []k8sIssue{{Code: "W001", Path: "/runtime", Message: "deprecated field"}}}
	r.Status.Resources = &k8sResources{Service: &k8sResourceStatus{Exists: true}, Workload: &k8sResourceStatus{Exists: false}}
	r.Status.Ports = &k8sPorts{Expected: []int{8080}, Observed: []int{8080, 9090}, Missing: nil, Unexpected: []int{9090}}
	condTime := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	r.Status.Conditions = flexSlice[k8sCondition]{{Type: "Ready", Status: "True", Reason: "AllChecks", Message: "all good", LastTransitionTime: condTime}}
	r.Status.Endpoints = flexSlice[k8sEndpoint]{{Interface: "http", Type: "health", URL: "http://billing:8080/healthz", Healthy: &healthy, StatusCode: &statusCode, LatencyMs: &latency, Error: "", Message: "OK"}}
	r.Status.Insights = flexSlice[k8sInsight]{{Severity: "warning", Title: "High latency", Description: "p99 > 500ms"}}
	r.Status.Summary = &k8sSummary{Total: 10, Passed: 8, Failed: 2}

	return serviceDetailsFromK8sStatus(r)
}

// ---------------------------------------------------------------------------
// timeAgoFromRFC3339
// ---------------------------------------------------------------------------

func TestK8s_timeAgoFromRFC3339_Valid(t *testing.T) {
	ts := time.Now().Add(-30 * time.Second).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.HasSuffix(result, "ago") {
		t.Errorf("expected 'ago' suffix, got %q", result)
	}
	if !strings.Contains(result, "s") {
		t.Errorf("expected seconds unit, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Invalid(t *testing.T) {
	result := timeAgoFromRFC3339("not-a-timestamp")
	if result != "" {
		t.Errorf("expected empty string for invalid input, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Minutes(t *testing.T) {
	ts := time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.Contains(result, "m ago") {
		t.Errorf("expected minutes format, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Hours(t *testing.T) {
	ts := time.Now().Add(-3 * time.Hour).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.Contains(result, "h ago") {
		t.Errorf("expected hours format, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Days(t *testing.T) {
	ts := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.Contains(result, "d ago") {
		t.Errorf("expected days format, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// flexSlice.UnmarshalJSON
// ---------------------------------------------------------------------------

func TestK8s_flexSlice_Array(t *testing.T) {
	input := `[{"name":"a"},{"name":"b"}]`
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte(input), &fs); err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("expected 2 items, got %d", len(fs))
	}
	if fs[0].Name != "a" || fs[1].Name != "b" {
		t.Errorf("unexpected items: %+v", fs)
	}
}

func TestK8s_flexSlice_SingleObject(t *testing.T) {
	input := `{"name":"only"}`
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte(input), &fs); err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 {
		t.Fatalf("expected 1 item, got %d", len(fs))
	}
	if fs[0].Name != "only" {
		t.Errorf("expected name 'only', got %q", fs[0].Name)
	}
}

func TestK8s_flexSlice_InvalidJSON(t *testing.T) {
	input := `not json`
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte(input), &fs); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestK8s_flexSlice_NullInput(t *testing.T) {
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte("null"), &fs); err != nil {
		t.Fatalf("unexpected error for null input: %v", err)
	}
	if len(fs) != 0 {
		t.Errorf("expected empty slice for null input, got %d items", len(fs))
	}
}

func TestK8s_flexSlice_InvalidJSON_ViaStatus(t *testing.T) {
	// Test the error path through full pactoStatus unmarshal to ensure coverage
	// hits the concrete instantiation used in production code.
	input := `{"interfaces": 42}` // 42 is not an array or object
	var status pactoStatus
	if err := json.Unmarshal([]byte(input), &status); err == nil {
		t.Error("expected error for invalid interfaces value")
	}
}

// ---------------------------------------------------------------------------
// Mock K8sClient for source tests
// ---------------------------------------------------------------------------

type mockK8sClient struct {
	probeErr     error
	crdDiscovery *CRDDiscovery
	crdErr       error
	listJSON     []byte
	listErr      error
	selectorJSON map[string][]byte // keyed by labelSelector
	selectorErr  error
	getJSON      []byte
	getErr       error
	countResult  int
	countErr     error
}

func (m *mockK8sClient) Probe(context.Context) error { return m.probeErr }
func (m *mockK8sClient) DiscoverCRD(context.Context) (*CRDDiscovery, error) {
	return m.crdDiscovery, m.crdErr
}
func (m *mockK8sClient) ListJSON(context.Context, string, string) ([]byte, error) {
	return m.listJSON, m.listErr
}
func (m *mockK8sClient) ListJSONWithSelector(_ context.Context, _, _, selector string) ([]byte, error) {
	if m.selectorErr != nil {
		return nil, m.selectorErr
	}
	if m.selectorJSON != nil {
		if data, ok := m.selectorJSON[selector]; ok {
			return data, nil
		}
	}
	return m.listJSON, m.listErr
}
func (m *mockK8sClient) GetJSON(context.Context, string, string, string) ([]byte, error) {
	return m.getJSON, m.getErr
}
func (m *mockK8sClient) CountResources(context.Context, string, string) (int, error) {
	return m.countResult, m.countErr
}

// ---------------------------------------------------------------------------
// NewK8sSource
// ---------------------------------------------------------------------------

func TestK8s_NewK8sSource_DefaultResourceName(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "", "")
	if src.resourceName != "pactos" {
		t.Errorf("expected default resource name 'pactos', got %q", src.resourceName)
	}
	if src.namespace != "default" {
		t.Errorf("expected namespace 'default', got %q", src.namespace)
	}
}

func TestK8s_NewK8sSource_CustomResourceName(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "prod", "pactocontracts", "pactorevisions")
	if src.resourceName != "pactocontracts" {
		t.Errorf("expected resource name 'pactocontracts', got %q", src.resourceName)
	}
	if src.namespace != "prod" {
		t.Errorf("expected namespace 'prod', got %q", src.namespace)
	}
}

// ---------------------------------------------------------------------------
// setListCache
// ---------------------------------------------------------------------------

func TestK8s_setListCache(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos", "")

	items := []pactoResource{
		{Status: pactoStatus{Phase: "Healthy"}},
	}
	items[0].Metadata.Name = "svc-a"

	src.setListCache(items, nil)

	src.listMu.Lock()
	defer src.listMu.Unlock()

	if len(src.listCache) != 1 {
		t.Fatalf("expected 1 cached item, got %d", len(src.listCache))
	}
	if src.listCache[0].Metadata.Name != "svc-a" {
		t.Errorf("expected cached name 'svc-a', got %q", src.listCache[0].Metadata.Name)
	}
	if src.listErr != nil {
		t.Errorf("expected nil error, got %v", src.listErr)
	}
	if src.listAt.IsZero() {
		t.Error("expected listAt to be set")
	}
}

func TestK8s_setListCache_WithError(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos", "")

	testErr := fmt.Errorf("connection refused")
	src.setListCache(nil, testErr)

	src.listMu.Lock()
	defer src.listMu.Unlock()

	if src.listCache != nil {
		t.Errorf("expected nil cache, got %v", src.listCache)
	}
	if src.listErr == nil || src.listErr.Error() != "connection refused" {
		t.Errorf("expected 'connection refused', got %v", src.listErr)
	}
}

// ---------------------------------------------------------------------------
// GetVersions
// ---------------------------------------------------------------------------

func TestK8s_GetVersions_NoRevisionCRD(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos", "")
	_, err := src.GetVersions(context.Background(), "any")
	if err == nil {
		t.Fatal("expected error from GetVersions")
	}
	if !strings.Contains(err.Error(), "PactoRevision CRD not available") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestK8s_GetVersions_Success(t *testing.T) {
	revisionsJSON := `{"items": [
		{"metadata": {"name": "billing-1-0-0-abc"}, "spec": {"version": "1.0.0", "serviceName": "billing", "source": {"oci": "ghcr.io/org/billing:1.0.0"}, "pactoRef": "billing"}, "status": {"resolved": true, "contractHash": "sha256:aaa", "createdAt": "2025-01-01T00:00:00Z"}},
		{"metadata": {"name": "billing-2-0-0-def"}, "spec": {"version": "2.0.0", "serviceName": "billing", "source": {"oci": "ghcr.io/org/billing:2.0.0"}, "pactoRef": "billing"}, "status": {"resolved": true, "contractHash": "sha256:bbb", "createdAt": "2025-06-01T00:00:00Z"}}
	]}`
	pactosJSON := `{"items": [{"metadata": {"name": "billing"}, "status": {"phase": "Healthy", "contract": {"serviceName": "billing", "version": "2.0.0"}}}]}`

	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=billing": []byte(revisionsJSON),
		},
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")
	versions, err := src.GetVersions(context.Background(), "billing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	// Should be sorted descending.
	if versions[0].Version != "2.0.0" {
		t.Errorf("expected first version '2.0.0', got %q", versions[0].Version)
	}
	if versions[1].Version != "1.0.0" {
		t.Errorf("expected second version '1.0.0', got %q", versions[1].Version)
	}
	if versions[0].Ref != "ghcr.io/org/billing:2.0.0" {
		t.Errorf("expected ref 'ghcr.io/org/billing:2.0.0', got %q", versions[0].Ref)
	}
	if versions[0].ContractHash != "sha256:bbb" {
		t.Errorf("expected hash 'sha256:bbb', got %q", versions[0].ContractHash)
	}
	if versions[0].CreatedAt == nil {
		t.Fatal("expected createdAt to be set")
	}
}

func TestK8s_GetVersions_ServiceNameMapping(t *testing.T) {
	// When the contract.serviceName differs from metadata.name,
	// revisions are queried using the K8s metadata.name.
	revisionsJSON := `{"items": [
		{"metadata": {"name": "my-billing-1-0-0"}, "spec": {"version": "1.0.0", "serviceName": "billing"}, "status": {"resolved": true, "contractHash": "sha256:aaa"}}
	]}`
	pactosJSON := `{"items": [{"metadata": {"name": "k8s-billing-name"}, "status": {"phase": "Healthy", "contract": {"serviceName": "billing", "version": "1.0.0"}}}]}`

	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=k8s-billing-name": []byte(revisionsJSON),
		},
	}
	src := NewK8sSource(client, "", "pactos", "pactorevisions")
	versions, err := src.GetVersions(context.Background(), "billing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
}

func TestK8s_GetVersions_NameFallback(t *testing.T) {
	// When the service name doesn't match any contract.serviceName,
	// resolvePactoName falls back to using the name directly.
	revisionsJSON := `{"items": [
		{"metadata": {"name": "unknown-1-0-0"}, "spec": {"version": "1.0.0"}, "status": {"resolved": true}}
	]}`
	pactosJSON := `{"items": [{"metadata": {"name": "other"}, "status": {"phase": "Healthy", "contract": {"serviceName": "other"}}}]}`
	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=unknown": []byte(revisionsJSON),
		},
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")
	versions, err := src.GetVersions(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
}

func TestK8s_GetVersions_ListError(t *testing.T) {
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy"}}]}`
	client := &mockK8sClient{
		listJSON:    []byte(pactosJSON),
		selectorErr: fmt.Errorf("forbidden"),
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")
	_, err := src.GetVersions(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "listing revisions") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_GetVersions_ResolvePactoNameError(t *testing.T) {
	client := &mockK8sClient{
		listErr: fmt.Errorf("connection refused"),
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")
	_, err := src.GetVersions(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "listing") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_GetVersions_BadJSON(t *testing.T) {
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy"}}]}`
	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=svc": []byte("not json"),
		},
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")
	_, err := src.GetVersions(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parsing revision response") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_GetVersions_EmptyRevisions(t *testing.T) {
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy"}}]}`
	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=svc": []byte(`{"items": []}`),
		},
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")
	versions, err := src.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("expected 0 versions, got %d", len(versions))
	}
}

func TestK8s_GetVersions_NoCreatedAt(t *testing.T) {
	revisionsJSON := `{"items": [
		{"metadata": {"name": "svc-1-0-0"}, "spec": {"version": "1.0.0"}, "status": {"resolved": true, "contractHash": "sha256:aaa"}}
	]}`
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy"}}]}`

	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=svc": []byte(revisionsJSON),
		},
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")
	versions, err := src.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].CreatedAt != nil {
		t.Error("expected nil createdAt when not set")
	}
	if versions[0].Ref != "" {
		t.Error("expected empty ref when source.oci not set")
	}
}

// ---------------------------------------------------------------------------
// GetDiff
// ---------------------------------------------------------------------------

func TestK8s_GetDiff_NoStore(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos", "")
	_, err := src.GetDiff(context.Background(), Ref{Name: "a", Version: "1"}, Ref{Name: "a", Version: "2"})
	if err == nil {
		t.Fatal("expected error from GetDiff without store")
	}
	if !strings.Contains(err.Error(), "diff requires OCI store") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestK8s_GetDiff_Success(t *testing.T) {
	revisionsJSON := `{"items": [
		{"metadata": {"name": "svc-1-0-0"}, "spec": {"version": "1.0.0", "source": {"oci": "ghcr.io/org/svc:1.0.0"}}, "status": {"resolved": true}},
		{"metadata": {"name": "svc-2-0-0"}, "spec": {"version": "2.0.0", "source": {"oci": "ghcr.io/org/svc:2.0.0"}}, "status": {"resolved": true}}
	]}`
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:2.0.0"}}}]}`
	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=svc": []byte(revisionsJSON),
		},
	}
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/svc", "1.0.0", "svc", "1.0.0")
	store.addBundle("ghcr.io/org/svc", "2.0.0", "svc", "2.0.0")

	src := NewK8sSource(client, "", "pactos", "pactorevisions")
	src.SetStore(store)

	result, err := src.GetDiff(context.Background(),
		Ref{Name: "svc", Version: "1.0.0"},
		Ref{Name: "svc", Version: "2.0.0"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil diff result")
	}
}

func TestK8s_GetDiff_RefAError(t *testing.T) {
	client := &mockK8sClient{
		listErr: fmt.Errorf("connection refused"),
	}
	store := newMockBundleStore()
	src := NewK8sSource(client, "", "pactos", "pactorevisions")
	src.SetStore(store)

	_, err := src.GetDiff(context.Background(),
		Ref{Name: "svc", Version: "1.0.0"},
		Ref{Name: "svc", Version: "2.0.0"},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "resolving ref for svc@1.0.0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_GetDiff_RefBError(t *testing.T) {
	// First ref resolves, second fails.
	pactosJSON := `{"items": [
		{"metadata": {"name": "svc-a"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc-a", "imageRef": "ghcr.io/org/svc-a:1.0.0"}}},
		{"metadata": {"name": "svc-b"}, "status": {"phase": "Healthy"}}
	]}`
	client := &mockK8sClient{listJSON: []byte(pactosJSON)}
	store := newMockBundleStore()
	src := NewK8sSource(client, "", "pactos", "")
	src.SetStore(store)

	_, err := src.GetDiff(context.Background(),
		Ref{Name: "svc-a", Version: "1.0.0"},
		Ref{Name: "svc-b", Version: "2.0.0"},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "resolving ref for svc-b@2.0.0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_GetDiff_PullAError(t *testing.T) {
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:1.0.0"}}}]}`
	client := &mockK8sClient{listJSON: []byte(pactosJSON)}
	store := newMockBundleStore() // no bundles added, Pull will fail
	src := NewK8sSource(client, "", "pactos", "")
	src.SetStore(store)

	_, err := src.GetDiff(context.Background(),
		Ref{Name: "svc", Version: "1.0.0"},
		Ref{Name: "svc", Version: "2.0.0"},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pulling") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_GetDiff_PullBError(t *testing.T) {
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:1.0.0"}}}]}`
	client := &mockK8sClient{listJSON: []byte(pactosJSON)}
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/svc", "1.0.0", "svc", "1.0.0") // Only A exists
	src := NewK8sSource(client, "", "pactos", "")
	src.SetStore(store)

	_, err := src.GetDiff(context.Background(),
		Ref{Name: "svc", Version: "1.0.0"},
		Ref{Name: "svc", Version: "2.0.0"},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pulling ghcr.io/org/svc:2.0.0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_SetStore(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos", "")
	if src.store != nil {
		t.Fatal("expected nil store initially")
	}
	src.SetStore(&mockBundleStore{})
	if src.store == nil {
		t.Fatal("expected store to be set")
	}
}

func TestK8s_ociRefForVersion_FromRevision(t *testing.T) {
	revisionsJSON := `{"items": [
		{"metadata": {"name": "svc-1-0-0"}, "spec": {"version": "1.0.0", "source": {"oci": "ghcr.io/org/svc:1.0.0"}}, "status": {"resolved": true}}
	]}`
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:latest"}}}]}`
	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=svc": []byte(revisionsJSON),
		},
	}
	src := NewK8sSource(client, "default", "pactos", "pactorevisions")

	ref, err := src.ociRefForVersion(context.Background(), "svc", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref != "ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected 'ghcr.io/org/svc:1.0.0', got %q", ref)
	}
}

func TestK8s_ociRefForVersion_FallbackToImageRef(t *testing.T) {
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:latest"}}}]}`
	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=svc": []byte(`{"items": []}`),
		},
	}
	// Use all-namespaces mode so getPacto falls back to listPactos.
	src := NewK8sSource(client, "", "pactos", "pactorevisions")

	ref, err := src.ociRefForVersion(context.Background(), "svc", "2.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref != "ghcr.io/org/svc:2.0.0" {
		t.Errorf("expected 'ghcr.io/org/svc:2.0.0', got %q", ref)
	}
}

func TestK8s_ociRefForVersion_NoRef(t *testing.T) {
	pactosJSON := `{"items": [{"metadata": {"name": "svc"}, "status": {"phase": "Healthy"}}]}`
	client := &mockK8sClient{
		listJSON: []byte(pactosJSON),
		selectorJSON: map[string][]byte{
			"pacto.trianalab.io/pacto=svc": []byte(`{"items": []}`),
		},
	}
	// Use all-namespaces mode so getPacto falls back to listPactos.
	src := NewK8sSource(client, "", "pactos", "pactorevisions")

	_, err := src.ociRefForVersion(context.Background(), "svc", "1.0.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no OCI reference found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestK8s_ociRefForVersion_NoRevisionCRD(t *testing.T) {
	// When revisionResourceName is empty, falls back to imageRef directly.
	singleJSON := `{"metadata": {"name": "svc", "namespace": "default"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc", "imageRef": "ghcr.io/org/svc:1.0.0"}}}`
	client := &mockK8sClient{
		getJSON: []byte(singleJSON),
	}
	src := NewK8sSource(client, "default", "pactos", "")

	ref, err := src.ociRefForVersion(context.Background(), "svc", "3.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref != "ghcr.io/org/svc:3.0.0" {
		t.Errorf("expected 'ghcr.io/org/svc:3.0.0', got %q", ref)
	}
}

func TestK8s_ociRefForVersion_GetPactoError(t *testing.T) {
	client := &mockK8sClient{
		getErr: fmt.Errorf("not found"),
	}
	src := NewK8sSource(client, "default", "pactos", "")

	_, err := src.ociRefForVersion(context.Background(), "svc", "1.0.0")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestK8s_OCIRepos(t *testing.T) {
	pactosJSON := `{"items": [
		{"metadata": {"name": "billing"}, "status": {"phase": "Healthy", "contract": {"serviceName": "billing", "imageRef": "ghcr.io/org/billing:1.0.0"}, "dependencies": [{"ref": "oci://ghcr.io/org/auth@^1.0.0", "required": true}]}},
		{"metadata": {"name": "auth"}, "status": {"phase": "Healthy", "contract": {"serviceName": "auth", "imageRef": "ghcr.io/org/auth:2.0.0"}}}
	]}`
	client := &mockK8sClient{listJSON: []byte(pactosJSON)}
	src := NewK8sSource(client, "", "pactos", "")

	repos := src.OCIRepos(context.Background())
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d: %v", len(repos), repos)
	}
	// Should be sorted.
	if repos[0] != "ghcr.io/org/auth" {
		t.Errorf("expected first repo 'ghcr.io/org/auth', got %q", repos[0])
	}
	if repos[1] != "ghcr.io/org/billing" {
		t.Errorf("expected second repo 'ghcr.io/org/billing', got %q", repos[1])
	}
}

func TestK8s_OCIRepos_SkipsNonOCIDeps(t *testing.T) {
	pactosJSON := `{"items": [
		{"metadata": {"name": "app"}, "status": {"phase": "Healthy", "contract": {"serviceName": "app", "imageRef": "ghcr.io/org/app:1.0.0"}, "dependencies": [
			{"ref": "oci://ghcr.io/org/auth@^1.0.0", "required": true},
			{"ref": "docker.io/library/postgres", "required": true},
			{"ref": "docker.io/library/redis", "required": true}
		]}}
	]}`
	client := &mockK8sClient{listJSON: []byte(pactosJSON)}
	src := NewK8sSource(client, "", "pactos", "")

	repos := src.OCIRepos(context.Background())
	// Should only contain the imageRef repo and the oci:// dep, not postgres/redis.
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d: %v", len(repos), repos)
	}
	for _, r := range repos {
		if strings.Contains(r, "postgres") || strings.Contains(r, "redis") {
			t.Errorf("non-OCI dep should be filtered: %q", r)
		}
	}
}

func TestK8s_OCIRepos_Empty(t *testing.T) {
	client := &mockK8sClient{listJSON: []byte(`{"items": []}`)}
	src := NewK8sSource(client, "", "pactos", "")

	repos := src.OCIRepos(context.Background())
	if len(repos) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repos))
	}
}

func TestK8s_OCIRepos_ListError(t *testing.T) {
	client := &mockK8sClient{listErr: fmt.Errorf("error")}
	src := NewK8sSource(client, "", "pactos", "")

	repos := src.OCIRepos(context.Background())
	if repos != nil {
		t.Errorf("expected nil repos on error, got %v", repos)
	}
}

// ---------------------------------------------------------------------------
// ListServices
// ---------------------------------------------------------------------------

func TestK8s_ListServices(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-b", "namespace": "default"}, "status": {"phase": "Healthy", "contract": {"serviceName": "svc-b", "version": "1.0.0"}}}, {"metadata": {"name": "svc-a"}, "status": {"phase": "Progressing"}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "default", "pactos", "")
	services, err := src.ListServices(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	// Should be sorted by name.
	if services[0].Name != "svc-a" {
		t.Errorf("expected first service 'svc-a', got %q", services[0].Name)
	}
	if services[1].Name != "svc-b" {
		t.Errorf("expected second service 'svc-b', got %q", services[1].Name)
	}
	if services[1].Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", services[1].Version)
	}
	if services[0].Phase != PhaseUnknown {
		t.Errorf("expected phase Unknown (normalized from Progressing), got %q", services[0].Phase)
	}
	if services[1].Phase != PhaseHealthy {
		t.Errorf("expected phase Healthy, got %q", services[1].Phase)
	}
}

// ---------------------------------------------------------------------------
// GetService with namespace (direct API get)
// ---------------------------------------------------------------------------

func TestK8s_GetService_WithNamespace(t *testing.T) {
	singleJSON := `{"metadata": {"name": "my-svc", "namespace": "default"}, "status": {"phase": "Healthy", "contract": {"serviceName": "my-svc", "version": "2.0.0"}}}`
	client := &mockK8sClient{getJSON: []byte(singleJSON)}

	src := NewK8sSource(client, "default", "pactos", "")
	details, err := src.GetService(context.Background(), "my-svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details.Name != "my-svc" {
		t.Errorf("expected name 'my-svc', got %q", details.Name)
	}
	if details.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", details.Version)
	}
	if details.Phase != PhaseHealthy {
		t.Errorf("expected phase Healthy, got %q", details.Phase)
	}
}

// ---------------------------------------------------------------------------
// GetService without namespace (lists all, filters by name)
// ---------------------------------------------------------------------------

func TestK8s_GetService_WithoutNamespace(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-a"}, "status": {"phase": "Healthy"}}, {"metadata": {"name": "target-svc"}, "status": {"phase": "Degraded", "contract": {"serviceName": "target-svc", "version": "3.0.0"}}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "", "pactos", "")
	details, err := src.GetService(context.Background(), "target-svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details.Name != "target-svc" {
		t.Errorf("expected name 'target-svc', got %q", details.Name)
	}
	if details.Version != "3.0.0" {
		t.Errorf("expected version '3.0.0', got %q", details.Version)
	}
	if details.Phase != PhaseDegraded {
		t.Errorf("expected phase Degraded, got %q", details.Phase)
	}
}

// ---------------------------------------------------------------------------
// listPactos error (API call fails)
// ---------------------------------------------------------------------------

func TestK8s_listPactos_Error(t *testing.T) {
	client := &mockK8sClient{listErr: fmt.Errorf("connection refused")}

	src := NewK8sSource(client, "default", "pactos", "")
	_, err := src.ListServices(context.Background())
	if err == nil {
		t.Fatal("expected error from ListServices when API call fails")
	}
	if !strings.Contains(err.Error(), "listing") {
		t.Errorf("expected error to mention 'listing', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// listPactos bad JSON
// ---------------------------------------------------------------------------

func TestK8s_listPactos_BadJSON(t *testing.T) {
	client := &mockK8sClient{listJSON: []byte("this is not valid json")}

	src := NewK8sSource(client, "default", "pactos", "")
	_, err := src.ListServices(context.Background())
	if err == nil {
		t.Fatal("expected error from ListServices when API returns bad JSON")
	}
	if !strings.Contains(err.Error(), "parsing API response") {
		t.Errorf("expected error to mention 'parsing API response', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getPacto not found (list returns items but none match)
// ---------------------------------------------------------------------------

func TestK8s_getPacto_NotFound(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-a"}, "status": {"phase": "Healthy"}}, {"metadata": {"name": "svc-b"}, "status": {"phase": "Healthy"}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "", "pactos", "")
	_, err := src.GetService(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error when service is not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to mention 'not found', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// listPactos cache hit — second call within TTL returns cached result
// ---------------------------------------------------------------------------

func TestK8s_listPactos_CacheHit(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-a"}, "status": {"phase": "Healthy"}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "default", "pactos", "")

	// First call populates cache.
	items1, err := src.listPactos(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(items1) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items1))
	}

	// Replace client response with different data to prove cache is used.
	client.listJSON = []byte(`{"items": []}`)

	// Second call should return cached data (within TTL).
	items2, err := src.listPactos(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(items2) != 1 {
		t.Fatalf("expected cached 1 item, got %d (cache not hit)", len(items2))
	}
}

// ---------------------------------------------------------------------------
// getPacto matches by contract.ServiceName in all-namespaces mode
// ---------------------------------------------------------------------------

func TestK8s_getPacto_MatchByServiceName(t *testing.T) {
	listJSON := `{"items": [
		{"metadata": {"name": "k8s-resource-name"}, "status": {"phase": "Healthy", "contract": {"serviceName": "my-service", "version": "1.0.0"}}}
	]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "", "pactos", "") // all-namespaces mode
	details, err := src.GetService(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details.Name != "my-service" {
		t.Errorf("expected name 'my-service', got %q", details.Name)
	}
}

// ---------------------------------------------------------------------------
// getPacto API error (with namespace, API call fails)
// ---------------------------------------------------------------------------

func TestK8s_getPacto_APIError_WithNamespace(t *testing.T) {
	client := &mockK8sClient{getErr: fmt.Errorf("connection refused")}

	src := NewK8sSource(client, "default", "pactos", "")
	_, err := src.GetService(context.Background(), "my-svc")
	if err == nil {
		t.Fatal("expected error when API call fails for direct get")
	}
	if !strings.Contains(err.Error(), "getting") {
		t.Errorf("expected error to mention 'getting', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getPacto bad JSON (with namespace, direct get returns invalid JSON)
// ---------------------------------------------------------------------------

func TestK8s_getPacto_BadJSON_WithNamespace(t *testing.T) {
	client := &mockK8sClient{getJSON: []byte("not valid json at all")}

	src := NewK8sSource(client, "default", "pactos", "")
	_, err := src.GetService(context.Background(), "my-svc")
	if err == nil {
		t.Fatal("expected error when API returns bad JSON for direct get")
	}
	if !strings.Contains(err.Error(), "parsing API response") {
		t.Errorf("expected error to mention 'parsing API response', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getPacto listPactos error in all-namespaces mode
// ---------------------------------------------------------------------------

func TestK8s_getPacto_ListError_AllNamespaces(t *testing.T) {
	client := &mockK8sClient{listErr: fmt.Errorf("connection refused")}

	src := NewK8sSource(client, "", "pactos", "") // all-namespaces mode
	_, err := src.GetService(context.Background(), "my-svc")
	if err == nil {
		t.Fatal("expected error when listPactos fails in all-namespaces mode")
	}
	if !strings.Contains(err.Error(), "listing") {
		t.Errorf("expected error to mention 'listing', got: %v", err)
	}
}

func TestObservedRuntimeFromK8s(t *testing.T) {
	// nil input
	if got := observedRuntimeFromK8s(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}

	// non-nil input
	grace := 30
	hasPVC := true
	hasEmpty := false
	delay := 5
	obs := &k8sObservedRuntime{
		WorkloadKind:                   "Deployment",
		DeploymentStrategy:             "RollingUpdate",
		PodManagementPolicy:            "OrderedReady",
		TerminationGracePeriodSeconds:  &grace,
		ContainerImages:                []string{"img:v1"},
		HasPVC:                         &hasPVC,
		HasEmptyDir:                    &hasEmpty,
		HealthProbeInitialDelaySeconds: &delay,
	}
	got := observedRuntimeFromK8s(obs)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.WorkloadKind != "Deployment" {
		t.Errorf("expected Deployment, got %q", got.WorkloadKind)
	}
	if got.DeploymentStrategy != "RollingUpdate" {
		t.Errorf("expected RollingUpdate, got %q", got.DeploymentStrategy)
	}
	if got.TerminationGracePeriodSeconds == nil || *got.TerminationGracePeriodSeconds != 30 {
		t.Errorf("expected 30, got %v", got.TerminationGracePeriodSeconds)
	}
	if got.HasPVC == nil || !*got.HasPVC {
		t.Error("expected HasPVC=true")
	}
	if got.HealthProbeInitialDelay == nil || *got.HealthProbeInitialDelay != 5 {
		t.Errorf("expected 5, got %v", got.HealthProbeInitialDelay)
	}
}
