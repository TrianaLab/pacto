package dashboard

import (
	"context"
	"fmt"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
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

func TestResolvedSource_ListServices_GroupsByName(t *testing.T) {
	k8s := &stubSource{
		services: []Service{
			{Name: "order-service", Version: "1.4.0", ContractStatus: StatusCompliant, Source: "k8s"},
		},
	}
	oci := &stubSource{
		services: []Service{
			{Name: "order-service", Version: "1.4.0", ContractStatus: StatusUnknown, Source: "oci"},
			{Name: "payment-service", Version: "2.0.0", ContractStatus: StatusUnknown, Source: "oci"},
		},
	}
	local := &stubSource{
		services: []Service{
			{Name: "order-service", Version: "1.5.0-dev", ContractStatus: StatusNonCompliant, Source: "local"},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s":   k8s,
		"oci":   oci,
		"local": local,
	})

	services, err := resolved.ListServices(context.Background())
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

	// ContractStatus should come from k8s (runtime)
	if order.ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant (from k8s), got %q", order.ContractStatus)
	}

	// Version should come from k8s (operator is authoritative for deployed version)
	if order.Version != "1.4.0" {
		t.Errorf("expected version '1.4.0' from k8s operator, got %q", order.Version)
	}
}

func TestResolvedSource_GetService_ContractPlusRuntime(t *testing.T) {
	k8s := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service: Service{Name: "svc", Version: "1.0.0", ContractStatus: StatusCompliant, Source: "k8s"},
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
				Service:      Service{Name: "svc", Version: "1.1.0-dev", ContractStatus: StatusNonCompliant, Source: "local"},
				Interfaces:   []InterfaceInfo{{Name: "api", Type: "http", Visibility: "public"}},
				Dependencies: []DependencyInfo{{Ref: "oci://other", Required: true}},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s":   k8s,
		"local": local,
	})

	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// ContractStatus from k8s (runtime enrichment)
	if details.ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant, got %q", details.ContractStatus)
	}

	// Version from k8s (operator is authoritative for deployed version)
	if details.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0' from k8s operator, got %q", details.Version)
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

	// Interfaces from local contract (NOT overridden by k8s)
	if len(details.Interfaces) != 1 || details.Interfaces[0].Name != "api" {
		t.Errorf("expected interfaces from local, got %v", details.Interfaces)
	}

	// Dependencies from local contract (NOT mixed with other sources)
	if len(details.Dependencies) != 1 {
		t.Errorf("expected 1 dependency from local, got %d", len(details.Dependencies))
	}

	// Sources list
	if len(details.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(details.Sources))
	}
}

func TestResolvedSource_GetService_LocalWinsOverOCI(t *testing.T) {
	oci := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:    Service{Name: "svc", Version: "1.0.0", Source: "oci"},
				Interfaces: []InterfaceInfo{{Name: "api", Type: "http", Endpoints: []InterfaceEndpoint{{Method: "GET", Path: "/old"}}}},
			},
		},
	}
	local := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:    Service{Name: "svc", Version: "1.1.0-dev", Source: "local"},
				Interfaces: []InterfaceInfo{{Name: "api", Type: "http", Endpoints: []InterfaceEndpoint{{Method: "GET", Path: "/new"}}}},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"oci":   oci,
		"local": local,
	})

	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// Local wins: version and interfaces from local, NOT merged
	if details.Version != "1.1.0-dev" {
		t.Errorf("expected version from local, got %q", details.Version)
	}
	if len(details.Interfaces) != 1 || details.Interfaces[0].Endpoints[0].Path != "/new" {
		t.Error("expected interfaces from local, not merged with OCI")
	}
}

func TestResolvedSource_GetService_K8sOnly(t *testing.T) {
	k8s := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:   Service{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"},
				Runtime:   &RuntimeInfo{Workload: "service"},
				Resources: &ResourcesInfo{ServiceExists: boolPtr(true)},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s": k8s,
	})

	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// K8s-only service: no contract, just runtime
	if details.ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant, got %q", details.ContractStatus)
	}
	if details.Runtime == nil {
		t.Error("expected runtime from k8s")
	}
}

func TestResolvedSource_GetService_NotFound(t *testing.T) {
	resolved := BuildResolvedSource(map[string]DataSource{
		"local": &stubSource{details: map[string]*ServiceDetails{}},
	})

	_, err := resolved.GetService(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestResolvedSource_GetVersions_MergedAndLabeled(t *testing.T) {
	k8s := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "1.0.0", ContractHash: "abc123"}},
		},
	}
	oci := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "1.0.0", Ref: "ghcr.io/org/svc:1.0.0"}, {Version: "1.1.0", Ref: "ghcr.io/org/svc:1.1.0"}},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s": k8s,
		"oci": oci,
	})

	versions, err := resolved.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// Should have 2 versions: 1.0.0 (from k8s, higher priority) and 1.1.0 (from oci)
	if len(versions) != 2 {
		t.Fatalf("expected 2 merged versions, got %d", len(versions))
	}

	// 1.1.0 should sort first (descending)
	if versions[0].Version != "1.1.0" {
		t.Errorf("expected 1.1.0 first, got %q", versions[0].Version)
	}
	if versions[0].Source != "oci" {
		t.Errorf("expected source label 'oci', got %q", versions[0].Source)
	}

	// 1.0.0 should come from k8s (first in priority order, dedup)
	if versions[1].Version != "1.0.0" {
		t.Errorf("expected 1.0.0 second, got %q", versions[1].Version)
	}
	if versions[1].Source != "k8s" {
		t.Errorf("expected source label 'k8s', got %q", versions[1].Source)
	}
	// k8s version should have the hash
	if versions[1].ContractHash != "abc123" {
		t.Errorf("expected contractHash from k8s, got %q", versions[1].ContractHash)
	}
}

func TestResolvedSource_GetVersions_FallsBackToLocal(t *testing.T) {
	local := &stubSource{
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0-dev"}},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"local": local,
	})

	versions, err := resolved.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Version != "2.0.0-dev" {
		t.Errorf("expected 1 version from local, got %v", versions)
	}
	if versions[0].Source != "local" {
		t.Errorf("expected source label 'local', got %q", versions[0].Source)
	}
}

func TestResolvedSource_GetVersions_NoCacheAsPublicSource(t *testing.T) {
	// Verify that cache is NOT a public source in the resolution pipeline.
	// OCI enrichment from cache happens internally inside OCISource, not
	// via the resolver merging a separate "cache" source.
	resolved := BuildResolvedSource(map[string]DataSource{
		"oci": &stubSource{
			versions: map[string][]Version{
				"svc": {
					{Version: "2.0.0", Ref: "ghcr.io/org/svc:2.0.0"},
					{Version: "1.0.0", Ref: "ghcr.io/org/svc:1.0.0"},
				},
			},
		},
	})

	versions, err := resolved.GetVersions(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	// Source label should always be "oci" — cache is internal to OCI.
	for _, v := range versions {
		if v.Source != "oci" {
			t.Errorf("expected source 'oci', got %q for version %s", v.Source, v.Version)
		}
	}
}

func TestResolvedSource_GetVersions_AllSourcesFail(t *testing.T) {
	resolved := BuildResolvedSource(map[string]DataSource{
		"oci":   &failingSource{err: fmt.Errorf("oci fail")},
		"local": &failingSource{err: fmt.Errorf("local fail")},
		"k8s":   &failingSource{err: fmt.Errorf("k8s fail")},
	})

	_, err := resolved.GetVersions(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error when all sources fail GetVersions")
	}
}

func TestResolvedSource_GetDiff_RoutesToOCI(t *testing.T) {
	oci := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", Version: "1.0.0"}},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"oci": oci,
	})

	a := Ref{Name: "svc", Version: "1.0.0"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	result, err := resolved.GetDiff(context.Background(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING, got %q", result.Classification)
	}
}

func TestResolvedSource_GetDiff_WithSourceHint(t *testing.T) {
	local := &stubSource{}
	resolved := BuildResolvedSource(map[string]DataSource{
		"local": local,
	})

	a := Ref{Name: "svc", Version: "1.0.0", Source: "local"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	result, err := resolved.GetDiff(context.Background(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestResolvedSource_GetDiff_NoSourceAvailable(t *testing.T) {
	resolved := BuildResolvedSource(map[string]DataSource{})

	a := Ref{Name: "svc", Version: "1.0.0"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	_, err := resolved.GetDiff(context.Background(), a, b)
	if err == nil {
		t.Fatal("expected error when no source available")
	}
}

func TestResolvedSource_GetDiff_K8sCannotDiff(t *testing.T) {
	// k8s source cannot provide diffs — error message should be clear
	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s": &failingSource{err: fmt.Errorf("diff not supported")},
	})

	a := Ref{Name: "svc", Version: "1.0.0"}
	b := Ref{Name: "svc", Version: "2.0.0"}

	_, err := resolved.GetDiff(context.Background(), a, b)
	if err == nil {
		t.Fatal("expected error — k8s cannot diff")
	}
}

func TestResolvedSource_RuntimeNeverOverridesContract(t *testing.T) {
	// k8s has interfaces and config (sparse metadata), local has full contract data.
	// Runtime enrichment must NOT override contract definitions (interfaces,
	// configuration, dependencies) but DOES override Version and Owner because
	// the operator is the authoritative source of the deployed version.
	k8s := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:        Service{Name: "svc", Version: "1.0.0", Owner: contract.NewOwnerFromString("k8s-team"), ContractStatus: StatusCompliant, Source: "k8s"},
				Interfaces:     []InterfaceInfo{{Name: "api", Type: "http"}},
				Configurations: []ConfigurationInfo{{HasSchema: true, Ref: "oci://config"}},
				Dependencies:   []DependencyInfo{{Ref: "oci://auth:1.0.0", Required: true}},
				Runtime:        &RuntimeInfo{Workload: "service"},
			},
		},
	}
	local := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:        Service{Name: "svc", Version: "1.1.0-dev", Owner: contract.NewOwnerFromString("local-team"), ContractStatus: StatusNonCompliant, Source: "local"},
				Interfaces:     []InterfaceInfo{{Name: "api", Type: "http", Endpoints: []InterfaceEndpoint{{Method: "GET", Path: "/v2"}}}},
				Configurations: []ConfigurationInfo{{HasSchema: true, Schema: "config.json", Values: []ConfigValue{{Key: "port", Value: "8080"}}}},
				Dependencies:   []DependencyInfo{{Ref: "oci://auth:2.0.0", Required: true}},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s":   k8s,
		"local": local,
	})

	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// Version and Owner come from k8s (operator is authoritative for deployed state):
	if details.Version != "1.0.0" {
		t.Errorf("expected version from k8s operator, got %q", details.Version)
	}
	if details.Owner.DisplayString() != "k8s-team" {
		t.Errorf("expected owner from k8s operator, got %q", details.Owner.DisplayString())
	}

	// Contract definitions from local (never overridden):
	if len(details.Interfaces) != 1 || details.Interfaces[0].Endpoints[0].Path != "/v2" {
		t.Error("expected interfaces from local contract")
	}
	if len(details.Configurations) != 1 || details.Configurations[0].Schema != "config.json" {
		t.Error("expected configuration from local contract")
	}
	if len(details.Dependencies) != 1 || details.Dependencies[0].Ref != "oci://auth:2.0.0" {
		t.Error("expected dependencies from local contract")
	}

	// Runtime fields from k8s:
	if details.ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant from k8s runtime, got %q", details.ContractStatus)
	}
	if details.Runtime == nil || details.Runtime.Workload != "service" {
		t.Error("expected runtime from k8s")
	}
}

func TestResolvedSource_NoContractMerging(t *testing.T) {
	// Verify that contract content is NEVER composed from multiple sources.
	// OCI has interfaces that local doesn't — they should NOT be appended.
	oci := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:    Service{Name: "svc", Version: "1.0.0", Source: "oci"},
				Interfaces: []InterfaceInfo{{Name: "api"}, {Name: "admin"}},
				Configurations: []ConfigurationInfo{{
					Schema: "schema.json",
					Values: []ConfigValue{{Key: "port", Value: "8080"}},
				}},
			},
		},
	}
	local := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:    Service{Name: "svc", Version: "1.1.0-dev", Source: "local"},
				Interfaces: []InterfaceInfo{{Name: "api"}},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"oci":   oci,
		"local": local,
	})

	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// Local wins — only 1 interface, NOT merged with OCI's 2
	if len(details.Interfaces) != 1 {
		t.Errorf("expected 1 interface from local (no merging), got %d", len(details.Interfaces))
	}

	// Local has no configuration — it should be empty, NOT filled from OCI
	if len(details.Configurations) != 0 {
		t.Error("expected empty configurations — local contract has none, should NOT fill from OCI")
	}
}

func TestResolvedSource_K8sPinnedVersionOverridesOCILatest(t *testing.T) {
	// Bug scenario: service pinned to 1.2.0 via operator, OCI has 2.0.0 as latest.
	// The dashboard must show current=1.2.0, not 2.0.0.
	k8s := &stubSource{
		services: []Service{
			{Name: "payments", Version: "1.2.0", ContractStatus: StatusCompliant, Source: "k8s"},
		},
		details: map[string]*ServiceDetails{
			"payments": {
				Service:       Service{Name: "payments", Version: "1.2.0", Owner: contract.NewOwnerFromString("billing"), ContractStatus: StatusCompliant, Source: "k8s"},
				ResolvedRef:   "ghcr.io/org/payments:1.2.0",
				VersionPolicy: VersionPolicyPinnedTag,
			},
		},
	}
	ociSrc := &stubSource{
		services: []Service{
			{Name: "payments", Version: "2.0.0", Source: "oci"},
		},
		details: map[string]*ServiceDetails{
			"payments": {
				Service:    Service{Name: "payments", Version: "2.0.0", Owner: contract.NewOwnerFromString("billing"), Source: "oci"},
				Interfaces: []InterfaceInfo{{Name: "api", Type: "http"}},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s": k8s,
		"oci": ociSrc,
	})

	// GetService: k8s version must win.
	details, err := resolved.GetService(context.Background(), "payments")
	if err != nil {
		t.Fatal(err)
	}
	if details.Version != "1.2.0" {
		t.Errorf("GetService: expected version=1.2.0 from k8s, got %q", details.Version)
	}
	if details.VersionPolicy != VersionPolicyPinnedTag {
		t.Errorf("GetService: expected versionPolicy=%q, got %q", VersionPolicyPinnedTag, details.VersionPolicy)
	}
	// Contract content (interfaces) still from OCI.
	if len(details.Interfaces) != 1 || details.Interfaces[0].Name != "api" {
		t.Error("GetService: expected interfaces from OCI contract")
	}

	// ListServices: k8s version must win.
	services, err := resolved.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("ListServices: expected 1 service, got %d", len(services))
	}
	if services[0].Version != "1.2.0" {
		t.Errorf("ListServices: expected version=1.2.0 from k8s, got %q", services[0].Version)
	}
}

func TestResolvedSource_K8sVersionChangeFromLatestToPinned(t *testing.T) {
	// Scenario: service was on 2.0.0 (latest), then pinned back to 1.2.0.
	// After the operator updates, dashboard must reflect the new version.
	k8s := &stubSource{
		services: []Service{
			{Name: "svc", Version: "1.2.0", ContractStatus: StatusCompliant, Source: "k8s"},
		},
		details: map[string]*ServiceDetails{
			"svc": {
				Service: Service{Name: "svc", Version: "1.2.0", ContractStatus: StatusCompliant, Source: "k8s"},
			},
		},
	}
	ociSrc := &stubSource{
		services: []Service{
			{Name: "svc", Version: "2.0.0", Source: "oci"},
		},
		details: map[string]*ServiceDetails{
			"svc": {
				Service: Service{Name: "svc", Version: "2.0.0", Source: "oci"},
			},
		},
		versions: map[string][]Version{
			"svc": {{Version: "2.0.0"}, {Version: "1.2.0"}, {Version: "1.0.0"}},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s": k8s,
		"oci": ociSrc,
	})

	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}

	// Current version from k8s, not OCI latest.
	if details.Version != "1.2.0" {
		t.Errorf("expected version=1.2.0 from k8s, got %q", details.Version)
	}
}

func TestResolvedSource_NoK8s_VersionFromContract(t *testing.T) {
	// Without k8s, Version comes from the contract source (no regression).
	ociSrc := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service: Service{Name: "svc", Version: "2.0.0", Source: "oci"},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"oci": ociSrc,
	})

	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Version != "2.0.0" {
		t.Errorf("expected version=2.0.0 from OCI (no k8s), got %q", details.Version)
	}
}

func TestBuildResolvedSource_ContractPriority(t *testing.T) {
	sources := map[string]DataSource{
		"local": &stubSource{},
		"oci":   &stubSource{},
		"k8s":   &stubSource{},
	}

	resolved := BuildResolvedSource(sources)

	// Should have local first, then oci in contract sources
	if len(resolved.contract) != 2 {
		t.Fatalf("expected 2 contract sources, got %d", len(resolved.contract))
	}
	if resolved.contract[0].name != "local" {
		t.Errorf("expected local first, got %q", resolved.contract[0].name)
	}
	if resolved.contract[1].name != "oci" {
		t.Errorf("expected oci second, got %q", resolved.contract[1].name)
	}

	// Should have k8s as runtime
	if resolved.runtime == nil {
		t.Error("expected k8s runtime source")
	}
}

func TestIsHigherContractPriority(t *testing.T) {
	if !isHigherContractPriority("local", "oci") {
		t.Error("local should be higher priority than oci")
	}
	if isHigherContractPriority("oci", "local") {
		t.Error("oci should NOT be higher priority than local")
	}
	if isHigherContractPriority("k8s", "local") {
		t.Error("k8s is not a contract source, should not be higher priority")
	}
	// Unknown current source: new known source wins.
	if !isHigherContractPriority("local", "unknown") {
		t.Error("local should be higher priority than unknown")
	}
}

func TestResolvedSource_ListServices_K8sOnlySource(t *testing.T) {
	// When only k8s reports a service (no contract source), Source should be "k8s".
	k8s := &stubSource{
		services: []Service{{Name: "runtime-svc", ContractStatus: StatusCompliant, Source: "k8s"}},
	}
	resolved := BuildResolvedSource(map[string]DataSource{"k8s": k8s})

	services, err := resolved.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Source != "k8s" {
		t.Errorf("expected source 'k8s' for k8s-only service, got %q", services[0].Source)
	}
	if services[0].ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant, got %q", services[0].ContractStatus)
	}
}

func TestResolvedSource_ListServices_K8sOwnerOverride(t *testing.T) {
	// When k8s provides an owner, it should override the contract source's owner.
	k8s := &stubSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Owner: contract.NewOwnerFromString("platform-team"), ContractStatus: StatusCompliant, Source: "k8s"}},
	}
	oci := &stubSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Owner: contract.NewOwnerFromString("dev-team"), Source: "oci"}},
	}
	resolved := BuildResolvedSource(map[string]DataSource{"k8s": k8s, "oci": oci})

	services, err := resolved.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Owner.DisplayString() != "platform-team" {
		t.Errorf("expected owner 'platform-team' from k8s, got %q", services[0].Owner.DisplayString())
	}
}

func TestResolvedSource_ListServices_SourceError(t *testing.T) {
	// A failing source should be skipped, not block other sources.
	local := &stubSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Source: "local"}},
	}
	resolved := BuildResolvedSource(map[string]DataSource{
		"local": local,
		"k8s":   &failingSource{err: fmt.Errorf("k8s unreachable")},
	})

	services, err := resolved.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Name != "svc" {
		t.Errorf("expected 1 service from local despite k8s failure, got %v", services)
	}
}

func TestResolvedSource_GetAggregated_WithRuntime(t *testing.T) {
	k8s := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service: Service{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"},
				Runtime: &RuntimeInfo{Workload: "service"},
			},
		},
	}
	local := &stubSource{
		details: map[string]*ServiceDetails{
			"svc": {
				Service:    Service{Name: "svc", Version: "1.0.0", Source: "local"},
				Interfaces: []InterfaceInfo{{Name: "api"}},
			},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s":   k8s,
		"local": local,
	})

	agg, err := resolved.GetAggregated(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if agg.Name != "svc" {
		t.Errorf("expected name 'svc', got %q", agg.Name)
	}
	// Should have per-source entries for both local and k8s.
	if len(agg.Sources) != 2 {
		t.Fatalf("expected 2 source entries, got %d", len(agg.Sources))
	}
}

func TestResolvedSource_GetAggregated_NotFound(t *testing.T) {
	resolved := BuildResolvedSource(map[string]DataSource{
		"local": &stubSource{details: map[string]*ServiceDetails{}},
	})

	_, err := resolved.GetAggregated(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func newFullRuntime() *ServiceDetails {
	return &ServiceDetails{
		Service:          Service{Name: "svc", ContractStatus: StatusCompliant},
		Runtime:          &RuntimeInfo{Workload: "service"},
		Resources:        &ResourcesInfo{ServiceExists: boolPtr(true)},
		Ports:            &PortsInfo{Expected: []int{8080}},
		Validation:       &ValidationInfo{Valid: true},
		Endpoints:        []EndpointStatus{{Interface: "api", URL: "http://svc:8080"}},
		Scaling:          &ScalingInfo{Replicas: intPtr(3)},
		Conditions:       []Condition{{Type: "Ready", Status: "True"}},
		Insights:         []Insight{{Severity: "info", Title: "ok"}},
		ChecksSummary:    &ChecksSummary{Passed: 5, Total: 5},
		ObservedRuntime:  &ObservedRuntime{WorkloadKind: "Deployment"},
		RuntimeDiff:      []RuntimeDiffRow{{Field: "image", DeclaredValue: "img:1", ObservedValue: "img:2"}},
		Namespace:        "prod",
		ResolvedRef:      "ghcr.io/org/svc:1.0.0",
		CurrentRevision:  "rev-1",
		LastReconciledAt: "2025-01-01T00:00:00Z",
		Compliance:       &ComplianceInfo{Status: "compliant"},
	}
}

func TestEnrichWithRuntime_StructFields(t *testing.T) {
	svcDetails := &ServiceDetails{
		Service: Service{Name: "svc", Version: "1.0.0", Owner: contract.NewOwnerFromString("team-a")},
	}
	enrichWithRuntime(svcDetails, newFullRuntime())

	if svcDetails.ContractStatus != StatusCompliant {
		t.Error("expected contract status from runtime")
	}
	if svcDetails.Runtime == nil {
		t.Error("expected runtime")
	}
	if svcDetails.Resources == nil {
		t.Error("expected resources")
	}
	if svcDetails.Ports == nil {
		t.Error("expected ports")
	}
	if svcDetails.Validation == nil {
		t.Error("expected validation")
	}
	if svcDetails.Scaling == nil {
		t.Error("expected scaling")
	}
	if svcDetails.ChecksSummary == nil {
		t.Error("expected checks summary")
	}
	if svcDetails.ObservedRuntime == nil {
		t.Error("expected observed runtime")
	}
	if svcDetails.Compliance == nil {
		t.Error("expected compliance")
	}
	// Contract fields must NOT be overridden.
	if svcDetails.Version != "1.0.0" {
		t.Errorf("contract version overridden: %q", svcDetails.Version)
	}
	if svcDetails.Owner.DisplayString() != "team-a" {
		t.Errorf("contract owner overridden: %q", svcDetails.Owner.DisplayString())
	}
}

func TestResolvedSource_HasSource(t *testing.T) {
	resolved := BuildResolvedSource(map[string]DataSource{
		"local": &stubSource{},
		"k8s":   &stubSource{},
	})

	if !resolved.HasSource("local") {
		t.Error("expected HasSource('local') = true")
	}
	if !resolved.HasSource("k8s") {
		t.Error("expected HasSource('k8s') = true")
	}
	if resolved.HasSource("oci") {
		t.Error("expected HasSource('oci') = false")
	}
}

func TestResolvedSource_AddContractSource(t *testing.T) {
	local := &stubSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Source: "local"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0", Source: "local"}}},
	}
	resolved := BuildResolvedSource(map[string]DataSource{"local": local})

	// Initially no OCI source.
	if resolved.HasSource("oci") {
		t.Fatal("expected no oci source initially")
	}

	// Add OCI as a contract source.
	oci := &stubSource{
		services: []Service{{Name: "remote-svc", Version: "2.0.0", Source: "oci"}},
		details:  map[string]*ServiceDetails{"remote-svc": {Service: Service{Name: "remote-svc", Version: "2.0.0", Source: "oci"}}},
	}
	resolved.AddContractSource("oci", oci)

	if !resolved.HasSource("oci") {
		t.Error("expected HasSource('oci') = true after AddContractSource")
	}

	// New source should participate in ListServices.
	services, err := resolved.ListServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services after adding OCI source, got %d", len(services))
	}

	// New source should participate in GetService.
	details, err := resolved.GetService(context.Background(), "remote-svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "remote-svc" {
		t.Errorf("expected name 'remote-svc', got %q", details.Name)
	}
}

func TestResolvedSource_AddSource(t *testing.T) {
	local := &stubSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Source: "local"}},
		details:  map[string]*ServiceDetails{"svc": {Service: Service{Name: "svc", Version: "1.0.0", Source: "local"}}},
		versions: map[string][]Version{"svc": {{Version: "1.0.0"}}},
	}
	resolved := BuildResolvedSource(map[string]DataSource{"local": local})

	// Add an auxiliary source (non-contract, for version/diff lookups only).
	aux := &stubSource{
		versions: map[string][]Version{"svc": {{Version: "0.9.0"}, {Version: "1.0.0"}}},
	}
	resolved.AddSource("aux", aux)

	if !resolved.HasSource("aux") {
		t.Error("expected HasSource('aux') = true after AddSource")
	}

	// GetSource should return the aux source.
	if resolved.GetSource("aux") == nil {
		t.Error("expected GetSource('aux') to return non-nil")
	}
	if resolved.GetSource("nonexistent") != nil {
		t.Error("expected GetSource('nonexistent') to return nil")
	}
}

func TestResolvedSource_SetRuntimeSource(t *testing.T) {
	k8sOld := &stubSource{
		services: []Service{{Name: "svc", ContractStatus: StatusWarning, Source: "k8s"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", ContractStatus: StatusWarning, Source: "k8s"}},
		},
	}
	local := &stubSource{
		services: []Service{{Name: "svc", Version: "1.0.0", Source: "local"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", Version: "1.0.0", Source: "local"}},
		},
	}

	resolved := BuildResolvedSource(map[string]DataSource{
		"k8s":   k8sOld,
		"local": local,
	})

	// Initially, contract status from old k8s (Warning)
	details, err := resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.ContractStatus != StatusWarning {
		t.Errorf("expected Warning from old k8s, got %q", details.ContractStatus)
	}

	// Swap to a new k8s source with Compliant status
	k8sNew := &stubSource{
		services: []Service{{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"}},
		},
	}
	resolved.SetRuntimeSource(k8sNew)

	// Now contract status should come from new k8s (Compliant)
	details, err = resolved.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant from new k8s after SetRuntimeSource, got %q", details.ContractStatus)
	}

	// Verify k8s is in the all-sources map
	if !resolved.HasSource("k8s") {
		t.Error("expected HasSource('k8s') = true after SetRuntimeSource")
	}
}

func TestResolvedSource_SetRuntimeSource_Nil(t *testing.T) {
	k8s := &stubSource{
		services: []Service{{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"}},
		details: map[string]*ServiceDetails{
			"svc": {Service: Service{Name: "svc", ContractStatus: StatusCompliant, Source: "k8s"}},
		},
	}
	resolved := BuildResolvedSource(map[string]DataSource{"k8s": k8s})

	// Remove k8s by setting nil
	resolved.SetRuntimeSource(nil)

	if resolved.HasSource("k8s") {
		t.Error("expected HasSource('k8s') = false after SetRuntimeSource(nil)")
	}

	// Should return error since no source has the service
	_, err := resolved.GetService(context.Background(), "svc")
	if err == nil {
		t.Error("expected error after removing k8s source")
	}
}

func TestEnrichWithRuntime_SlicesAndMetadata(t *testing.T) {
	contract := &ServiceDetails{
		Service: Service{Name: "svc", Version: "1.0.0"},
	}
	enrichWithRuntime(contract, newFullRuntime())

	if len(contract.Endpoints) != 1 {
		t.Error("expected endpoints")
	}
	if len(contract.Conditions) != 1 {
		t.Error("expected conditions")
	}
	if len(contract.Insights) != 1 {
		t.Error("expected insights")
	}
	if len(contract.RuntimeDiff) != 1 {
		t.Error("expected runtime diff")
	}
	if contract.Namespace != "prod" {
		t.Error("expected namespace")
	}
	if contract.ResolvedRef != "ghcr.io/org/svc:1.0.0" {
		t.Error("expected resolved ref")
	}
	if contract.CurrentRevision != "rev-1" {
		t.Error("expected current revision")
	}
	if contract.LastReconciledAt != "2025-01-01T00:00:00Z" {
		t.Error("expected last reconciled at")
	}
}
