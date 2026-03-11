package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/internal/graph"
	"github.com/trianalab/pacto/pkg/contract"
)

func TestDiff_LocalFiles(t *testing.T) {
	oldDir := writeTestBundle(t)
	newDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OldPath != oldDir {
		t.Errorf("expected OldPath=%s, got %s", oldDir, result.OldPath)
	}
	if result.NewPath != newDir {
		t.Errorf("expected NewPath=%s, got %s", newDir, result.NewPath)
	}
	if result.Classification == "" {
		t.Error("expected non-empty classification")
	}
}

func TestDiff_OldPathError(t *testing.T) {
	newDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	_, err := svc.Diff(context.Background(), DiffOptions{OldPath: "/nonexistent/dir", NewPath: newDir})
	if err == nil {
		t.Error("expected error for nonexistent old path")
	}
}

func TestDiff_NewPathError(t *testing.T) {
	oldDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	_, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: "/nonexistent/dir"})
	if err == nil {
		t.Error("expected error for nonexistent new path")
	}
}

func TestDiff_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{
		OldPath: "oci://ghcr.io/acme/svc:1.0.0",
		NewPath: "oci://ghcr.io/acme/svc:2.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification == "" {
		t.Error("expected non-empty classification")
	}
}

// writeBundleWithDep creates a parent bundle dir with a local dependency child.
// Returns the parent dir path.
func writeBundleWithDep(t *testing.T, parentVersion, childVersion string) string {
	t.Helper()
	dir := t.TempDir()

	parentYAML := []byte(`pactoVersion: "1.0"
service:
  name: parent-svc
  version: "` + parentVersion + `"
dependencies:
  - ref: ./child
    required: true
    compatibility: "^1.0.0"
`)
	childYAML := []byte(`pactoVersion: "1.0"
service:
  name: child-svc
  version: "` + childVersion + `"
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
  health:
    interface: api
    path: /health
`)

	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), parentYAML, 0644); err != nil {
		t.Fatal(err)
	}
	childDir := filepath.Join(dir, "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "pacto.yaml"), childYAML, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiff_DependencyChanges(t *testing.T) {
	oldDir := writeBundleWithDep(t, "1.0.0", "1.0.0")
	newDir := writeBundleWithDep(t, "1.0.0", "2.0.0")
	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.DependencyDiffs) == 0 {
		t.Fatal("expected dependency diffs for child version change")
	}
	dd := result.DependencyDiffs[0]
	if dd.Name != "child-svc" {
		t.Errorf("expected dep name 'child-svc', got %q", dd.Name)
	}
	if dd.Classification == "" {
		t.Error("expected non-empty classification on dependency diff")
	}
}

func TestDiff_DependencyNoChanges(t *testing.T) {
	oldDir := writeBundleWithDep(t, "1.0.0", "1.0.0")
	newDir := writeBundleWithDep(t, "1.0.0", "1.0.0")
	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.DependencyDiffs) != 0 {
		t.Errorf("expected no dependency diffs for identical children, got %d", len(result.DependencyDiffs))
	}
}

func TestDiff_DependencyClassificationElevation(t *testing.T) {
	// Old child has an extra interface; new child removes it → BREAKING in child.
	// Parent is identical → NON_BREAKING at root.
	// Overall should be elevated to BREAKING.
	oldDir := writeBundleWithDep(t, "1.0.0", "1.0.0")

	// Add a second interface to old child so removing it in new is BREAKING.
	oldChildYAML := []byte(`pactoVersion: "1.0"
service:
  name: child-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
  - name: grpc
    type: grpc
    port: 9090
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	if err := os.WriteFile(filepath.Join(oldDir, "child", "pacto.yaml"), oldChildYAML, 0644); err != nil {
		t.Fatal(err)
	}

	newDir := writeBundleWithDep(t, "1.0.0", "1.0.0")
	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification != "BREAKING" {
		t.Errorf("expected BREAKING overall, got %s", result.Classification)
	}
}

func TestDiff_DependencyOnlyInNew(t *testing.T) {
	// Old has no dependencies, new has one → the dep exists only in newNodes.
	// This tests the !exists branch at line 71.
	oldDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(oldDir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: parent-svc
  version: "1.0.0"
`), 0644); err != nil {
		t.Fatal(err)
	}

	newDir := writeBundleWithDep(t, "1.0.0", "1.0.0")
	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// child-svc only in new, so it should be skipped (no old to compare against).
	for _, dd := range result.DependencyDiffs {
		if dd.Name == "child-svc" {
			t.Error("did not expect child-svc in dependency diffs when it only exists in new")
		}
	}
}

func TestOciRefName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"ghcr.io/org/pactos/my-svc:1.0.0", "my-svc"},
		{"ghcr.io/org/pactos/my-svc", "my-svc"},
		{"registry.io/svc", "svc"},
	}
	for _, tt := range tests {
		if got := ociRefName(tt.input); got != tt.want {
			t.Errorf("ociRefName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDiff_LocalOverrideForOCIDeps(t *testing.T) {
	// Regression test: when the NEW side is local and has OCI deps, the diff
	// must resolve those deps from local siblings (not the registry). Without
	// the local override, both sides resolve to the same published artifact
	// and no dependency diff appears — the exact bug that went undetected.

	dir := t.TempDir()
	parentYAML := []byte(`pactoVersion: "1.0"
service:
  name: parent-svc
  version: "1.0.0"
dependencies:
  - ref: oci://ghcr.io/acme/child-svc:1.0.0
    required: true
    compatibility: "^1.0.0"
`)

	// OLD parent (OCI) — resolved from mock store, which also returns the
	// mock child bundle (child-svc v1.0.0, port 8080).
	// NEW parent (local) — identical contract, but sibling child-svc/ has
	// a different version (2.0.0) that should be picked up via local override.
	parentDir := filepath.Join(dir, "parent-svc")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "pacto.yaml"), parentYAML, 0644); err != nil {
		t.Fatal(err)
	}

	childDir := filepath.Join(dir, "child-svc")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: child-svc
  version: "2.0.0"
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
  health:
    interface: api
    path: /health
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Mock store returns a parent-svc bundle with the same OCI dep, and a
	// child-svc bundle at v1.0.0 (the "published" version).
	childPort := 8080
	store := &mockBundleStore{
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if strings.Contains(ref, "child-svc") {
				return &contract.Bundle{
					Contract: &contract.Contract{
						PactoVersion: "1.0",
						Service:      contract.ServiceIdentity{Name: "child-svc", Version: "1.0.0"},
						Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &childPort}},
						Runtime: &contract.Runtime{
							Workload: "service",
							State: contract.State{
								Type:            "stateless",
								Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
								DataCriticality: "low",
							},
							Health: &contract.Health{Interface: "api", Path: "/health"},
						},
					},
					RawYAML: []byte(""),
				}, nil
			}
			// parent-svc from OCI
			return &contract.Bundle{
				Contract: &contract.Contract{
					PactoVersion: "1.0",
					Service:      contract.ServiceIdentity{Name: "parent-svc", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Ref: "oci://ghcr.io/acme/child-svc:1.0.0", Required: true, Compatibility: "^1.0.0"},
					},
				},
				RawYAML: parentYAML,
			}, nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{
		OldPath: "oci://ghcr.io/acme/parent-svc:1.0.0",
		NewPath: parentDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The key assertion: local override should cause child-svc to differ
	// (v1.0.0 from OCI vs v2.0.0 from local sibling).
	if len(result.DependencyDiffs) == 0 {
		t.Fatal("expected dependency diffs for child-svc (local override should differ from OCI)")
	}
	found := false
	for _, dd := range result.DependencyDiffs {
		if dd.Name == "child-svc" {
			found = true
			if dd.Classification == "" {
				t.Error("expected non-empty classification on child-svc diff")
			}
		}
	}
	if !found {
		t.Error("expected child-svc in dependency diffs")
	}
}

func TestNewDiffFetcher_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	fetcher := svc.newDiffFetcher("oci://ghcr.io/acme/svc:1.0.0")
	if _, ok := fetcher.(*localOverrideFetcher); ok {
		t.Error("expected regular fetcher for OCI ref, got localOverrideFetcher")
	}
}

func TestNewDiffFetcher_LocalRef(t *testing.T) {
	svc := NewService(nil, nil)
	fetcher := svc.newDiffFetcher(t.TempDir())
	if _, ok := fetcher.(*localOverrideFetcher); !ok {
		t.Error("expected localOverrideFetcher for local ref")
	}
}

func TestLocalOverrideFetcher_FallbackToInner(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	inner := svc.newDepFetcher("oci://ghcr.io/acme/svc:1.0.0")
	f := &localOverrideFetcher{inner: inner, parentDir: t.TempDir()}
	dep := contract.Dependency{Ref: "oci://ghcr.io/acme/child-svc:1.0.0", Compatibility: "^1.0.0"}
	bundle, err := f.Fetch(context.Background(), dep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Contract.Service.Name != "test-svc" {
		t.Errorf("expected fallback to inner fetcher (test-svc), got %q", bundle.Contract.Service.Name)
	}
}

func TestLocalOverrideFetcher_LocalDep(t *testing.T) {
	// When dep ref is local (not OCI), should delegate to inner without override.
	bundleDir := writeTestBundle(t)
	parentDir := filepath.Dir(bundleDir)
	svc := NewService(nil, nil)
	inner := svc.newDepFetcher(parentDir)
	f := &localOverrideFetcher{inner: inner, parentDir: parentDir}
	dep := contract.Dependency{Ref: filepath.Base(bundleDir), Compatibility: "^1.0.0"}
	bundle, err := f.Fetch(context.Background(), dep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bundle.Contract.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %q", bundle.Contract.Service.Name)
	}
}

func TestDiff_DocsDirectoryIgnored(t *testing.T) {
	// Create two bundles with identical contracts but different docs/ content.
	// The diff must produce zero changes — docs/ is informational metadata.
	oldDir := writeTestBundle(t)
	newDir := writeTestBundle(t)

	// Add docs/ to old bundle.
	oldDocsDir := filepath.Join(oldDir, "docs")
	if err := os.MkdirAll(oldDocsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDocsDir, "README.md"), []byte("# Old Docs"), 0644); err != nil {
		t.Fatal(err)
	}

	// Add different docs/ to new bundle.
	newDocsDir := filepath.Join(newDir, "docs")
	if err := os.MkdirAll(newDocsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDocsDir, "README.md"), []byte("# Completely Rewritten Docs"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDocsDir, "runbook.md"), []byte("# New Runbook"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING when only docs/ differs, got %s", result.Classification)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes when only docs/ differs, got %d: %v", len(result.Changes), result.Changes)
	}
}

func TestDiff_DocsAddedToNewBundle(t *testing.T) {
	// Old bundle has no docs/, new bundle adds docs/. No changes expected.
	oldDir := writeTestBundle(t)
	newDir := writeTestBundle(t)

	newDocsDir := filepath.Join(newDir, "docs")
	if err := os.MkdirAll(newDocsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDocsDir, "README.md"), []byte("# Service Docs"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING when docs/ is added, got %s", result.Classification)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes when docs/ is added, got %d", len(result.Changes))
	}
}

func TestDiff_DocsRemovedFromBundle(t *testing.T) {
	// Old bundle has docs/, new bundle removes it. No changes expected.
	oldDir := writeTestBundle(t)
	newDir := writeTestBundle(t)

	oldDocsDir := filepath.Join(oldDir, "docs")
	if err := os.MkdirAll(oldDocsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDocsDir, "README.md"), []byte("# Docs"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification != "NON_BREAKING" {
		t.Errorf("expected NON_BREAKING when docs/ is removed, got %s", result.Classification)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes when docs/ is removed, got %d", len(result.Changes))
	}
}

func TestCollectNodes_NilRoot(t *testing.T) {
	nodes := collectNodes(nil)
	if len(nodes) != 0 {
		t.Errorf("expected empty map, got %d entries", len(nodes))
	}
}

func TestCollectNodes_WithDependencies(t *testing.T) {
	root := &graph.Node{
		Name:    "root",
		Version: "1.0.0",
		Dependencies: []graph.Edge{
			{Node: &graph.Node{
				Name:    "child-a",
				Version: "2.0.0",
				Dependencies: []graph.Edge{
					{Node: &graph.Node{Name: "grandchild", Version: "3.0.0"}},
				},
			}},
			{Node: &graph.Node{Name: "child-b", Version: "4.0.0"}},
		},
	}
	nodes := collectNodes(root)
	if len(nodes) != 4 {
		t.Errorf("expected 4 nodes, got %d", len(nodes))
	}
	for _, name := range []string{"root", "child-a", "child-b", "grandchild"} {
		if _, ok := nodes[name]; !ok {
			t.Errorf("expected node %q in map", name)
		}
	}
}

func TestCollectNodes_CycleHandling(t *testing.T) {
	child := &graph.Node{Name: "child", Version: "1.0.0"}
	root := &graph.Node{
		Name:         "root",
		Version:      "1.0.0",
		Dependencies: []graph.Edge{{Node: child}},
	}
	// Create a cycle: child -> root
	child.Dependencies = []graph.Edge{{Node: root}}

	nodes := collectNodes(root)
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes (cycle should be handled), got %d", len(nodes))
	}
}
