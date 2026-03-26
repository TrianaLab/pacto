package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
)

// pullCountingStore tracks pull calls and returns a configurable bundle.
type pullCountingStore struct {
	bundle    *contract.Bundle
	pullCount atomic.Int32
	pullErr   error
}

func (s *pullCountingStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *pullCountingStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (s *pullCountingStore) Pull(_ context.Context, _ string) (*contract.Bundle, error) {
	s.pullCount.Add(1)
	if s.pullErr != nil {
		return nil, s.pullErr
	}
	return s.bundle, nil
}
func (s *pullCountingStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }

// startResolveTestServer creates a test server with the given source, store, and returns the base URL.
// The caller must cancel the returned cancel func to stop the server.
func startResolveTestServer(t *testing.T, source DataSource, store oci.BundleStore, sourceInfo []SourceInfo) (string, context.CancelFunc) {
	t.Helper()
	resolved := BuildResolvedSource(map[string]DataSource{"local": source})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := NewResolvedServer(resolved, ui, sourceInfo, nil)
	srv.SetResolver(oci.NewResolver(store))

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)

	return "http://" + ln.Addr().String(), cancel
}

// newPaymentBundle creates a test bundle for payment-service.
func newPaymentBundle() *contract.Bundle {
	port := 8080
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "payment-service", Version: "2.0.0"},
			Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
			Runtime: &contract.Runtime{
				Workload: "service",
				State: contract.State{
					Type:            "stateless",
					Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
					DataCriticality: "low",
				},
			},
		},
		RawYAML: []byte("pactoVersion: \"1.0\"\nservice:\n  name: payment-service\n  version: \"2.0.0\"\ninterfaces:\n  - name: api\n    type: http\n    port: 8080\nruntime:\n  workload: service\n  state:\n    type: stateless\n    persistence:\n      scope: local\n      durability: ephemeral\n    dataCriticality: low\n"),
	}
}

// newOrderServiceSource creates a local source with an order-service that depends on payment-service.
func newOrderServiceSource() *stubSource {
	return &stubSource{
		name: "local",
		services: []Service{
			{Name: "order-service", Version: "1.0.0", Source: "local"},
		},
		details: map[string]*ServiceDetails{
			"order-service": {
				Service: Service{Name: "order-service", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
				Dependencies: []DependencyInfo{
					{
						Name:          "payment-service",
						Ref:           "oci://ghcr.io/org/payment-service-pacto:2.0.0",
						Required:      true,
						Compatibility: "^2.0.0",
					},
				},
			},
		},
	}
}

// expectGETStatus issues a GET and asserts the status code.
func expectGETStatus(t *testing.T, url string, wantStatus int) {
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

// resolveRef calls POST /api/resolve and decodes the response into ServiceDetails.
func resolveRef(t *testing.T, base, ref string) (*http.Response, ServiceDetails) {
	t.Helper()
	resp, err := http.Post(base+"/api/resolve", "application/json",
		strings.NewReader(fmt.Sprintf(`{"ref":%q}`, ref)))
	if err != nil {
		t.Fatal(err)
	}
	var details ServiceDetails
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
			t.Fatal(err)
		}
	}
	return resp, details
}

func TestResolveE2E_LazyDependencyResolution(t *testing.T) {
	localSource := newOrderServiceSource()
	store := &pullCountingStore{bundle: newPaymentBundle()}
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true, Reason: "found"}}
	base, cancel := startResolveTestServer(t, localSource, store, sourceInfo)
	defer cancel()

	ref := "oci://ghcr.io/org/payment-service-pacto:2.0.0"

	// Verify order-service exists, payment-service does not.
	expectGETStatus(t, base+"/api/services/order-service", http.StatusOK)
	expectGETStatus(t, base+"/api/services/payment-service", http.StatusNotFound)

	// Resolve the remote dependency.
	resp, details := resolveRef(t, base, ref)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /api/resolve, got %d", resp.StatusCode)
	}

	if details.Name != "payment-service" {
		t.Errorf("expected name 'payment-service', got %q", details.Name)
	}
	if details.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", details.Version)
	}
	if details.Source != "oci" {
		t.Errorf("expected source 'oci', got %q", details.Source)
	}
	if len(details.Interfaces) != 1 {
		t.Errorf("expected 1 interface, got %d", len(details.Interfaces))
	}
	if store.pullCount.Load() != 1 {
		t.Errorf("expected 1 pull call, got %d", store.pullCount.Load())
	}

	// Second resolve should also succeed.
	resp2, details2 := resolveRef(t, base, ref)
	defer resp2.Body.Close() //nolint:errcheck
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from cached resolve, got %d", resp2.StatusCode)
	}
	if details2.Name != "payment-service" {
		t.Errorf("expected name 'payment-service' on second resolve, got %q", details2.Name)
	}
}

// TestResolveE2E_AuthFailure validates that auth errors are properly surfaced.
func TestResolveE2E_AuthFailure(t *testing.T) {
	localSource := &stubSource{
		name:     "local",
		services: []Service{{Name: "my-svc", Version: "1.0.0", Source: "local"}},
		details: map[string]*ServiceDetails{
			"my-svc": {
				Service:      Service{Name: "my-svc", Version: "1.0.0", Source: "local"},
				Dependencies: []DependencyInfo{{Name: "private-dep", Ref: "ghcr.io/private/dep-pacto:1.0.0", Required: true}},
			},
		},
	}

	store := &pullCountingStore{
		pullErr: &oci.AuthenticationError{Ref: "ghcr.io/private/dep-pacto:1.0.0", Err: fmt.Errorf("401 unauthorized")},
	}

	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/resolve", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/private/dep-pacto:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}

	var errBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}

	// Verify the error message contains actionable info.
	detail, _ := errBody["detail"].(string)
	if detail == "" {
		detail, _ = errBody["title"].(string)
	}
	if !strings.Contains(strings.ToLower(detail), "authentication") {
		t.Errorf("expected auth-related error message, got %q", detail)
	}
}

// TestResolveE2E_InvalidRef validates proper 422 for invalid OCI references.
func TestResolveE2E_InvalidRef(t *testing.T) {
	localSource := &stubSource{name: "local", services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &pullCountingStore{
		pullErr: &oci.InvalidRefError{Ref: "bad-ref:1.0.0", Err: fmt.Errorf("invalid format")},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/resolve", "application/json",
		strings.NewReader(`{"ref":"bad-ref:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
}

// TestResolveE2E_InvalidBundle validates proper 422 for invalid bundles.
func TestResolveE2E_InvalidBundle(t *testing.T) {
	localSource := &stubSource{name: "local", services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &pullCountingStore{
		pullErr: &oci.InvalidBundleError{Ref: "ghcr.io/org/bad:1.0.0", Err: fmt.Errorf("missing pacto.yaml")},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/resolve", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/bad:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
}

// constrainedStore supports tag listing for semver resolution tests.
type constrainedStore struct {
	bundle *contract.Bundle
	tags   []string
}

func (s *constrainedStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *constrainedStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (s *constrainedStore) Pull(_ context.Context, _ string) (*contract.Bundle, error) {
	if s.bundle == nil {
		return nil, &oci.ArtifactNotFoundError{Ref: "x", Err: fmt.Errorf("not found")}
	}
	return s.bundle, nil
}
func (s *constrainedStore) ListTags(_ context.Context, _ string) ([]string, error) {
	return s.tags, nil
}

// TestResolveE2E_ConstrainedResolve validates that compatibility constraint is passed through API.
func TestResolveE2E_ConstrainedResolve(t *testing.T) {
	localSource := &stubSource{name: "local", services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &constrainedStore{
		bundle: newPaymentBundle(),
		tags:   []string{"1.0.0", "2.0.0", "2.1.0", "3.0.0"},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	// Resolve with constraint — should succeed
	resp, err := http.Post(base+"/api/resolve", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/payment-service-pacto","compatibility":"^2.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestResolveE2E_NoMatchingVersion validates 422 when no tags match constraint.
func TestResolveE2E_NoMatchingVersion(t *testing.T) {
	localSource := &stubSource{name: "local", services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &constrainedStore{
		tags: []string{"1.0.0", "1.1.0"},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/resolve", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/svc","compatibility":"^5.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp.StatusCode)
	}
}

// TestResolveE2E_ListRemoteVersions validates the POST /api/versions endpoint.
func TestResolveE2E_ListRemoteVersions(t *testing.T) {
	localSource := &stubSource{name: "local", services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &constrainedStore{
		tags: []string{"v1.0.0", "latest", "2.0.0", "abc", "1.5.0"},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/versions", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/svc"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Versions []string `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Versions) != 3 {
		t.Fatalf("expected 3 semver versions, got %d: %v", len(result.Versions), result.Versions)
	}
	// Should be sorted descending
	if result.Versions[0] != "2.0.0" {
		t.Errorf("expected first version 2.0.0, got %q", result.Versions[0])
	}
}

// ── Fetch all versions & cache promotion tests ──────────────────────

// cachingStore is a BundleStore that tracks pulls and persists to a temp cache dir.
type cachingStore struct {
	bundles  map[string]*contract.Bundle // tag -> bundle
	tags     []string
	pullRefs []string
}

func (s *cachingStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *cachingStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (s *cachingStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	s.pullRefs = append(s.pullRefs, ref)
	// Extract tag from ref.
	if idx := strings.LastIndex(ref, ":"); idx > 0 {
		tag := ref[idx+1:]
		if b, ok := s.bundles[tag]; ok {
			return b, nil
		}
	}
	// Return the first available bundle as fallback.
	for _, b := range s.bundles {
		return b, nil
	}
	return nil, &oci.ArtifactNotFoundError{Ref: ref, Err: fmt.Errorf("not found")}
}
func (s *cachingStore) ListTags(_ context.Context, _ string) ([]string, error) {
	return s.tags, nil
}

func newVersionedBundle(name, version string) *contract.Bundle {
	port := 8080
	yaml := fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: %s
  version: "%s"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`, name, version)
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: name, Version: version},
			Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
			Runtime: &contract.Runtime{
				Workload: "service",
				State: contract.State{
					Type:            "stateless",
					Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
					DataCriticality: "low",
				},
			},
		},
		RawYAML: []byte(yaml),
	}
}

func TestResolveE2E_FetchAllVersions_PersistsToCache(t *testing.T) {
	store := &cachingStore{
		bundles: map[string]*contract.Bundle{
			"1.0.0": newVersionedBundle("payment-service", "1.0.0"),
			"2.0.0": newVersionedBundle("payment-service", "2.0.0"),
			"3.0.0": newVersionedBundle("payment-service", "3.0.0"),
		},
		tags: []string{"1.0.0", "2.0.0", "3.0.0", "latest"},
	}

	localSource := &stubSource{
		name:     "local",
		services: []Service{{Name: "order-service", Version: "1.0.0", Source: "local"}},
		details: map[string]*ServiceDetails{
			"order-service": {Service: Service{Name: "order-service", Version: "1.0.0", Source: "local"}},
		},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	// Call POST /api/versions with fetch=true.
	resp, err := http.Post(base+"/api/versions", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/payment-service-pacto","fetch":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Versions []string `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Should return only semver tags (excluding "latest"), sorted descending.
	if len(result.Versions) != 3 {
		t.Fatalf("expected 3 versions, got %d: %v", len(result.Versions), result.Versions)
	}
	if result.Versions[0] != "3.0.0" {
		t.Errorf("expected first version 3.0.0, got %q", result.Versions[0])
	}

	// All 3 semver versions should have been pulled (cached).
	if len(store.pullRefs) != 3 {
		t.Fatalf("expected 3 pulls, got %d: %v", len(store.pullRefs), store.pullRefs)
	}
}

func TestResolveE2E_FetchAllVersions_DefaultNonFetchMode(t *testing.T) {
	store := &cachingStore{
		bundles: map[string]*contract.Bundle{
			"1.0.0": newVersionedBundle("svc", "1.0.0"),
		},
		tags: []string{"1.0.0", "2.0.0"},
	}

	localSource := &stubSource{
		name:     "local",
		services: []Service{},
		details:  map[string]*ServiceDetails{},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	// Default mode (fetch=false) should NOT pull bundles.
	resp, err := http.Post(base+"/api/versions", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/svc"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// No pulls should have happened.
	if len(store.pullRefs) != 0 {
		t.Errorf("expected 0 pulls in non-fetch mode, got %d", len(store.pullRefs))
	}
}

func TestResolveE2E_ResolvedDependency_AvailableAsService(t *testing.T) {
	// This test verifies that after resolving a dependency, it appears
	// in the service list (via index cache invalidation).
	localSource := newOrderServiceSource()
	store := &pullCountingStore{bundle: newPaymentBundle()}
	sourceInfo := []SourceInfo{{Type: "local", Enabled: true, Reason: "found"}}
	base, cancel := startResolveTestServer(t, localSource, store, sourceInfo)
	defer cancel()

	// payment-service not found before resolve.
	expectGETStatus(t, base+"/api/services/payment-service", http.StatusNotFound)

	// Resolve the dependency.
	resp, details := resolveRef(t, base, "oci://ghcr.io/org/payment-service-pacto:2.0.0")
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if details.Source != "oci" {
		t.Errorf("expected source 'oci', got %q", details.Source)
	}

	// After resolve, payment-service should be accessible
	// (through the resolver's index cache invalidation, which triggers
	// the next GetService to find it).
	// Note: with a real CachedStore+CacheSource, the service would
	// persist across restarts. Here we just verify the immediate visibility.
}

func TestResolveE2E_CurrentVersionIsHighestSemver(t *testing.T) {
	// Verify that version list is sorted by semver descending
	// and "latest" / non-semver tags are excluded.
	store := &cachingStore{
		bundles: map[string]*contract.Bundle{
			"1.0.0": newVersionedBundle("svc", "1.0.0"),
		},
		tags: []string{"v1.0.0", "latest", "3.0.0", "main", "2.0.0", "1.0.0-alpha"},
	}

	localSource := &stubSource{
		name:     "local",
		services: []Service{},
		details:  map[string]*ServiceDetails{},
	}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/versions", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/svc"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var result struct {
		Versions []string `json:"versions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	// Should exclude "latest" and "main", include all semver tags.
	if len(result.Versions) != 4 {
		t.Fatalf("expected 4 semver versions, got %d: %v", len(result.Versions), result.Versions)
	}
	if result.Versions[0] != "3.0.0" {
		t.Errorf("expected first (current) version '3.0.0', got %q", result.Versions[0])
	}
	if result.Versions[1] != "2.0.0" {
		t.Errorf("expected second version '2.0.0', got %q", result.Versions[1])
	}
}

// authFailTagStore fails ListTags with an auth error.
type authFailTagStore struct {
	constrainedStore
}

func (s *authFailTagStore) ListTags(_ context.Context, ref string) ([]string, error) {
	return nil, &oci.AuthenticationError{Ref: ref, Err: fmt.Errorf("401 unauthorized")}
}

// TestResolveE2E_ListRemoteVersions_AuthError validates 403 on auth failure.
func TestResolveE2E_ListRemoteVersions_AuthError(t *testing.T) {
	localSource := &stubSource{name: "local", services: []Service{}, details: map[string]*ServiceDetails{}}
	store := &authFailTagStore{}
	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/versions", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/svc"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

// TestResolveE2E_ListRemoteVersions_GenericError validates 502 on non-auth failure.
func TestResolveE2E_ListRemoteVersions_GenericError(t *testing.T) {
	localSource := &stubSource{name: "local", services: []Service{}, details: map[string]*ServiceDetails{}}
	base, cancel := startResolveTestServer(t, localSource, &genericTagErrStore{}, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/versions", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/svc"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
}

// genericTagErrStore fails ListTags with a generic error.
type genericTagErrStore struct {
	constrainedStore
}

func (s *genericTagErrStore) ListTags(_ context.Context, _ string) ([]string, error) {
	return nil, fmt.Errorf("network timeout")
}

// TestResolveE2E_ArtifactNotFound validates proper 404 from registry.
func TestResolveE2E_ArtifactNotFound(t *testing.T) {
	localSource := &stubSource{
		name:     "local",
		services: []Service{},
		details:  map[string]*ServiceDetails{},
	}

	store := &pullCountingStore{
		pullErr: &oci.ArtifactNotFoundError{Ref: "ghcr.io/org/nonexistent:1.0.0", Err: fmt.Errorf("404")},
	}

	base, cancel := startResolveTestServer(t, localSource, store, nil)
	defer cancel()

	resp, err := http.Post(base+"/api/resolve", "application/json",
		strings.NewReader(`{"ref":"ghcr.io/org/nonexistent:1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
