package dashboard

import (
	"context"
	"fmt"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

// mockBundleStore implements oci.BundleStore for testing.
type mockBundleStore struct {
	bundles map[string]*contract.Bundle // keyed by ref (repo:tag)
	tags    map[string][]string         // keyed by repo
}

func newMockBundleStore() *mockBundleStore {
	return &mockBundleStore{
		bundles: make(map[string]*contract.Bundle),
		tags:    make(map[string][]string),
	}
}

func (m *mockBundleStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	b, ok := m.bundles[ref]
	if !ok {
		return nil, fmt.Errorf("bundle not found: %s", ref)
	}
	return b, nil
}

func (m *mockBundleStore) Push(_ context.Context, _ string, _ *contract.Bundle) (string, error) {
	return "sha256:mock", nil
}

func (m *mockBundleStore) ListTags(_ context.Context, repo string) ([]string, error) {
	tags, ok := m.tags[repo]
	if !ok {
		return nil, fmt.Errorf("repo not found: %s", repo)
	}
	return tags, nil
}

func (m *mockBundleStore) Resolve(_ context.Context, ref string) (string, error) {
	return "sha256:mock-" + ref, nil
}

func (m *mockBundleStore) addBundle(repo, tag, name, version string) {
	ref := repo + ":" + tag
	m.bundles[ref] = &contract.Bundle{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{
				Name:    name,
				Version: version,
			},
		},
		RawYAML: []byte(fmt.Sprintf("pactoVersion: \"1.0\"\nservice:\n  name: %s\n  version: %s\n", name, version)),
	}
	m.tags[repo] = append(m.tags[repo], tag)
}

func (m *mockBundleStore) addBundleWithDeps(repo, tag, name, version string, deps []contract.Dependency) {
	ref := repo + ":" + tag
	m.bundles[ref] = &contract.Bundle{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{
				Name:    name,
				Version: version,
			},
			Dependencies: deps,
		},
		RawYAML: []byte(fmt.Sprintf("pactoVersion: \"1.0\"\nservice:\n  name: %s\n  version: %s\n", name, version)),
	}
	m.tags[repo] = append(m.tags[repo], tag)
}

func TestOCISource_ListServices(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")
	store.addBundle("ghcr.io/org/api", "2.0.0", "api", "2.0.0")
	store.addBundle("ghcr.io/org/worker", "1.0.0", "worker", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api", "ghcr.io/org/worker"})
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	if services[0].Name != "api" {
		t.Errorf("expected first service 'api', got %q", services[0].Name)
	}
	if services[1].Name != "worker" {
		t.Errorf("expected second service 'worker', got %q", services[1].Name)
	}
	if services[0].Source != "oci" {
		t.Errorf("expected source 'oci', got %q", services[0].Source)
	}
}

func TestOCISource_ListServices_SkipsUnreachableRepos(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")
	// "ghcr.io/org/unreachable" has no tags registered, so ListTags will fail

	src := NewOCISource(store, []string{"ghcr.io/org/api", "ghcr.io/org/unreachable"})
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service (skipping unreachable), got %d", len(services))
	}
}

func TestOCISource_ListServices_SkipsEmptyTagRepos(t *testing.T) {
	store := newMockBundleStore()
	store.tags["ghcr.io/org/empty"] = []string{} // repo exists but has no tags

	src := NewOCISource(store, []string{"ghcr.io/org/empty"})
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(services))
	}
}

func TestOCISource_ListServices_SkipsPullErrors(t *testing.T) {
	store := newMockBundleStore()
	// Repo has tags but bundle pull fails
	store.tags["ghcr.io/org/bad"] = []string{"1.0.0"}
	// No bundle registered for the ref, so Pull will fail

	src := NewOCISource(store, []string{"ghcr.io/org/bad"})
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("expected 0 services (pull failed), got %d", len(services))
	}
}

func TestOCISource_GetService(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")
	store.addBundle("ghcr.io/org/api", "2.0.0", "api", "2.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	details, err := src.GetService(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "api" {
		t.Errorf("expected name 'api', got %q", details.Name)
	}
	// Latest tag should be 2.0.0
	if details.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", details.Version)
	}
}

func TestOCISource_GetService_NotFound(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	_, err := src.GetService(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestOCISource_GetVersions(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")
	store.addBundle("ghcr.io/org/api", "2.0.0", "api", "2.0.0")
	store.addBundle("ghcr.io/org/api", "3.0.0", "api", "3.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	versions, err := src.GetVersions(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	// Should be sorted descending
	if versions[0].Version != "3.0.0" {
		t.Errorf("expected first version '3.0.0', got %q", versions[0].Version)
	}
	if versions[2].Version != "1.0.0" {
		t.Errorf("expected last version '1.0.0', got %q", versions[2].Version)
	}
	// Ref should include repo
	if versions[0].Ref != "ghcr.io/org/api:3.0.0" {
		t.Errorf("expected ref with repo, got %q", versions[0].Ref)
	}
}

func TestOCISource_GetVersions_NotFound(t *testing.T) {
	store := newMockBundleStore()
	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	_, err := src.GetVersions(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestOCISource_GetDiff(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")
	store.addBundle("ghcr.io/org/api", "2.0.0", "api", "2.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	a := Ref{Name: "api", Version: "1.0.0"}
	b := Ref{Name: "api", Version: "2.0.0"}

	result, err := src.GetDiff(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil diff result")
	}
	if result.From.Version != "1.0.0" {
		t.Errorf("expected from version '1.0.0', got %q", result.From.Version)
	}
	if result.To.Version != "2.0.0" {
		t.Errorf("expected to version '2.0.0', got %q", result.To.Version)
	}
}

func TestOCISource_GetDiff_PullError(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")
	// Version 2.0.0 exists as tag but bundle is not registered

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	a := Ref{Name: "api", Version: "1.0.0"}
	b := Ref{Name: "api", Version: "9.9.9"}

	_, err := src.GetDiff(ctx, a, b)
	if err == nil {
		t.Fatal("expected error when pulling nonexistent version")
	}
}

func TestOCISource_GetDiff_FromPullError(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "2.0.0", "api", "2.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	a := Ref{Name: "api", Version: "9.9.9"}
	b := Ref{Name: "api", Version: "2.0.0"}

	_, err := src.GetDiff(ctx, a, b)
	if err == nil {
		t.Fatal("expected error when pulling nonexistent from version")
	}
}

func TestOCISource_FindRepo_ByPathComponent(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	repo, err := src.findRepo(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if repo != "ghcr.io/org/api" {
		t.Errorf("expected 'ghcr.io/org/api', got %q", repo)
	}
}

func TestOCISource_FindRepo_ByContractName(t *testing.T) {
	store := newMockBundleStore()
	// Repo name doesn't match service name, but contract does
	store.addBundle("ghcr.io/org/my-repo", "1.0.0", "my-service", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/my-repo"})
	ctx := context.Background()

	repo, err := src.findRepo(ctx, "my-service")
	if err != nil {
		t.Fatal(err)
	}
	if repo != "ghcr.io/org/my-repo" {
		t.Errorf("expected 'ghcr.io/org/my-repo', got %q", repo)
	}
}

func TestOCISource_FindRepo_NotFound(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	_, err := src.findRepo(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for not found service")
	}
}

func TestOCISource_FindRepo_ListTagsError(t *testing.T) {
	store := newMockBundleStore()
	// Repo "other" not registered; ListTags returns error; no path match either
	src := NewOCISource(store, []string{"ghcr.io/org/other"})
	ctx := context.Background()

	_, err := src.findRepo(ctx, "my-service")
	if err == nil {
		t.Fatal("expected error when no repo matches")
	}
}

func TestOCISource_FindRepo_PullErrorDuringLookup(t *testing.T) {
	store := newMockBundleStore()
	// Repo has tags but pull fails (bundle not registered)
	store.tags["ghcr.io/org/broken"] = []string{"1.0.0"}

	src := NewOCISource(store, []string{"ghcr.io/org/broken"})
	ctx := context.Background()

	_, err := src.findRepo(ctx, "my-service")
	if err == nil {
		t.Fatal("expected error when pull fails during lookup")
	}
}

func TestOCISource_FindLatestBundle(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/api", "1.0.0", "api", "1.0.0")
	store.addBundle("ghcr.io/org/api", "2.0.0", "api", "2.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	bundle, err := src.findLatestBundle(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Contract.Service.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", bundle.Contract.Service.Version)
	}
}

func TestOCISource_FindLatestBundle_NoTags(t *testing.T) {
	store := newMockBundleStore()
	store.tags["ghcr.io/org/api"] = []string{} // repo exists but empty

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	_, err := src.findLatestBundle(ctx, "api")
	if err == nil {
		t.Fatal("expected error when no tags found")
	}
}

func TestOCISource_GetVersions_ListTagsError(t *testing.T) {
	store := newMockBundleStore()
	// Repo matches by name but then ListTags fails for the version listing
	// because we need findRepo to succeed but the subsequent ListTags to fail.
	// Actually findRepo succeeds for path match, but then GetVersions calls ListTags again.
	// Let's simulate by having findRepo succeed by name but no tags in store.
	// findRepo matches "api" by last path component, so it returns the repo.
	// Then GetVersions calls ListTags on that repo.
	// If tags are not registered, it fails.

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	// findRepo will succeed because last path component is "api"
	// but ListTags will fail because store has no tags for this repo
	_, err := src.GetVersions(ctx, "api")
	if err == nil {
		t.Fatal("expected error when ListTags fails")
	}
}

func TestOCISource_ImplementsDataSource(t *testing.T) {
	store := newMockBundleStore()
	src := NewOCISource(store, nil)
	var _ DataSource = src
}

func TestOCISource_PullRef_FindRepoError(t *testing.T) {
	store := newMockBundleStore()
	// No repos configured — findRepo will fail.
	src := NewOCISource(store, []string{})
	ctx := context.Background()

	_, err := src.pullRef(ctx, Ref{Name: "nonexistent", Version: "1.0.0"})
	if err == nil {
		t.Fatal("expected error when no repo matches")
	}
}

func TestOCISource_FindLatestBundle_PullError(t *testing.T) {
	store := newMockBundleStore()
	// Repo matches by name, has tags, but pull fails.
	store.tags["ghcr.io/org/api"] = []string{"1.0.0"}
	// No bundle registered, so Pull will fail.

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	_, err := src.findLatestBundle(ctx, "api")
	if err == nil {
		t.Fatal("expected error when pull fails after getting tags")
	}
}

func TestOCISource_FindLatestBundle_ListTagsError(t *testing.T) {
	store := newMockBundleStore()
	// Repo matches by path component but ListTags will fail (no tags registered).
	// findRepo returns "ghcr.io/org/api" because last path component matches.
	// Then findLatestBundle calls ListTags, which errors because repo is not in store.tags.

	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	ctx := context.Background()

	_, err := src.findLatestBundle(ctx, "api")
	if err == nil {
		t.Fatal("expected error when ListTags fails")
	}
}

func TestOCISource_FindRepo_CachedRepoMap(t *testing.T) {
	store := newMockBundleStore()
	src := NewOCISource(store, []string{"ghcr.io/org/api"})
	// Pre-populate repoMap as ListServices would.
	src.repoMap = map[string]string{"my-svc": "ghcr.io/org/api"}

	repo, err := src.findRepo(context.Background(), "my-svc")
	if err != nil {
		t.Fatal(err)
	}
	if repo != "ghcr.io/org/api" {
		t.Errorf("expected cached repo, got %q", repo)
	}
}

// waitForDiscovery triggers ListServices and waits for background discovery to finish.
func waitForDiscovery(t *testing.T, src *OCISource) []Service {
	t.Helper()
	ctx := context.Background()
	// First call triggers shallow scan + background discovery.
	_, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for background discovery to complete.
	<-src.done
	// Second call returns the full discovered set.
	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return services
}

func TestOCISource_ListServices_RecursiveDependencies(t *testing.T) {
	store := newMockBundleStore()

	// Root service depends on two OCI services.
	store.addBundleWithDeps("ghcr.io/org/root", "1.0.0", "root", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/svc-a", Required: true, Compatibility: "^1.0.0"},
		{Ref: "oci://ghcr.io/org/svc-b:1.0.0", Required: true},
	})
	// svc-a depends on svc-c.
	store.addBundleWithDeps("ghcr.io/org/svc-a", "1.0.0", "svc-a", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/svc-c", Required: true},
	})
	store.addBundle("ghcr.io/org/svc-b", "1.0.0", "svc-b", "1.0.0")
	store.addBundle("ghcr.io/org/svc-c", "1.0.0", "svc-c", "1.0.0")

	// Only the root repo is configured.
	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)

	names := make(map[string]bool)
	for _, svc := range services {
		names[svc.Name] = true
	}

	for _, expected := range []string{"root", "svc-a", "svc-b", "svc-c"} {
		if !names[expected] {
			t.Errorf("expected service %q to be discovered, got services: %v", expected, names)
		}
	}
	if len(services) != 4 {
		t.Errorf("expected 4 services, got %d", len(services))
	}
}

func TestOCISource_ListServices_RecursiveHandlesCycles(t *testing.T) {
	store := newMockBundleStore()

	// a -> b -> a (cycle)
	store.addBundleWithDeps("ghcr.io/org/a", "1.0.0", "a", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/b"},
	})
	store.addBundleWithDeps("ghcr.io/org/b", "1.0.0", "b", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/a"},
	})

	src := NewOCISource(store, []string{"ghcr.io/org/a"})
	services := waitForDiscovery(t, src)
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

func TestOCISource_ListServices_RecursiveReferences(t *testing.T) {
	store := newMockBundleStore()

	// Root service has a configuration ref and a policy ref pointing to OCI bundles.
	ref := "ghcr.io/org/root:1.0.0"
	store.bundles[ref] = &contract.Bundle{
		Contract: &contract.Contract{
			Service:       contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
			Configuration: &contract.Configuration{Ref: "oci://ghcr.io/org/shared-config"},
			Policy:        &contract.Policy{Ref: "oci://ghcr.io/org/shared-policy:1.0.0"},
		},
		RawYAML: []byte("pactoVersion: \"1.0\"\nservice:\n  name: root\n  version: 1.0.0\n"),
	}
	store.tags["ghcr.io/org/root"] = []string{"1.0.0"}
	store.addBundle("ghcr.io/org/shared-config", "1.0.0", "shared-config", "1.0.0")
	store.addBundle("ghcr.io/org/shared-policy", "1.0.0", "shared-policy", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)

	names := make(map[string]bool)
	for _, svc := range services {
		names[svc.Name] = true
	}

	for _, expected := range []string{"root", "shared-config", "shared-policy"} {
		if !names[expected] {
			t.Errorf("expected referenced service %q to be discovered, got services: %v", expected, names)
		}
	}
}

func TestOCISource_ListServices_SkipsLocalDeps(t *testing.T) {
	store := newMockBundleStore()

	// Root has both OCI and local dependencies.
	store.addBundleWithDeps("ghcr.io/org/root", "1.0.0", "root", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/remote", Required: true},
		{Ref: "./local-dep"},
		{Ref: "file://./another-local"},
	})
	store.addBundle("ghcr.io/org/remote", "1.0.0", "remote", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)
	if len(services) != 2 {
		t.Fatalf("expected 2 services (root + remote), got %d", len(services))
	}
}

func TestOCISource_ListServices_RecursiveSkipsUnreachableDeps(t *testing.T) {
	store := newMockBundleStore()

	// Root depends on a reachable and an unreachable dep.
	store.addBundleWithDeps("ghcr.io/org/root", "1.0.0", "root", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/reachable"},
		{Ref: "oci://ghcr.io/org/unreachable"},
	})
	store.addBundle("ghcr.io/org/reachable", "1.0.0", "reachable", "1.0.0")
	// "unreachable" is not registered — ListTags will fail.

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)
	if len(services) != 2 {
		t.Fatalf("expected 2 services (root + reachable), got %d", len(services))
	}
}

func TestOCISource_ListServices_SharedDependency(t *testing.T) {
	store := newMockBundleStore()

	// Both a and b depend on shared.
	store.addBundleWithDeps("ghcr.io/org/root", "1.0.0", "root", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/a"},
		{Ref: "oci://ghcr.io/org/b"},
	})
	store.addBundleWithDeps("ghcr.io/org/a", "1.0.0", "a", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/shared"},
	})
	store.addBundleWithDeps("ghcr.io/org/b", "1.0.0", "b", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/shared"},
	})
	store.addBundle("ghcr.io/org/shared", "1.0.0", "shared", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)
	if len(services) != 4 {
		t.Fatalf("expected 4 services (root, a, b, shared), got %d", len(services))
	}
}

func TestOCISource_ListServices_ShallowScanImmediate(t *testing.T) {
	store := newMockBundleStore()

	// Root has deps but the first call should return immediately with just root.
	store.addBundleWithDeps("ghcr.io/org/root", "1.0.0", "root", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/dep"},
	})
	store.addBundle("ghcr.io/org/dep", "1.0.0", "dep", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	ctx := context.Background()

	// First call should return at least the root (shallow scan).
	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) < 1 {
		t.Fatalf("expected at least 1 service from shallow scan, got %d", len(services))
	}
	found := false
	for _, svc := range services {
		if svc.Name == "root" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected root service in shallow scan results")
	}
}

func TestExtractOCIRepo(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"oci://ghcr.io/org/svc", "ghcr.io/org/svc"},
		{"oci://ghcr.io/org/svc:1.0.0", "ghcr.io/org/svc:1.0.0"},
		{"./local", ""},
		{"file://./local", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractOCIRepo(tt.ref)
		if got != tt.want {
			t.Errorf("extractOCIRepo(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestStripTag(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"ghcr.io/org/svc:1.0.0", "ghcr.io/org/svc"},
		{"ghcr.io/org/svc", "ghcr.io/org/svc"},
		{"ghcr.io/org/svc@sha256:abc", "ghcr.io/org/svc"},
	}
	for _, tt := range tests {
		got := stripTag(tt.ref)
		if got != tt.want {
			t.Errorf("stripTag(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestOCISource_SetOnDiscover(t *testing.T) {
	store := newMockBundleStore()
	store.addBundle("ghcr.io/org/svc", "1.0.0", "svc", "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/svc"})

	var called int
	src.SetOnDiscover(func() { called++ })

	services := waitForDiscovery(t, src)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if called == 0 {
		t.Error("expected onDiscover callback to be called")
	}
}

func TestOCISource_Discovering(t *testing.T) {
	store := newMockBundleStore()
	src := NewOCISource(store, nil)

	// Before ListServices, Discovering is false (not started).
	if src.Discovering() {
		t.Error("expected Discovering()=false before start")
	}

	// After discovery completes, Discovering is false.
	store.addBundle("ghcr.io/org/svc", "1.0.0", "svc", "1.0.0")
	src2 := NewOCISource(store, []string{"ghcr.io/org/svc"})
	waitForDiscovery(t, src2)
	if src2.Discovering() {
		t.Error("expected Discovering()=false after completion")
	}
}

func TestOCISource_DiscoverRepo_DuplicateServiceName(t *testing.T) {
	store := newMockBundleStore()
	// Two different repos produce bundles with the same service name.
	store.addBundle("ghcr.io/org/svc-v1", "1.0.0", "svc", "1.0.0")
	store.addBundle("ghcr.io/org/svc-v2", "2.0.0", "svc", "2.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/svc-v1", "ghcr.io/org/svc-v2"})
	services := waitForDiscovery(t, src)

	// Only one should be registered (first wins).
	if len(services) != 1 {
		t.Fatalf("expected 1 service (duplicate name skipped), got %d", len(services))
	}
}

func TestOCISource_BackgroundDiscover_PrefetchErrors(t *testing.T) {
	store := newMockBundleStore()
	// Root with a dep. Dep has valid latest + an older tag that fails to pull.
	store.addBundleWithDeps("ghcr.io/org/root", "1.0.0", "root", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/dep"},
	})
	// Register dep with version 2.0.0 as latest (will be discovered via this).
	store.addBundle("ghcr.io/org/dep", "2.0.0", "dep", "2.0.0")
	// Add an older tag with no bundle — simulates a pull error during prefetch.
	store.tags["ghcr.io/org/dep"] = append(store.tags["ghcr.io/org/dep"], "1.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)

	// Both services should be discovered despite pull error on 1.0.0.
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

// countingStore wraps mockBundleStore and makes ListTags fail for a repo
// after it has been called a given number of times for that repo.
type countingStore struct {
	*mockBundleStore
	listTagsCalls map[string]int
	failAfter     int // fail ListTags after this many calls per repo
}

func (c *countingStore) ListTags(ctx context.Context, repo string) ([]string, error) {
	c.listTagsCalls[repo]++
	if c.listTagsCalls[repo] > c.failAfter {
		return nil, fmt.Errorf("simulated ListTags failure for %s", repo)
	}
	return c.mockBundleStore.ListTags(ctx, repo)
}

func TestOCISource_BackgroundDiscover_ListTagsErrorDuringPrefetch(t *testing.T) {
	base := newMockBundleStore()
	base.addBundle("ghcr.io/org/root", "1.0.0", "root", "1.0.0")

	// ListTags is called during: shallowScan(1), discoverRepo+BFS(0 for root, no deps),
	// and prefetch(1). Allow first call (discovery), fail second (prefetch).
	store := &countingStore{mockBundleStore: base, listTagsCalls: make(map[string]int), failAfter: 1}

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
}

func TestOCISource_DepReposForService_FindBundleError(t *testing.T) {
	store := newMockBundleStore()
	src := NewOCISource(store, nil)
	// depReposForService for a nonexistent service returns nil.
	repos := src.depReposForService(context.Background(), "nonexistent")
	if repos != nil {
		t.Errorf("expected nil repos for nonexistent service, got %v", repos)
	}
}

func TestOCISource_DepReposForService_WithExplicitTag(t *testing.T) {
	store := newMockBundleStore()
	store.addBundleWithDeps("ghcr.io/org/root", "1.0.0", "root", "1.0.0", []contract.Dependency{
		{Ref: "oci://ghcr.io/org/dep:2.0.0", Required: true},
	})
	store.addBundle("ghcr.io/org/dep", "2.0.0", "dep", "2.0.0")

	src := NewOCISource(store, []string{"ghcr.io/org/root"})
	services := waitForDiscovery(t, src)

	// Should discover both root and dep (tag stripped from ref).
	names := make(map[string]bool)
	for _, svc := range services {
		names[svc.Name] = true
	}
	if !names["root"] || !names["dep"] {
		t.Errorf("expected root and dep, got %v", names)
	}
}
