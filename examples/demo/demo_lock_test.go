package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// TestDemoLockPinsTheLocalProvider holds catalog-service 1.3.0's committed lock
// to the postgresql bundle beside it: the fleet resolves the configuration
// reference through the lock's contentHash, so an edit to postgresql 16.1.0 that
// skips the lock would silently unresolve the demo's only provisioning request.
func TestDemoLockPinsTheLocalProvider(t *testing.T) {
	l, err := lock.ReadFS(os.DirFS(filepath.Join("bundles", "catalog-service", "v1.3.0")))
	if err != nil || l == nil {
		t.Fatalf("read catalog-service 1.3.0 pacto.lock: %v", err)
	}
	if l.LockVersion < lock.RootOccurrenceLockVersion {
		t.Errorf("lockVersion %d cannot attribute a root reference", l.LockVersion)
	}
	want := hashBundle(t, filepath.Join("bundles", "postgresql", "v16.1.0"))
	for _, r := range l.References {
		if r.From == "" && r.Kind == "config" && r.Name == "database" {
			if r.ContentHash != want {
				t.Errorf("contentHash %s, want %s (lock.HashFS of postgresql 16.1.0): update the lock", r.ContentHash, want)
			}
			return
		}
	}
	t.Fatal("the lock has no root entry for the database configuration reference")
}

// TestDemoConfigRefEdgeResolves builds the fleet `pacto fleet --local
// examples/demo/bundles` builds and checks catalog-service 1.3.0's configuration
// reference resolves to postgresql, and that impact of postgresql 16.0.1 to
// 16.1.0 is complete and lists catalog-service.
func TestDemoConfigRefEdgeResolves(t *testing.T) {
	ctx := context.Background()
	snap, err := fleet.Build(ctx, fleet.BuildOptions{}, fleetsrc.NewLocalSource("local", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rel := range snap.Relationships {
		if rel.Type == fleet.RelationshipConfigRef && rel.To == "database" && rel.FromService == "catalog-service" {
			found = true
			if !rel.Resolved || rel.ToService != "postgresql" {
				t.Errorf("catalog-service database reference: resolved=%v to=%q", rel.Resolved, rel.ToService)
			}
		}
	}
	if !found {
		t.Fatal("no configuration reference from catalog-service in the demo fleet")
	}

	oldC, oldFS := readBundle(t, filepath.Join("bundles", "postgresql", "v16.0.1"))
	newC, newFS := readBundle(t, filepath.Join("bundles", "postgresql", "v16.1.0"))
	res := impact.Analyze(ctx, oldC, newC, oldFS, newFS, snap, impact.Options{})
	if res.Completeness != fleet.CompletenessComplete {
		t.Errorf("impact completeness %q, want complete: %+v", res.Completeness, res.Limitations)
	}
	for _, c := range res.Consumers {
		if c.Service == "catalog-service" {
			if c.CompatibilityVerdict != "compatible" {
				t.Errorf("catalog-service verdict %q, want compatible", c.CompatibilityVerdict)
			}
			return
		}
	}
	t.Error("impact does not list catalog-service")
}

// hashBundle is the content identity the local fleet source gives a bundle dir.
func hashBundle(t *testing.T, dir string) string {
	t.Helper()
	dirFS := os.DirFS(dir)
	m, err := ignore.Load(dirFS)
	if err != nil {
		t.Fatal(err)
	}
	h, err := lock.HashFS(ignore.FS(dirFS, m))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// readBundle parses the contract in dir and returns it with the dir's FS.
func readBundle(t *testing.T, dir string) (*contract.Contract, fs.FS) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return c, os.DirFS(dir)
}
