package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/internal/testutil"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/lock"
)

// rootBundleWithDep returns a local root contract that declares one OCI dep.
func rootBundleWithDep() *contract.Bundle {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "root", Version: "2.1.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth", Compatibility: "^1.0.0", Required: true},
		},
	}
	return &contract.Bundle{Contract: c}
}

func TestBuildLockCapturesDigest(t *testing.T) {
	authContract := &contract.Contract{Service: contract.ServiceIdentity{Name: "auth", Version: "1.2.0"}}
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.0"}, nil },
		ResolveFn:  func(_ context.Context, _ string) (string, error) { return "sha256:authdigest", nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: authContract}, nil
		},
	}
	s := NewService(store, nil)

	l, err := s.buildLock(context.Background(), "testdata/root", rootBundleWithDep())
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
					Service: contract.ServiceIdentity{Name: "auth", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Name: "db", Ref: "oci://ghcr.io/acme/db:2.0.0", Compatibility: "^2.0.0", Required: true},
					},
				}}, nil
			case "ghcr.io/acme/db:2.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "db", Version: "2.0.0"},
				}}, nil
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	l, err := s.buildLock(context.Background(), "testdata/root", root)
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "local-dep", Ref: dir, Compatibility: "", Required: true},
		},
	}}
	l, err := s.buildLock(context.Background(), ".", root)
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

// TestBuildLockReferences covers config AND policy reference entries.
func TestBuildLockReferences(t *testing.T) {
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, ref string) (string, error) { return "sha256:" + ref, nil },
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{Name: "cfg", Ref: "oci://ghcr.io/acme/cfg:1.0.0"},
		},
		Policies: []contract.PolicySource{
			{Name: "sec", Ref: "oci://ghcr.io/acme/sec:2.0.0"},
		},
	}}
	l, err := s.buildLock(context.Background(), "testdata/root", root)
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
	if cfg.Source != "oci" || cfg.Ref != "oci://ghcr.io/acme/cfg:1.0.0" || cfg.Digest == "" {
		t.Errorf("config ref wrong: %+v", cfg)
	}
	pol, ok := l.Reference("policy", "sec")
	if !ok {
		t.Fatalf("policy ref sec missing: %+v", l.References)
	}
	if pol.Source != "oci" || pol.Ref != "oci://ghcr.io/acme/sec:2.0.0" || pol.Digest == "" {
		t.Errorf("policy ref wrong: %+v", pol)
	}
}

// TestBuildLockLocalReference covers the local-reference branch (contentHash).
func TestBuildLockLocalReference(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	store := &testutil.MockBundleStore{}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{Name: "localcfg", Ref: dir},
		},
	}}
	l, err := s.buildLock(context.Background(), ".", root)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	ref, ok := l.Reference("config", "localcfg")
	if !ok {
		t.Fatalf("local config ref missing: %+v", l.References)
	}
	if ref.Source != "local" || ref.Path != dir || ref.ContentHash == "" {
		t.Errorf("local ref wrong: %+v", ref)
	}
}

func TestBuildLockConflict(t *testing.T) {
	store := conflictStore()
	s := NewService(store, nil)
	_, err := s.buildLock(context.Background(), "testdata/root", conflictRootBundle())
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
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
					Service: contract.ServiceIdentity{Name: "a", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Name: "shared", Ref: "oci://ghcr.io/acme/shared:2.0.0", Compatibility: "^2.0.0", Required: true},
					},
				}}, nil
			case "ghcr.io/acme/b:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{
					Service: contract.ServiceIdentity{Name: "b", Version: "1.0.0"},
					Dependencies: []contract.Dependency{
						{Name: "shared", Ref: "oci://ghcr.io/acme/shared:3.0.0", Compatibility: "^3.0.0", Required: true},
					},
				}}, nil
			case "ghcr.io/acme/shared:2.0.0":
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "shared", Version: "2.0.0"}}}, nil
			case "ghcr.io/acme/shared:3.0.0":
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "shared", Version: "3.0.0"}}}, nil
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root)
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
			return &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "auth", Version: "1.0.0"}}}, nil
		},
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", fmt.Errorf("manifest not found")
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root)
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Policies: []contract.PolicySource{
			{Name: "sec", Ref: "oci://ghcr.io/acme/sec:2.0.0"},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root)
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			// Non-existent local path so loadLocalBundle fails -> failed edge.
			{Name: "missing-local", Ref: "/nonexistent/path/xyz", Compatibility: "", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root)
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "local-dep", Ref: dir, Compatibility: "", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root)
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
					Service: contract.ServiceIdentity{Name: "auth", Version: "1.0.0"},
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "auth", Ref: "oci://ghcr.io/acme/auth:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root)
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
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "a", Version: "1.0.0"}}}, nil
			case "ghcr.io/acme/b:1.0.0":
				return &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "b", Version: "1.0.0"}}}, nil
			}
			return nil, fmt.Errorf("unexpected ref %q", ref)
		},
	}
	s := NewService(store, nil)
	root := &contract.Bundle{Contract: &contract.Contract{
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Name: "a", Ref: "oci://ghcr.io/acme/a:1.0.0", Compatibility: "^1.0.0", Required: true},
			{Name: "b", Ref: "oci://ghcr.io/acme/b:1.0.0", Compatibility: "^1.0.0", Required: true},
		},
	}}
	_, err := s.buildLock(context.Background(), "testdata/root", root)
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{Name: "badcfg", Ref: "/nonexistent/ref/path"},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root)
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
		Service: contract.ServiceIdentity{Name: "root", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{Name: "localcfg", Ref: dir},
		},
	}}
	_, err := s.buildLock(context.Background(), ".", root)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

func TestReferenceKindAndName(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.ConfigurationSource{{Name: "cfg", Ref: "oci://r/cfg"}},
		Policies: []contract.PolicySource{
			{Name: "sec", Ref: "oci://r/sec"},
			// Ref shared with a config: policy must win (precedence).
			{Name: "shared-pol", Ref: "oci://r/shared"},
		},
	}
	c.Configurations = append(c.Configurations, contract.ConfigurationSource{Name: "shared-cfg", Ref: "oci://r/shared"})

	tests := []struct {
		name     string
		ref      string
		wantKind string
		wantName string
	}{
		{"config", "oci://r/cfg", "config", "cfg"},
		{"policy", "oci://r/sec", "policy", "sec"},
		{"policy precedence over config", "oci://r/shared", "policy", "shared-pol"},
		{"unknown ref defaults to config", "oci://r/unknown", "config", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKind, gotName := referenceKindAndName(c, tt.ref)
			if gotKind != tt.wantKind || gotName != tt.wantName {
				t.Errorf("referenceKindAndName(%q) = (%q,%q), want (%q,%q)", tt.ref, gotKind, gotName, tt.wantKind, tt.wantName)
			}
		})
	}
}

func TestSetBuildVersion(t *testing.T) {
	orig := BuildVersion
	t.Cleanup(func() { BuildVersion = orig })
	SetBuildVersion("9.9.9")
	if BuildVersion != "9.9.9" {
		t.Errorf("SetBuildVersion: got %q", BuildVersion)
	}
}
