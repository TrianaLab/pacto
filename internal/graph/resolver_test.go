package graph

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

// mockFetcher returns pre-configured contracts by ref.
type mockFetcher struct {
	contracts map[string]*contract.Contract
}

func (m *mockFetcher) Fetch(_ context.Context, dep contract.Dependency) (*contract.Bundle, error) {
	c, ok := m.contracts[dep.Ref]
	if !ok {
		return nil, fmt.Errorf("not found: %s", dep.Ref)
	}
	return &contract.Bundle{Contract: c}, nil
}

// blockingFetcher blocks until signaled, then delegates to an inner fetcher.
type blockingFetcher struct {
	inner   ContractFetcher
	mu      sync.Mutex
	barrier chan struct{}
	calls   int
}

func newBlockingFetcher(inner ContractFetcher) *blockingFetcher {
	return &blockingFetcher{inner: inner, barrier: make(chan struct{})}
}

func (f *blockingFetcher) Fetch(ctx context.Context, dep contract.Dependency) (*contract.Bundle, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	<-f.barrier
	return f.inner.Fetch(ctx, dep)
}

func (f *blockingFetcher) release() { close(f.barrier) }

func TestResolve_NoDependencies(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
	}

	result := Resolve(context.Background(), c, nil)

	if result.Root.Name != "svc-a" {
		t.Errorf("expected root name svc-a, got %s", result.Root.Name)
	}
	if result.Root.Version != "1.0.0" {
		t.Errorf("expected root version 1.0.0, got %s", result.Root.Version)
	}
	if len(result.Root.Dependencies) != 0 {
		t.Errorf("expected 0 dependencies, got %d", len(result.Root.Dependencies))
	}
	if len(result.Cycles) != 0 {
		t.Errorf("expected 0 cycles, got %d", len(result.Cycles))
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(result.Conflicts))
	}
}

func TestResolve_DirectDependenciesNoFetcher(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, nil)

	if len(result.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Root.Dependencies))
	}

	edge := result.Root.Dependencies[0]
	if edge.Ref != "oci://registry.io/svc-b:1.0.0" {
		t.Errorf("expected ref oci://registry.io/svc-b:1.0.0, got %s", edge.Ref)
	}
	if edge.Node != nil {
		t.Error("expected nil node when fetcher is nil")
	}
}

func TestResolve_WithFetcher(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	if len(result.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Root.Dependencies))
	}

	edge := result.Root.Dependencies[0]
	if edge.Node == nil {
		t.Fatal("expected resolved node, got nil")
	}
	if edge.Node.Name != "svc-b" {
		t.Errorf("expected node name svc-b, got %s", edge.Node.Name)
	}
	if edge.Node.Version != "1.0.0" {
		t.Errorf("expected node version 1.0.0, got %s", edge.Node.Version)
	}
}

func TestResolve_TransitiveDependencies(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/svc-c:2.0.0", Required: true, Compatibility: "^2.0.0"},
				},
			},
			"oci://registry.io/svc-c:2.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-c", Version: "2.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	edge := result.Root.Dependencies[0]
	if edge.Node == nil {
		t.Fatal("expected resolved node for svc-b")
	}
	if len(edge.Node.Dependencies) != 1 {
		t.Fatalf("expected 1 transitive dep, got %d", len(edge.Node.Dependencies))
	}

	childEdge := edge.Node.Dependencies[0]
	if childEdge.Node == nil {
		t.Fatal("expected resolved node for svc-c")
	}
	if childEdge.Node.Name != "svc-c" {
		t.Errorf("expected svc-c, got %s", childEdge.Node.Name)
	}
}

func TestResolve_CycleDetection(t *testing.T) {
	// True cycle: A -> B -> B (self-referencing via same ref string)
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
				},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	if len(result.Cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(result.Cycles))
	}
}

func TestResolve_FetchError(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-missing:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	edge := result.Root.Dependencies[0]
	if edge.Error == "" {
		t.Error("expected error on edge, got empty string")
	}
	if edge.Node != nil {
		t.Error("expected nil node on error edge")
	}
}

func TestResolve_VersionConflict(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/svc-c:2.0.0", Required: true, Compatibility: "^2.0.0"},
				},
			},
			"oci://registry.io/svc-b:2.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "2.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/svc-c:3.0.0", Required: true, Compatibility: "^3.0.0"},
				},
			},
			"oci://registry.io/svc-c:2.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-c", Version: "2.0.0"},
			},
			"oci://registry.io/svc-c:3.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-c", Version: "3.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/svc-b:2.0.0", Required: true, Compatibility: "^2.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	// svc-c appears at both 2.0.0 and 3.0.0 — version conflict
	if len(result.Conflicts) == 0 {
		t.Fatal("expected at least 1 conflict")
	}

	found := false
	for _, conflict := range result.Conflicts {
		if conflict.Name == "svc-c" {
			found = true
			if len(conflict.Versions) < 2 {
				t.Errorf("expected at least 2 versions in conflict, got %d", len(conflict.Versions))
			}
		}
	}
	if !found {
		t.Error("expected conflict for svc-c")
	}
}

func TestResolve_MultipleDependencies(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
			},
			"oci://registry.io/svc-c:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-c", Version: "1.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/svc-c:1.0.0", Required: false, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	if len(result.Root.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(result.Root.Dependencies))
	}

	if result.Root.Dependencies[0].Node.Name != "svc-b" {
		t.Errorf("expected svc-b, got %s", result.Root.Dependencies[0].Node.Name)
	}
	if result.Root.Dependencies[1].Node.Name != "svc-c" {
		t.Errorf("expected svc-c, got %s", result.Root.Dependencies[1].Node.Name)
	}
	if result.Root.Dependencies[1].Required != false {
		t.Error("expected svc-c to be optional")
	}
}

func TestCollectVersions_NilNode(t *testing.T) {
	versions := map[string]map[string]bool{}
	collectVersions(nil, versions)
	if len(versions) != 0 {
		t.Errorf("expected empty map for nil node, got %v", versions)
	}
}

func TestResolve_DiamondDependency(t *testing.T) {
	// A -> B, A -> C, B -> D, C -> D
	// D should only be fetched once; second encounter hits visited[dep.Ref] skip
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/svc-d:1.0.0", Required: true, Compatibility: "^1.0.0"},
				},
			},
			"oci://registry.io/svc-c:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-c", Version: "1.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/svc-d:1.0.0", Required: true, Compatibility: "^1.0.0"},
				},
			},
			"oci://registry.io/svc-d:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-d", Version: "1.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/svc-c:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	// Both B and C should be resolved
	if len(result.Root.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(result.Root.Dependencies))
	}
	// Both B and C should have D as a dependency.
	// With parallel resolution, either B or C may resolve D first;
	// the other should see it as shared.
	bEdge := result.Root.Dependencies[0]
	if bEdge.Node == nil || len(bEdge.Node.Dependencies) != 1 {
		t.Fatal("expected B to have 1 dependency (D)")
	}
	cEdge := result.Root.Dependencies[1]
	if cEdge.Node == nil || len(cEdge.Node.Dependencies) != 1 {
		t.Fatal("expected C to have 1 dependency (D)")
	}
	dUnderB := bEdge.Node.Dependencies[0]
	dUnderC := cEdge.Node.Dependencies[0]

	sharedCount := 0
	for _, d := range []Edge{dUnderB, dUnderC} {
		if d.Node == nil {
			t.Fatal("expected D to have a node")
		}
		if d.Node.Name != "svc-d" {
			t.Errorf("expected svc-d, got %s", d.Node.Name)
		}
		if d.Shared {
			sharedCount++
			if len(d.Node.Dependencies) != 0 {
				t.Error("expected shared node to have no dependencies")
			}
		}
	}
	if sharedCount != 1 {
		t.Errorf("expected exactly 1 shared D edge, got %d", sharedCount)
	}
}

func TestResolve_SharedEdgeHasNodeInfo(t *testing.T) {
	// When the same ref is encountered twice, exactly one edge should be
	// marked Shared and carry name/version but no children.
	// With parallel resolution, the scheduling order is non-deterministic.
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	if len(result.Root.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(result.Root.Dependencies))
	}

	sharedCount := 0
	for _, edge := range result.Root.Dependencies {
		if edge.Shared {
			sharedCount++
			if edge.Node == nil {
				t.Fatal("shared edge should have node info")
			}
			if edge.Node.Name != "svc-b" {
				t.Errorf("expected svc-b, got %s", edge.Node.Name)
			}
		}
	}
	if sharedCount != 1 {
		t.Errorf("expected exactly 1 shared edge, got %d", sharedCount)
	}
}

func TestDetectConflicts_NilNodeInEdge(t *testing.T) {
	root := &Node{
		Name:    "svc-a",
		Version: "1.0.0",
		Dependencies: []Edge{
			{Ref: "oci://registry.io/svc-b:1.0.0", Node: nil}, // nil node (e.g., failed fetch)
		},
	}
	conflicts := detectConflicts(root)
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts with nil node, got %d", len(conflicts))
	}
}

func TestDetectConflicts_NoConflicts(t *testing.T) {
	root := &Node{
		Name:    "svc-a",
		Version: "1.0.0",
		Dependencies: []Edge{
			{
				Ref:  "oci://registry.io/svc-b:1.0.0",
				Node: &Node{Name: "svc-b", Version: "1.0.0"},
			},
			{
				Ref:  "oci://registry.io/svc-c:1.0.0",
				Node: &Node{Name: "svc-c", Version: "1.0.0"},
			},
		},
	}

	conflicts := detectConflicts(root)
	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}
}

func TestDetectConflicts_WithConflict(t *testing.T) {
	root := &Node{
		Name:    "svc-a",
		Version: "1.0.0",
		Dependencies: []Edge{
			{
				Ref: "oci://registry.io/svc-b:1.0.0",
				Node: &Node{
					Name:    "svc-b",
					Version: "1.0.0",
					Dependencies: []Edge{
						{
							Ref:  "oci://registry.io/svc-c:2.0.0",
							Node: &Node{Name: "svc-c", Version: "2.0.0"},
						},
					},
				},
			},
			{
				Ref:  "oci://registry.io/svc-c:3.0.0",
				Node: &Node{Name: "svc-c", Version: "3.0.0"},
			},
		},
	}

	conflicts := detectConflicts(root)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Name != "svc-c" {
		t.Errorf("expected conflict for svc-c, got %s", conflicts[0].Name)
	}
}

func TestResolve_LocalDependencyMarkedLocal(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"../dep-svc": {
				Service: contract.ServiceIdentity{Name: "dep-svc", Version: "1.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "../dep-svc", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	edge := result.Root.Dependencies[0]
	if !edge.Local {
		t.Error("expected edge to be marked as local")
	}
	if edge.Node == nil {
		t.Fatal("expected resolved node")
	}
	if !edge.Node.Local {
		t.Error("expected node to be marked as local")
	}
}

func TestResolve_FileSchemeMarkedLocal(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"file:///abs/path/dep-svc": {
				Service: contract.ServiceIdentity{Name: "dep-svc", Version: "2.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "file:///abs/path/dep-svc", Required: true, Compatibility: "^2.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	edge := result.Root.Dependencies[0]
	if !edge.Local {
		t.Error("expected file:// edge to be marked as local")
	}
	if edge.Node == nil {
		t.Fatal("expected resolved node")
	}
	if !edge.Node.Local {
		t.Error("expected file:// node to be marked as local")
	}
}

func TestResolve_OCINotMarkedLocal(t *testing.T) {
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	edge := result.Root.Dependencies[0]
	if edge.Local {
		t.Error("expected OCI edge to NOT be marked as local")
	}
	if edge.Node == nil {
		t.Fatal("expected resolved node")
	}
	if edge.Node.Local {
		t.Error("expected OCI node to NOT be marked as local")
	}
}

func TestResolve_PendingDedup(t *testing.T) {
	// Two sibling deps reference the same ref. A blocking fetcher ensures
	// both goroutines enter resolveEdge concurrently: one creates the
	// pending channel and fetches, the other waits on it.
	inner := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
			},
		},
	}
	bf := newBlockingFetcher(inner)

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	done := make(chan *Result, 1)
	go func() {
		done <- Resolve(context.Background(), c, bf)
	}()

	// Release the blocking fetcher once at least 1 fetch call has been made.
	// The second goroutine should be waiting on the pending channel,
	// not issuing another fetch.
	for {
		bf.mu.Lock()
		n := bf.calls
		bf.mu.Unlock()
		if n >= 1 {
			break
		}
	}
	bf.release()

	result := <-done

	if len(result.Root.Dependencies) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(result.Root.Dependencies))
	}
	sharedCount := 0
	for _, e := range result.Root.Dependencies {
		if e.Shared {
			sharedCount++
			if e.Node == nil {
				t.Error("shared edge should have node info")
			}
		}
	}
	if sharedCount != 1 {
		t.Errorf("expected exactly 1 shared, got %d", sharedCount)
	}
	// Only 1 fetch should have occurred since the second goroutine waited.
	bf.mu.Lock()
	fetchCalls := bf.calls
	bf.mu.Unlock()
	if fetchCalls != 1 {
		t.Errorf("expected 1 fetch call, got %d", fetchCalls)
	}
}

func TestResolve_PendingFetchError(t *testing.T) {
	// Directly test the pending-wait-nil-visited path by pre-populating
	// the resolver's internal state. A -> B (fetched first, fails),
	// A -> C (depends on B, sequential). B fails and is not added to
	// visited. C depends on the same ref as B via a transitive dep
	// structure that forces the pending path.
	//
	// We use a blocking fetcher to guarantee ordering:
	// 1. Goroutine 1 starts fetching B, blocks
	// 2. Goroutine 2 sees B in pending, waits on channel
	// 3. We release the barrier, B fetch fails
	// 4. Goroutine 2 wakes up, finds nil in visited
	inner := &mockFetcher{contracts: map[string]*contract.Contract{}}
	bf := newBlockingFetcher(inner)

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/missing:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/missing:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	done := make(chan *Result, 1)
	go func() {
		done <- Resolve(context.Background(), c, bf)
	}()

	// Wait until at least 1 fetch call is blocked.
	for {
		bf.mu.Lock()
		n := bf.calls
		bf.mu.Unlock()
		if n >= 1 {
			break
		}
	}
	bf.release()

	result := <-done

	if len(result.Root.Dependencies) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(result.Root.Dependencies))
	}
	// With concurrent resolution of the same failing ref, every edge must
	// carry an error — including the one that waited on the pending channel.
	for i, e := range result.Root.Dependencies {
		if e.Error == "" {
			t.Errorf("edge %d: expected error, got empty string (shared=%v)", i, e.Shared)
		}
	}
}

func TestResolve_PendingFetchError_Deterministic(t *testing.T) {
	// Deterministically test the path where a goroutine waits on a pending
	// channel and finds nil in visited (fetch failed). We achieve this by
	// having A -> B (transitive: B -> D), A -> C (transitive: C -> D),
	// where B is slow and D fails. This ensures D's pending channel is
	// created by one branch and waited on by the other.
	inner := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/missing:1.0.0", Required: true, Compatibility: "^1.0.0"},
				},
			},
			"oci://registry.io/svc-c:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-c", Version: "1.0.0"},
				Dependencies: []contract.Dependency{
					{Ref: "oci://registry.io/missing:1.0.0", Required: true, Compatibility: "^1.0.0"},
				},
			},
			// "missing" is NOT in the map, so Fetch fails
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/svc-c:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, inner)

	// B and C both depend on "missing". One will fetch and fail,
	// the other will find it via visited (nil) or pending-wait.
	bNode := result.Root.Dependencies[0].Node
	cNode := result.Root.Dependencies[1].Node
	if bNode == nil || cNode == nil {
		t.Fatal("expected B and C to be resolved")
	}

	// Both B and C have 1 dep on "missing". All edges must carry an error,
	// including the one that waited on the pending channel.
	allMissingEdges := append(bNode.Dependencies, cNode.Dependencies...)
	for i, e := range allMissingEdges {
		if e.Error == "" {
			t.Errorf("edge %d for missing dep: expected error, got empty string (shared=%v)", i, e.Shared)
		}
	}
}

func TestResolveEdge_PendingWaitGetsError(t *testing.T) {
	// Directly exercise the pending-wait error path by pre-populating
	// the resolver with a closed pending channel and a stored error.
	// A single call to resolveEdge sees the pending entry, receives
	// from the already-closed channel, finds nil in visited, and
	// reads the error from the errors map.
	r := &resolver{
		fetcher: &mockFetcher{},
		visited: map[string]*Node{},
		errors:  map[string]string{},
		pending: map[string]chan struct{}{},
	}

	ref := "oci://registry.io/fail:1.0.0"
	ch := make(chan struct{})
	close(ch) // pre-close so <-ch returns immediately
	r.pending[ref] = ch
	r.errors[ref] = "upstream failure"

	dep := contract.Dependency{Ref: ref, Required: true, Compatibility: "^1.0.0"}
	edge := r.resolveEdge(context.Background(), dep, []string{"root"})

	if !edge.Shared {
		t.Error("expected edge to be marked as shared")
	}
	if edge.Node != nil {
		t.Error("expected nil node for failed fetch")
	}
	if edge.Error != "upstream failure" {
		t.Errorf("expected error 'upstream failure', got %q", edge.Error)
	}
}

func TestResolve_FetchErrorParallel(t *testing.T) {
	// Fetch error with multiple deps (parallel path) where refs are unique.
	fetcher := &mockFetcher{
		contracts: map[string]*contract.Contract{
			"oci://registry.io/svc-b:1.0.0": {
				Service: contract.ServiceIdentity{Name: "svc-b", Version: "1.0.0"},
			},
		},
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc-a", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://registry.io/svc-b:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "oci://registry.io/missing:1.0.0", Required: true, Compatibility: "^1.0.0"},
		},
	}

	result := Resolve(context.Background(), c, fetcher)

	if len(result.Root.Dependencies) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(result.Root.Dependencies))
	}
	bEdge := result.Root.Dependencies[0]
	if bEdge.Node == nil {
		t.Error("expected svc-b to be resolved")
	}
	missingEdge := result.Root.Dependencies[1]
	if missingEdge.Error == "" {
		t.Error("expected error for missing ref")
	}
}
