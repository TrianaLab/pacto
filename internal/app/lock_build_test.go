package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v2/internal/testutil"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/lock"
)

// rootBundleWithDep returns a local root contract that declares one OCI dep.
func rootBundleWithDep() *contract.Bundle {
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "root", Version: "2.1.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth", Compatibility: "^1.0.0", Required: true},
		},
	}
	return &contract.Bundle{Contract: c}
}

func TestBuildLockCapturesDigest(t *testing.T) {
	authContract := &contract.Contract{Service: contract.Service{Name: "auth", Version: "1.2.0"}}
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.0"}, nil },
		ResolveFn:  func(_ context.Context, _ string) (string, error) { return "sha256:authdigest", nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: authContract}, nil
		},
	}
	s := NewService(store, nil)

	l, err := s.buildLock(context.Background(), "testdata/root", rootBundleWithDep(), nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	e, ok := l.Dependency("auth")
	if !ok {
		t.Fatalf("auth not in lock: %+v", l.Dependencies)
	}
	if e.Digest != "sha256:authdigest" || e.Version != "1.2.0" || e.Constraint != "^1.0.0" || e.Source != "oci" {
		t.Errorf("auth entry wrong: %+v", e)
	}
	if e.Ref != "oci://ghcr.io/acme/auth" {
		t.Errorf("auth ref wrong: %q", e.Ref)
	}
	if l.Root.Name != "root" || l.Root.Version != "2.1.0" {
		t.Errorf("root info wrong: %+v", l.Root)
	}
	if l.Pacto.Version != BuildVersion {
		t.Errorf("pacto version: got %q want %q", l.Pacto.Version, BuildVersion)
	}
	if l.LockVersion != lock.CurrentLockVersion {
		t.Errorf("lock version: got %d", l.LockVersion)
	}
}

// TestBuildLockDependsOn covers transitive deps and dependsOn population.
func TestBuildLockDependsOn(t *testing.T) {
	// root -> auth -> db. auth's lock entry lists db in dependsOn.
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			switch ref {
			case "ghcr.io/acme/auth:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service: contract.Service{Name: "auth", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Name: "db", Ref: "oci://ghcr.io/acme/db:2.0.0", Compatibility: "^2.0.0", Required: true},
					},
				}}, nil
			case "ghcr.io/acme/db:2.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service: contract.Service{Name: "db", Version: "2.0.0"},
				}}, nil
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	l, err := s.buildLock(context.Background(), "testdata/root", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	if len(l.Dependencies) != 2 {
		t.Fatalf("expected 2 deps, got %d: %+v", len(l.Dependencies), l.Dependencies)
	}
	auth, ok := l.Dependency("auth")
	if !ok {
		t.Fatalf("auth missing")
	}
	if len(auth.DependsOn) != 1 || auth.DependsOn[0] != "db" {
		t.Errorf("auth dependsOn wrong: %+v", auth.DependsOn)
	}
	if _, ok := l.Dependency("db"); !ok {
		t.Errorf("db missing from lock")
	}
}

// TestBuildLockLocalDep covers the local-dependency branch (contentHash via HashFS).
func TestBuildLockLocalDep(t *testing.T) {
	// The dep fetcher loads local bundles from disk via loadLocalBundle, so we
	// drive the local branch through a fetched node by injecting a node whose
	// ref is local. We use a store-free service and a local dep on disk.
	dir := testutil.WriteTestBundle(t)
	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "local-dep", Ref: dir, Compatibility: "", Required: true},
		},
	}}
	l, err := s.buildLock(context.Background(), ".", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	e, ok := l.Dependency("test-svc")
	if !ok {
		t.Fatalf("local dep not in lock: %+v", l.Dependencies)
	}
	if e.Source != "local" {
		t.Errorf("expected local source, got %q", e.Source)
	}
	if e.Path != dir {
		t.Errorf("expected path %q, got %q", dir, e.Path)
	}
	if e.ContentHash == "" {
		t.Errorf("expected contentHash to be set")
	}
	if e.Digest != "" {
		t.Errorf("local dep must not carry digest: %q", e.Digest)
	}
}

// TestBuildLockReferences covers config AND policy reference entries. The
// referenced bundles are fetched (transitive closure), so Version is populated.
func TestBuildLockReferences(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if strings.Contains(ref, "/cfg") {
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "cfg", Version: "1.0.0"}}}, nil
			}
			return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "sec", Version: "2.0.0"}}}, nil
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "cfg", Ref: "oci://ghcr.io/acme/cfg:1.0.0"},
		},
		Policies: []contract.Policy{
			{Name: "sec", Ref: "oci://ghcr.io/acme/sec:2.0.0"},
		},
	}}
	l, err := s.buildLock(context.Background(), "testdata/root", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	if len(l.References) != 2 {
		t.Fatalf("expected 2 references, got %d: %+v", len(l.References), l.References)
	}
	cfg, ok := l.Reference("config", "cfg")
	if !ok {
		t.Fatalf("config ref cfg missing: %+v", l.References)
	}
	if cfg.Source != "oci" || cfg.Ref != "oci://ghcr.io/acme/cfg:1.0.0" || cfg.Digest == "" || cfg.Version != "1.0.0" {
		t.Errorf("config ref wrong: %+v", cfg)
	}
	pol, ok := l.Reference("policy", "sec")
	if !ok {
		t.Fatalf("policy ref sec missing: %+v", l.References)
	}
	if pol.Source != "oci" || pol.Ref != "oci://ghcr.io/acme/sec:2.0.0" || pol.Digest == "" || pol.Version != "2.0.0" {
		t.Errorf("policy ref wrong: %+v", pol)
	}
}

// TestBuildLockLocalReference covers the local-reference branch (contentHash).
func TestBuildLockLocalReference(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "localcfg", Ref: dir},
		},
	}}
	l, err := s.buildLock(context.Background(), ".", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	ref, ok := l.Reference("config", "localcfg")
	if !ok {
		t.Fatalf("local config ref missing: %+v", l.References)
	}
	if ref.Source != "local" || ref.Path != dir || ref.ContentHash == "" || ref.Version != "1.0.0" {
		t.Errorf("local ref wrong: %+v", ref)
	}
}

func TestBuildLockConflict(t *testing.T) {
	store := conflictStore()
	s := NewService(store, nil)
	_, err := s.buildLock(context.Background(), "testdata/root", conflictRootBundle(), nil)
	var ce *lock.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *lock.ConflictError, got %v", err)
	}
	if ce.Service != "shared" {
		t.Errorf("conflict service: got %q want %q", ce.Service, "shared")
	}
}

// conflictRootBundle declares two deps that both pull a shared downstream
// service at incompatible pinned versions, producing a graph conflict.
func conflictRootBundle() *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "a", Ref: "oci://ghcr.io/acme/a:1.0.0", Compatibility: "^1.0.0", Required: true},
			{Name: "b", Ref: "oci://ghcr.io/acme/b:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
}

func conflictStore() *testutil.MockBundleStore {
	return &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			switch ref {
			case "ghcr.io/acme/a:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service: contract.Service{Name: "a", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Name: "shared", Ref: "oci://ghcr.io/acme/shared:2.0.0", Compatibility: "^2.0.0", Required: true},
					},
				}}, nil
			case "ghcr.io/acme/b:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service: contract.Service{Name: "b", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Name: "shared", Ref: "oci://ghcr.io/acme/shared:3.0.0", Compatibility: "^3.0.0", Required: true},
					},
				}}, nil
			case "ghcr.io/acme/shared:2.0.0":
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "shared", Version: "2.0.0"}}}, nil
			case "ghcr.io/acme/shared:3.0.0":
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "shared", Version: "3.0.0"}}}, nil
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
	}
}

// splitCycleStore serves the concurrent split-cycle graph:
// root -> a, root -> c; a -> b; b -> c; c -> b. The b<->c back-edge is split
// across the two sibling branches under root, which resolve concurrently.
func splitCycleStore() *testutil.MockBundleStore {
	return &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			dep := func(name, r string) contract.Dependency {
				return contract.Dependency{Name: name, Ref: r, Compatibility: "^1.0.0", Required: true}
			}
			switch ref {
			case "ghcr.io/acme/a:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service:      contract.Service{Name: "a", Version: "1.0.0"},
					Dependencies: []contract.Dependency{dep("b", "oci://ghcr.io/acme/b:1.0.0")},
				}}, nil
			case "ghcr.io/acme/b:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service:      contract.Service{Name: "b", Version: "1.0.0"},
					Dependencies: []contract.Dependency{dep("c", "oci://ghcr.io/acme/c:1.0.0")},
				}}, nil
			case "ghcr.io/acme/c:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service:      contract.Service{Name: "c", Version: "1.0.0"},
					Dependencies: []contract.Dependency{dep("b", "oci://ghcr.io/acme/b:1.0.0")},
				}}, nil
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
	}
}

func splitCycleRootBundle() *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "a", Ref: "oci://ghcr.io/acme/a:1.0.0", Compatibility: "^1.0.0", Required: true},
			{Name: "c", Ref: "oci://ghcr.io/acme/c:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
}

// TestBuildLockSplitCycleFailsClosed is the regression test for the concurrent
// split-cycle defect: the b<->c cycle is discovered across two sibling branches
// that resolve concurrently, so the inline path-check could dedup it to Shared
// edges and let the lock build "successfully". The deterministic post-resolution
// cycle pass must mark a back-edge so buildLock fails closed with
// LOCK_UNRESOLVED — on every run, not by scheduling luck.
func TestBuildLockSplitCycleFailsClosed(t *testing.T) {
	for i := 0; i < 50; i++ {
		s := NewService(splitCycleStore(), nil)
		_, err := s.buildLock(context.Background(), "testdata/root", splitCycleRootBundle(), nil)
		var ue *lock.UnresolvedError
		if !errors.As(err, &ue) {
			t.Fatalf("run %d: expected *lock.UnresolvedError for split cycle, got %v", i, err)
		}
		if !strings.Contains(ue.Reason, "cycle detected") {
			t.Fatalf("run %d: unresolved reason not a cycle: %q", i, ue.Reason)
		}
	}
}

// TestBuildLockUnresolvedFailedEdge covers a required dep whose fetch fails:
// the graph records Error and a nil Node, and buildLock must fail closed.
func TestBuildLockUnresolvedFailedEdge(t *testing.T) {
	store := &testutil.MockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return nil, fmt.Errorf("registry unreachable")
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
	if ue.Ref != "oci://ghcr.io/acme/auth:1.0.0" {
		t.Errorf("unresolved ref: got %q", ue.Ref)
	}
}

// TestBuildLockUnresolvedDigestError covers the resolveDigest error path:
// the node resolves (fetched) but the subsequent digest resolution fails.
func TestBuildLockUnresolvedDigestError(t *testing.T) {
	store := &testutil.MockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "auth", Version: "1.0.0"}}}, nil
		},
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", fmt.Errorf("manifest not found")
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// TestBuildLockUnresolvedReferenceDigestError covers the reference OCI digest
// error path (a reference whose digest resolution fails fails closed).
func TestBuildLockUnresolvedReferenceDigestError(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", fmt.Errorf("manifest not found")
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Policies: []contract.Policy{
			{Name: "sec", Ref: "oci://ghcr.io/acme/sec:2.0.0"},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// TestBuildLockUnresolvedFailedReferenceLoad covers a local reference whose
// bundle fails to load (non-existent path) -> fails closed with UnresolvedError.
func TestBuildLockUnresolvedReferenceLoadError(t *testing.T) {
	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			// Non-existent local path so loadLocalBundle fails -> failed edge.
			{Name: "missing-local", Ref: "/nonexistent/path/xyz", Compatibility: "", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// TestBuildLockUnresolvedHashError covers the HashFS error branch: a resolved
// local dependency whose FS walk errors (unreadable subdirectory) fails closed.
func TestBuildLockUnresolvedHashError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based FS walk error not reproducible as root")
	}
	dir := testutil.WriteTestBundle(t)
	// Make the docs subdir unreadable so fs.WalkDir returns a permission error
	// when HashFS walks the bundle FS produced by loadLocalBundle.
	bad := filepath.Join(dir, "docs")
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "local-dep", Ref: dir, Compatibility: "", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// TestBuildLockTransitiveFailure covers firstFailedEdge recursing into a child
// node to surface a grandchild's failed edge (root -> auth ok -> db fails).
func TestBuildLockTransitiveFailure(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			switch ref {
			case "ghcr.io/acme/auth:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service: contract.Service{Name: "auth", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Name: "db", Ref: "oci://ghcr.io/acme/db:2.0.0", Compatibility: "^2.0.0", Required: true},
					},
				}}, nil
			case "ghcr.io/acme/db:2.0.0":
				return nil, fmt.Errorf("db registry unreachable")
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
	if ue.Ref != "oci://ghcr.io/acme/db:2.0.0" {
		t.Errorf("unresolved ref: got %q want db", ue.Ref)
	}
}

// TestBuildLockSecondEntryDigestError covers the walkClosure buildErr guard:
// with two resolved OCI deps, the first's digest resolution fails, setting
// buildErr; the second visit must short-circuit on the guard.
func TestBuildLockSecondEntryDigestError(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) {
			if ref == "ghcr.io/acme/a:1.0.0" {
				return "", fmt.Errorf("manifest not found")
			}
			return "sha256:" + ref, nil
		},
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			switch ref {
			case "ghcr.io/acme/a:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "a", Version: "1.0.0"}}}, nil
			case "ghcr.io/acme/b:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "b", Version: "1.0.0"}}}, nil
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "a", Ref: "oci://ghcr.io/acme/a:1.0.0", Compatibility: "^1.0.0", Required: true},
			{Name: "b", Ref: "oci://ghcr.io/acme/b:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// TestBuildLockLocalReferenceLoadError covers a local reference whose bundle
// fails to load (non-existent path) -> fails closed.
func TestBuildLockLocalReferenceLoadError(t *testing.T) {
	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "badcfg", Ref: "/nonexistent/ref/path"},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// TestBuildLockLocalReferenceHashError covers the HashFS error branch for a
// local reference (unreadable subdirectory in the referenced bundle).
func TestBuildLockLocalReferenceHashError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based FS walk error not reproducible as root")
	}
	dir := testutil.WriteTestBundle(t)
	bad := filepath.Join(dir, "docs")
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "localcfg", Ref: dir},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root, nil)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// policyRefContract returns a contract named n declaring the given policy refs.
func policyRefContract(name, version string, policyRefs ...string) *contract.Contract {
	c := &contract.Contract{Service: contract.Service{Name: name, Version: version}}
	for _, r := range policyRefs {
		c.Policies = append(c.Policies, contract.Policy{Name: name + "-pol", Ref: r})
	}
	return c
}

// TestBuildReferenceClosureTransitive covers the transitive OCI jump
// root -> P -> Q: Q is pinned with both version and digest.
func TestBuildReferenceClosureTransitive(t *testing.T) {
	pContract := policyRefContract("p", "1.0.0", "oci://r/q")
	qContract := policyRefContract("q", "2.0.0")
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.0.0", "2.0.0"}, nil },
		ResolveFn:  func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if strings.Contains(ref, "/p") {
				return &contract.Bundle{Contract: pContract}, nil
			}
			return &contract.Bundle{Contract: qContract}, nil
		},
	}
	s := NewService(store, nil)
	root := policyRefContract("root", "0.1.0", "oci://r/p")
	refs, err := s.buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 references (p, q), got %d: %+v", len(refs), refs)
	}
	byRef := map[string]lock.Reference{}
	for _, r := range refs {
		byRef[r.Ref] = r
	}
	q := byRef["oci://r/q"]
	if q.Version != "2.0.0" || q.Digest == "" || q.Kind != "policy" {
		t.Errorf("transitive Q not pinned correctly: %+v", q)
	}
}

// TestBuildReferenceClosureCycle covers a P<->Q cycle: dedupe by declared ref
// string terminates the walk, each pinned exactly once.
func TestBuildReferenceClosureCycle(t *testing.T) {
	pContract := policyRefContract("p", "1.0.0", "oci://r/q")
	qContract := policyRefContract("q", "1.0.0", "oci://r/p")
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.0.0"}, nil },
		ResolveFn:  func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if strings.Contains(ref, "/p") {
				return &contract.Bundle{Contract: pContract}, nil
			}
			return &contract.Bundle{Contract: qContract}, nil
		},
	}
	s := NewService(store, nil)
	root := policyRefContract("root", "0.1.0", "oci://r/p")
	refs, err := s.buildReferenceClosure(context.Background(), root, "")
	if err != nil {
		t.Fatalf("cycle should terminate: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want p and q once each, got %d", len(refs))
	}
}

// TestBuildReferenceClosureLocalInsideOCIErrors covers a local ref reached
// inside an OCI-fetched bundle (baseDir == "") -> *lock.UnresolvedError.
func TestBuildReferenceClosureLocalInsideOCIErrors(t *testing.T) {
	pContract := policyRefContract("p", "1.0.0", "../local-policy")
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.0.0"}, nil },
		ResolveFn:  func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: pContract}, nil
		},
	}
	s := NewService(store, nil)
	root := policyRefContract("root", "0.1.0", "oci://r/p")
	_, err := s.buildReferenceClosure(context.Background(), root, "")
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("want *lock.UnresolvedError for local ref inside OCI bundle, got %v", err)
	}
}

// TestBuildReferenceClosureLocalAtRoot covers a local ref at the root: the
// declaring bundle dir is a real temp dir and ContentHash + Version are set.
func TestBuildReferenceClosureLocalAtRoot(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	parent := filepath.Dir(bundleDir)
	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Contract{
		Service:        contract.Service{Name: "root", Version: "0.1.0"},
		Configurations: []contract.Configuration{{Name: "localcfg", Ref: "bundle"}},
	}
	refs, err := s.buildReferenceClosure(context.Background(), root, parent)
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("want 1 local reference, got %d: %+v", len(refs), refs)
	}
	r := refs[0]
	if r.Source != "local" || r.ContentHash == "" || r.Version != "1.0.0" || r.Path != "bundle" || r.Kind != "config" {
		t.Errorf("local ref at root wrong: %+v", r)
	}
}

// TestBuildReferenceClosureResolveError covers an OCI resolve failure ->
// *lock.UnresolvedError (fail closed).
func TestBuildReferenceClosureResolveError(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) { return "", fmt.Errorf("manifest not found") },
	}
	s := NewService(store, nil)
	root := policyRefContract("root", "0.1.0", "oci://r/p:1.0.0")
	_, err := s.buildReferenceClosure(context.Background(), root, "")
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("want *lock.UnresolvedError for OCI resolve error, got %v", err)
	}
}

// TestBuildReferenceClosurePullError covers an OCI pull failure (digest
// resolves but Pull fails) -> *lock.UnresolvedError.
func TestBuildReferenceClosurePullError(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return nil, fmt.Errorf("registry unreachable")
		},
	}
	s := NewService(store, nil)
	root := policyRefContract("root", "0.1.0", "oci://r/p:1.0.0")
	_, err := s.buildReferenceClosure(context.Background(), root, "")
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("want *lock.UnresolvedError for OCI pull error, got %v", err)
	}
}

// TestBuildLockOCIRootReferenceBaseDir covers buildLock with an OCI root ref:
// referenceBaseDir returns "" so the closure resolves the root's OCI references
// with no filesystem base.
func TestBuildLockOCIRootReferenceBaseDir(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "sec", Version: "2.0.0"}}}, nil
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.Service{Name: "root", Version: "1.0.0"},
		Policies: []contract.Policy{
			{Name: "sec", Ref: "oci://ghcr.io/acme/sec:2.0.0"},
		},
	}}
	l, err := s.buildLock(context.Background(), "oci://ghcr.io/acme/root:1.0.0", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	pol, ok := l.Reference("policy", "sec")
	if !ok || pol.Version != "2.0.0" || pol.Digest == "" {
		t.Errorf("policy ref wrong: %+v", pol)
	}
}

// TestBuildReferenceClosureLocalTransitive covers a transitive LOCAL reference
// closure: root -> p (local) -> q (local, reached via a relative path from p's
// own directory). Both p and q must be pinned with a ContentHash (no digest) and
// the correct Version read from each referenced bundle's contract.
func TestBuildReferenceClosureLocalTransitive(t *testing.T) {
	rootDir := t.TempDir()
	writeBundleYAML := func(dir, yaml string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	assertRef := func(byName map[string]lock.Reference, name, wantVersion string) {
		t.Helper()
		r, ok := byName[name]
		if !ok {
			t.Fatalf("%s reference missing: %+v", name, byName)
		}
		if r.Source != "local" {
			t.Errorf("%s source: got %q, want local", name, r.Source)
		}
		if r.ContentHash == "" {
			t.Errorf("%s ContentHash empty", name)
		}
		if r.Digest != "" {
			t.Errorf("%s Digest: got %q, want empty", name, r.Digest)
		}
		if r.Version != wantVersion {
			t.Errorf("%s Version: got %q, want %q", name, r.Version, wantVersion)
		}
		if r.Kind != "policy" {
			t.Errorf("%s Kind: got %q, want policy", name, r.Kind)
		}
	}

	// q: a leaf policy bundle.
	writeBundleYAML(filepath.Join(rootDir, "q"), "pactoVersion: \"2.0\"\nservice:\n  name: q\n  version: \"3.1.0\"\n")
	// p: a policy bundle that references q via a path relative to p's own dir.
	writeBundleYAML(filepath.Join(rootDir, "p"),
		"pactoVersion: \"2.0\"\nservice:\n  name: p\n  version: \"2.0.0\"\npolicies:\n  - name: from-p\n    ref: ../q\n")

	s := NewService(&testutil.MockBundleStore{}, nil)
	// root references p locally; baseDir is rootDir so "p" resolves to rootDir/p.
	root := &contract.Contract{
		Service:  contract.Service{Name: "root", Version: "1.0.0"},
		Policies: []contract.Policy{{Name: "from-root", Ref: "p"}},
	}
	refs, err := s.buildReferenceClosure(context.Background(), root, rootDir)
	if err != nil {
		t.Fatalf("buildReferenceClosure: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 references (p, q), got %d: %+v", len(refs), refs)
	}
	byName := map[string]lock.Reference{}
	for _, r := range refs {
		byName[r.Name] = r
	}
	assertRef(byName, "from-root", "2.0.0")
	assertRef(byName, "from-p", "3.1.0")
}

func TestSetBuildVersion(t *testing.T) {
	orig := BuildVersion
	t.Cleanup(func() { BuildVersion = orig })
	SetBuildVersion("9.9.9")
	if BuildVersion != "9.9.9" {
		t.Errorf("SetBuildVersion: got %q", BuildVersion)
	}
}
