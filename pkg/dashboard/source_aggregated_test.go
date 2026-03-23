package dashboard

import (
	"context"
	"fmt"
	"testing"
)

type stubSource struct {
	name     string
	services []Service
	details  map[string]*ServiceDetails
	versions map[string][]Version
}

func (s *stubSource) ListServices(_ context.Context) ([]Service, error) {
	return s.services, nil
}

func (s *stubSource) GetService(_ context.Context, name string) (*ServiceDetails, error) {
	if d, ok := s.details[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("not found")
}

func (s *stubSource) GetVersions(_ context.Context, name string) ([]Version, error) {
	if v, ok := s.versions[name]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("no versions")
}

func (s *stubSource) GetDiff(_ context.Context, a, b Ref) (*DiffResult, error) {
	return &DiffResult{From: a, To: b, Classification: "NON_BREAKING"}, nil
}

func TestAggregatedSource_ListServices_GroupsByName(t *testing.T) {
	k8s := &stubSource{
		services: []Service{
			{Name: "order-service", Version: "1.4.0", Phase: PhaseHealthy, Source: "k8s"},
		},
	}
	oci := &stubSource{
		services: []Service{
			{Name: "order-service", Version: "1.4.0", Phase: PhaseUnknown, Source: "oci"},
			{Name: "payment-service", Version: "2.0.0", Phase: PhaseUnknown, Source: "oci"},
		},
	}
	local := &stubSource{
		services: []Service{
			{Name: "order-service", Version: "1.5.0-dev", Phase: PhaseInvalid, Source: "local"},
		},
	}

	agg := NewAggregatedSource(map[string]DataSource{
		"k8s":   k8s,
		"oci":   oci,
		"local": local,
	})

	services, err := agg.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(services) != 2 {
		t.Fatalf("expected 2 unique services, got %d", len(services))
	}

	// Find order-service
	var order *Service
	for i := range services {
		if services[i].Name == "order-service" {
			order = &services[i]
			break
		}
	}
	if order == nil {
		t.Fatal("order-service not found")
	}

	// Should have all 3 sources
	if len(order.Sources) != 3 {
		t.Errorf("expected 3 sources, got %d: %v", len(order.Sources), order.Sources)
	}

	// Phase should come from k8s (highest priority)
	if order.Phase != PhaseHealthy {
		t.Errorf("expected phase Healthy (from k8s), got %q", order.Phase)
	}

	// Version should come from k8s (highest priority for runtime)
	// Actually k8s is applied last, so it wins
	// But local is applied after OCI, and k8s after local
	// The order is: oci, local, k8s. k8s phase wins, local version wins.
	// Wait - let me re-read mergedSummary: it iterates oci->local->k8s, setting values.
	// So k8s version "1.4.0" would be set last if non-empty.
	// But for version: k8s sets version=1.4.0 (last) so it wins.
	// Actually the Source field is set to the last applied, which is "k8s".
	if order.Source != "k8s" {
		t.Errorf("expected primary source 'k8s', got %q", order.Source)
	}
}

func TestAggregatedSource_GetService_MergesDetails(t *testing.T) {
	k8s := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service: Service{Name: "svc", Version: "1.0.0", Phase: PhaseHealthy, Source: "k8s"},
				Runtime: &RuntimeInfo{Workload: "service", HealthInterface: "api", HealthPath: "/healthz"},
				Resources: &ResourcesInfo{
					ServiceExists: boolPtr(true),
				},
			},
		},
	}
	local := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:      Service{Name: "svc", Version: "1.1.0-dev", Phase: PhaseInvalid, Source: "local"},
				Interfaces:   []InterfaceInfo{{Name: "api", Type: "http", Visibility: "public"}},
				Dependencies: []DependencyInfo{{Ref: "oci://other", Required: true}},
			},
		},
	}

	agg := NewAggregatedSource(map[string]DataSource{
		"k8s":   k8s,
		"local": local,
	})

	details, err := agg.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// Phase from k8s
	if details.Phase != PhaseHealthy {
		t.Errorf("expected phase Healthy, got %q", details.Phase)
	}

	// Version from local (in-progress)
	if details.Version != "1.1.0-dev" {
		t.Errorf("expected version '1.1.0-dev', got %q", details.Version)
	}

	// Runtime from k8s
	if details.Runtime == nil {
		t.Fatal("expected runtime from k8s")
	}
	if details.Runtime.HealthPath != "/healthz" {
		t.Errorf("expected health path '/healthz', got %q", details.Runtime.HealthPath)
	}

	// Resources from k8s
	if details.Resources == nil || details.Resources.ServiceExists == nil || !*details.Resources.ServiceExists {
		t.Error("expected resources.serviceExists=true from k8s")
	}

	// Interfaces from local
	if len(details.Interfaces) != 1 || details.Interfaces[0].Name != "api" {
		t.Errorf("expected interfaces from local, got %v", details.Interfaces)
	}

	// Dependencies from local
	if len(details.Dependencies) != 1 {
		t.Errorf("expected 1 dependency from local, got %d", len(details.Dependencies))
	}

	// Sources list
	if len(details.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(details.Sources))
	}
}

func TestAggregatedSource_GetVersions_PrefersOCI(t *testing.T) {
	oci := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "1.0.0"}, {Version: "1.1.0"}, {Version: "1.2.0"}},
		},
	}
	local := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "1.3.0-dev"}},
		},
	}

	agg := NewAggregatedSource(map[string]DataSource{
		"oci":   oci,
		"local": local,
	})

	versions, err := agg.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	if len(versions) != 3 {
		t.Fatalf("expected 3 versions from OCI, got %d", len(versions))
	}
}

func TestAggregatedSource_ServiceNotFound(t *testing.T) {
	agg := NewAggregatedSource(map[string]DataSource{
		"local": &stubSource{details: map[string]*ServiceDetails{}},
	})

	_, err := agg.GetService(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestAggregatedSource_GetDiff_RoutesToOCI(t *testing.T) {
	oci := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", Version: "1.0.0"}},
		},
	}
	local := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", Version: "2.0.0-dev"}},
		},
	}

	agg := NewAggregatedSource(map[string]DataSource{
		"oci":   oci,
		"local": local,
	})

	a := Ref{Name: "svc", Version: "1.0.0"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	result, err := agg.GetDiff(context.Background(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING, got %q", result.Classification)
	}
}

func TestAggregatedSource_GetDiff_WithSourceHint(t *testing.T) {
	local := &stubSource{}
	agg := NewAggregatedSource(map[string]DataSource{
		"local": local,
	})

	a := Ref{Name: "svc", Version: "1.0.0", Source: "local"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	result, err := agg.GetDiff(context.Background(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestAggregatedSource_GetDiff_NoSourceAvailable(t *testing.T) {
	agg := NewAggregatedSource(map[string]DataSource{})

	a := Ref{Name: "svc", Version: "1.0.0"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	_, err := agg.GetDiff(context.Background(), a, b)
	if err == nil {
		t.Fatal("expected error when no source available")
	}
}

func TestMergeServiceDetails_OCICacheBaseline(t *testing.T) {
	sources := []ServiceSourceData{
		{
			SourceType: "oci",
			Service: &ServiceDetails{
				Service:       Service{Name: "svc", Version: "1.0.0", Owner: "team-a", Source: "oci"},
				ImageRef:      "ghcr.io/org/svc:1.0.0",
				ChartRef:      "oci://charts/svc",
				Interfaces:    []InterfaceInfo{{Name: "api", Type: "http"}},
				Configuration: &ConfigurationInfo{Schema: "config.json"},
				Dependencies:  []DependencyInfo{{Ref: "dep", Required: true}},
				Policy:        &PolicyInfo{Schema: "policy.json"},
				Runtime:       &RuntimeInfo{Workload: "service"},
			},
		},
	}

	merged := mergeServiceDetails(sources)
	if merged.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", merged.Version)
	}
	if merged.Owner != "team-a" {
		t.Errorf("expected owner 'team-a', got %q", merged.Owner)
	}
	if merged.ImageRef != "ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected imageRef, got %q", merged.ImageRef)
	}
	if merged.ChartRef != "oci://charts/svc" {
		t.Errorf("expected chartRef, got %q", merged.ChartRef)
	}
	if len(merged.Interfaces) != 1 {
		t.Errorf("expected 1 interface, got %d", len(merged.Interfaces))
	}
	if merged.Configuration == nil {
		t.Error("expected configuration from OCI")
	}
	if len(merged.Dependencies) != 1 {
		t.Errorf("expected 1 dep, got %d", len(merged.Dependencies))
	}
	if merged.Policy == nil {
		t.Error("expected policy from OCI")
	}
	if merged.Runtime == nil {
		t.Error("expected runtime from OCI")
	}
}

func TestMergeServiceDetails_Empty(t *testing.T) {
	result := mergeServiceDetails(nil)
	if result != nil {
		t.Error("expected nil for empty sources")
	}
}

func TestMergeServiceDetails_K8sOverridesAll(t *testing.T) {
	sources := []ServiceSourceData{
		{
			SourceType: "k8s",
			Service: &ServiceDetails{
				Service:       Service{Name: "svc", Phase: PhaseHealthy, Source: "k8s"},
				Runtime:       &RuntimeInfo{Workload: "service"},
				Resources:     &ResourcesInfo{ServiceExists: boolPtr(true)},
				Ports:         &PortsInfo{Expected: []int{8080}},
				Validation:    &ValidationInfo{Valid: true},
				Endpoints:     []EndpointStatus{{Interface: "api"}},
				Scaling:       &ScalingInfo{Replicas: intPtr(3)},
				Conditions:    []Condition{{Type: "Ready", Status: "True"}},
				Insights:      []Insight{{Severity: "info", Title: "ok"}},
				ChecksSummary: &ChecksSummary{Total: 5, Passed: 5},
			},
		},
		{
			SourceType: "oci",
			Service: &ServiceDetails{
				Service: Service{Name: "svc", Version: "1.0.0", Owner: "team", Source: "oci"},
			},
		},
	}

	merged := mergeServiceDetails(sources)
	if merged.Phase != PhaseHealthy {
		t.Errorf("expected phase Healthy from k8s, got %q", merged.Phase)
	}
	if merged.Runtime == nil {
		t.Error("expected runtime from k8s")
	}
	if merged.Resources == nil {
		t.Error("expected resources from k8s")
	}
	if merged.Ports == nil {
		t.Error("expected ports from k8s")
	}
	if merged.Validation == nil {
		t.Error("expected validation from k8s")
	}
	if len(merged.Endpoints) != 1 {
		t.Errorf("expected 1 endpoint from k8s, got %d", len(merged.Endpoints))
	}
	if merged.Scaling == nil {
		t.Error("expected scaling from k8s")
	}
	if len(merged.Conditions) != 1 {
		t.Errorf("expected 1 condition from k8s, got %d", len(merged.Conditions))
	}
	if len(merged.Insights) != 1 {
		t.Errorf("expected 1 insight from k8s, got %d", len(merged.Insights))
	}
	if merged.ChecksSummary == nil {
		t.Error("expected checksSummary from k8s")
	}
	// OCI baseline values should fill in
	if merged.Version != "1.0.0" {
		t.Errorf("expected version from oci, got %q", merged.Version)
	}
	if merged.Owner != "team" {
		t.Errorf("expected owner from oci, got %q", merged.Owner)
	}
}

func TestAggregatedSource_GetVersions_FallsBackToCache(t *testing.T) {
	// OCI has no versions, cache does.
	oci := &stubSource{
		versions: map[string][]Version{},
	}
	cache := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "1.0.0"}},
		},
	}

	agg := NewAggregatedSource(map[string]DataSource{
		"oci":   oci,
		"cache": cache,
	})

	versions, err := agg.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Version != "1.0.0" {
		t.Errorf("expected 1 version from cache, got %v", versions)
	}
}

func TestAggregatedSource_GetDiff_SourceHintMissing(t *testing.T) {
	// Source hint refers to a source that doesn't exist — should fall back.
	oci := &stubSource{}
	agg := NewAggregatedSource(map[string]DataSource{
		"oci": oci,
	})

	from := Ref{Name: "svc", Version: "1.0.0", Source: "nonexistent"}
	to := Ref{Name: "svc", Version: "2.0.0"}

	result, err := agg.GetDiff(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING, got %q", result.Classification)
	}
}

func TestAggregatedSource_GetDiff_FallbackToCacheThenLocal(t *testing.T) {
	// OCI fails, cache succeeds.
	ociErr := &failingSource{err: fmt.Errorf("oci error")}
	cache := &stubSource{}

	agg := NewAggregatedSource(map[string]DataSource{
		"oci":   ociErr,
		"cache": cache,
	})

	from := Ref{Name: "svc", Version: "1.0.0"}
	to := Ref{Name: "svc", Version: "2.0.0"}

	result, err := agg.GetDiff(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result from cache fallback")
	}
}

func TestAggregatedSource_GetDiff_AllSourcesFail(t *testing.T) {
	// All sources fail diff.
	agg := NewAggregatedSource(map[string]DataSource{
		"oci":   &failingSource{err: fmt.Errorf("oci fail")},
		"cache": &failingSource{err: fmt.Errorf("cache fail")},
		"local": &failingSource{err: fmt.Errorf("local fail")},
	})

	from := Ref{Name: "svc", Version: "1.0.0"}
	to := Ref{Name: "svc", Version: "2.0.0"}

	_, err := agg.GetDiff(context.Background(), from, to)
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
}

func TestMergedSummary_EmptyPhase(t *testing.T) {
	// When phase is empty string, it should not override default Unknown.
	entry := &aggregatedEntry{}
	entry.add("oci", &Service{Name: "svc", Version: "1.0.0", Phase: ""})

	merged := entry.mergedSummary("svc")
	if merged.Phase != PhaseUnknown {
		t.Errorf("expected PhaseUnknown, got %q", merged.Phase)
	}
}

func TestMergeServiceDetails_CacheBaseline(t *testing.T) {
	// Cache provides baseline when no other source sets values.
	sources := []ServiceSourceData{
		{
			SourceType: "cache",
			Service: &ServiceDetails{
				Service:       Service{Name: "svc", Version: "0.9.0", Owner: "team-b", Source: "cache"},
				ImageRef:      "ghcr.io/org/svc:0.9.0",
				ChartRef:      "oci://charts/svc-old",
				Interfaces:    []InterfaceInfo{{Name: "api", Type: "http"}},
				Configuration: &ConfigurationInfo{Schema: "cache-config.json"},
				Dependencies:  []DependencyInfo{{Ref: "dep-cache", Required: true}},
				Policy:        &PolicyInfo{Schema: "cache-policy.json"},
				Runtime:       &RuntimeInfo{Workload: "deployment"},
			},
		},
	}

	merged := mergeServiceDetails(sources)
	if merged.Version != "0.9.0" {
		t.Errorf("expected version from cache, got %q", merged.Version)
	}
	if merged.Owner != "team-b" {
		t.Errorf("expected owner from cache, got %q", merged.Owner)
	}
	if merged.ImageRef != "ghcr.io/org/svc:0.9.0" {
		t.Errorf("expected imageRef from cache, got %q", merged.ImageRef)
	}
	if merged.ChartRef != "oci://charts/svc-old" {
		t.Errorf("expected chartRef from cache, got %q", merged.ChartRef)
	}
	if len(merged.Interfaces) != 1 {
		t.Errorf("expected 1 interface from cache, got %d", len(merged.Interfaces))
	}
	if merged.Configuration == nil {
		t.Error("expected configuration from cache")
	}
	if len(merged.Dependencies) != 1 {
		t.Errorf("expected 1 dependency from cache, got %d", len(merged.Dependencies))
	}
	if merged.Policy == nil {
		t.Error("expected policy from cache")
	}
	if merged.Runtime == nil {
		t.Error("expected runtime from cache")
	}
}

func TestMergedSummary_WithOwner(t *testing.T) {
	entry := &aggregatedEntry{}
	entry.add("oci", &Service{Name: "svc", Version: "1.0.0", Owner: "team-a", Phase: PhaseUnknown})

	merged := entry.mergedSummary("svc")
	if merged.Owner != "team-a" {
		t.Errorf("expected owner 'team-a', got %q", merged.Owner)
	}
}

func TestMergeServiceDetails_K8sWithOCIBaseline(t *testing.T) {
	// k8s sets runtime state but no contract details.
	// OCI fills in interfaces, configuration, dependencies, policy.
	sources := []ServiceSourceData{
		{
			SourceType: "k8s",
			Service: &ServiceDetails{
				Service:   Service{Name: "svc", Phase: PhaseHealthy, Source: "k8s"},
				Runtime:   &RuntimeInfo{Workload: "service"},
				Resources: &ResourcesInfo{ServiceExists: boolPtr(true)},
			},
		},
		{
			SourceType: "oci",
			Service: &ServiceDetails{
				Service:       Service{Name: "svc", Version: "1.0.0", Owner: "team-a", Source: "oci"},
				ImageRef:      "ghcr.io/org/svc:1.0.0",
				ChartRef:      "oci://charts/svc",
				Interfaces:    []InterfaceInfo{{Name: "api", Type: "http"}},
				Configuration: &ConfigurationInfo{Schema: "config.json"},
				Dependencies:  []DependencyInfo{{Ref: "dep", Required: true}},
				Policy:        &PolicyInfo{Schema: "policy.json"},
			},
		},
	}

	merged := mergeServiceDetails(sources)
	// OCI baseline should fill in
	if len(merged.Interfaces) != 1 {
		t.Errorf("expected 1 interface from oci baseline, got %d", len(merged.Interfaces))
	}
	if merged.Configuration == nil {
		t.Error("expected configuration from oci baseline")
	}
	if len(merged.Dependencies) != 1 {
		t.Errorf("expected 1 dependency from oci baseline, got %d", len(merged.Dependencies))
	}
	if merged.Policy == nil {
		t.Error("expected policy from oci baseline")
	}
}

// failingSource is a DataSource that always returns errors.
type failingSource struct {
	err error
}

func (f *failingSource) ListServices(_ context.Context) ([]Service, error) {
	return nil, f.err
}

func (f *failingSource) GetService(_ context.Context, _ string) (*ServiceDetails, error) {
	return nil, f.err
}

func (f *failingSource) GetVersions(_ context.Context, _ string) ([]Version, error) {
	return nil, f.err
}

func (f *failingSource) GetDiff(_ context.Context, _, _ Ref) (*DiffResult, error) {
	return nil, f.err
}

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}

func TestMergeServiceDetails_CacheRuntimeFallback(t *testing.T) {
	// k8s has no Runtime, cache provides it as baseline.
	sources := []ServiceSourceData{
		{
			SourceType: "k8s",
			Service: &ServiceDetails{
				Service: Service{Name: "svc", Phase: PhaseHealthy, Source: "k8s"},
			},
		},
		{
			SourceType: "cache",
			Service: &ServiceDetails{
				Service: Service{Name: "svc", Version: "1.0.0", Source: "cache"},
				Runtime: &RuntimeInfo{Workload: "deployment"},
			},
		},
	}

	merged := mergeServiceDetails(sources)
	if merged.Runtime == nil {
		t.Fatal("expected runtime from cache baseline")
	}
	if merged.Runtime.Workload != "deployment" {
		t.Errorf("expected workload 'deployment', got %q", merged.Runtime.Workload)
	}
}

func TestAggregatedSource_GetVersions_FallsBackToLocal(t *testing.T) {
	// Only local has versions — oci and cache are not in the map, hitting the !ok branch.
	local := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0-dev"}},
		},
	}

	agg := NewAggregatedSource(map[string]DataSource{
		"local": local,
	})

	versions, err := agg.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Version != "2.0.0-dev" {
		t.Errorf("expected 1 version from local, got %v", versions)
	}
}

func TestAggregatedSource_GetVersions_AllSourcesFail(t *testing.T) {
	// All sources return errors — should get error.
	agg := NewAggregatedSource(map[string]DataSource{
		"oci":   &failingSource{err: fmt.Errorf("oci fail")},
		"cache": &failingSource{err: fmt.Errorf("cache fail")},
		"local": &failingSource{err: fmt.Errorf("local fail")},
		"k8s":   &failingSource{err: fmt.Errorf("k8s fail")},
	})

	_, err := agg.GetVersions(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error when all sources fail GetVersions")
	}
}

func TestSourcePriorityIndex_UnknownSource(t *testing.T) {
	idx := sourcePriorityIndex(runtimePriority, "unknown-source")
	if idx != len(runtimePriority) {
		t.Errorf("expected %d for unknown source, got %d", len(runtimePriority), idx)
	}
}

func TestUnionDependencies(t *testing.T) {
	sources := []ServiceSourceData{
		{
			SourceType: "local",
			Service: &ServiceDetails{
				Dependencies: []DependencyInfo{
					{Ref: "oci://ghcr.io/acme/auth:1.0.0", Required: true},
					{Ref: "oci://ghcr.io/acme/common:1.0.0", Required: false},
				},
			},
		},
		{
			SourceType: "k8s",
			Service: &ServiceDetails{
				Dependencies: []DependencyInfo{
					{Ref: "oci://ghcr.io/acme/auth:1.0.0", Required: true},  // duplicate
					{Ref: "oci://ghcr.io/acme/cache:2.0.0", Required: true}, // unique to k8s
				},
			},
		},
	}

	result := unionDependencies(sources)
	if len(result) != 3 {
		t.Fatalf("expected 3 unique deps, got %d: %v", len(result), result)
	}
	// local has higher content priority, so auth should come from local first.
	refs := make(map[string]bool)
	for _, d := range result {
		refs[d.Ref] = true
	}
	for _, expected := range []string{"oci://ghcr.io/acme/auth:1.0.0", "oci://ghcr.io/acme/common:1.0.0", "oci://ghcr.io/acme/cache:2.0.0"} {
		if !refs[expected] {
			t.Errorf("missing expected dep %q", expected)
		}
	}
}

func TestMergedSummary_OwnerOverride(t *testing.T) {
	entry := &aggregatedEntry{}
	entry.add("oci", &Service{Name: "svc", Version: "1.0.0", Owner: "oci-team"})
	entry.add("k8s", &Service{Name: "svc", Version: "1.0.0", Owner: "k8s-team"})

	merged := entry.mergedSummary("svc")
	// k8s has higher identity priority than oci, so its owner should win.
	if merged.Owner != "k8s-team" {
		t.Errorf("expected owner 'k8s-team', got %q", merged.Owner)
	}
}

func TestMergedSummary_VersionOverride(t *testing.T) {
	entry := &aggregatedEntry{}
	entry.add("oci", &Service{Name: "svc", Version: "1.0.0"})
	entry.add("local", &Service{Name: "svc", Version: "2.0.0-dev"})

	merged := entry.mergedSummary("svc")
	if merged.Version != "2.0.0-dev" {
		t.Errorf("expected version '2.0.0-dev' from local, got %q", merged.Version)
	}
	if merged.Source != "local" {
		t.Errorf("expected source 'local', got %q", merged.Source)
	}
}

func TestMergeServiceDetails_K8sConfigurationPolicyScalingInsightsChecks(t *testing.T) {
	sources := []ServiceSourceData{
		{
			SourceType: "k8s",
			Service: &ServiceDetails{
				Service:       Service{Name: "svc", Phase: PhaseHealthy, Source: "k8s"},
				Configuration: &ConfigurationInfo{Schema: "k8s-config.json"},
				Policy:        &PolicyInfo{Schema: "k8s-policy.json"},
				Scaling:       &ScalingInfo{Replicas: intPtr(2)},
				Insights:      []Insight{{Severity: "info", Title: "k8s insight"}},
				ChecksSummary: &ChecksSummary{Total: 3, Passed: 3},
				Resources:     &ResourcesInfo{ServiceExists: boolPtr(true)},
			},
		},
	}

	merged := mergeServiceDetails(sources)
	if merged.Scaling == nil || *merged.Scaling.Replicas != 2 {
		t.Error("expected scaling from k8s")
	}
	if len(merged.Insights) != 1 || merged.Insights[0].Title != "k8s insight" {
		t.Error("expected insights from k8s")
	}
	if merged.ChecksSummary == nil || merged.ChecksSummary.Passed != 3 {
		t.Error("expected checksSummary from k8s")
	}
	if merged.Resources == nil {
		t.Error("expected resources from k8s")
	}
}

func TestMergeServiceDetails_LocalConfigurationAndPolicy(t *testing.T) {
	sources := []ServiceSourceData{
		{
			SourceType: "local",
			Service: &ServiceDetails{
				Service:       Service{Name: "svc", Version: "1.0.0", Source: "local"},
				Configuration: &ConfigurationInfo{Schema: "local.json"},
				Policy:        &PolicyInfo{Schema: "local-policy.json"},
			},
		},
	}
	merged := mergeServiceDetails(sources)
	if merged.Configuration == nil || merged.Configuration.Schema != "local.json" {
		t.Error("expected configuration from local")
	}
	if merged.Policy == nil || merged.Policy.Schema != "local-policy.json" {
		t.Error("expected policy from local")
	}
}
