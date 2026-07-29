package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/impact"
)

// writeImpactService writes a minimal valid bundle for `name` at `version` into
// dir, so old/new revisions can be resolved as local paths.
func writeImpactService(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestService_Impact(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")

	// A fleet root carrying the changed service so it resolves in the graph.
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	res, err := svc.Impact(context.Background(), ImpactOptions{
		OldPath: oldDir,
		NewPath: newDir,
		Fleet:   FleetOptions{LocalRoots: []string{fleetRoot}},
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if res.Service != "orders" {
		t.Errorf("Service = %q, want orders", res.Service)
	}
	if res.SchemaVersion != impact.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", res.SchemaVersion, impact.SchemaVersion)
	}
	if res.OldVersion != "1.0.0" || res.NewVersion != "2.0.0" {
		t.Errorf("versions = %s -> %s, want 1.0.0 -> 2.0.0", res.OldVersion, res.NewVersion)
	}
}

func TestService_Impact_OldPathError(t *testing.T) {
	newDir := t.TempDir()
	writeImpactService(t, newDir, "orders", "2.0.0")
	svc := NewService(nil, nil)
	if _, err := svc.Impact(context.Background(), ImpactOptions{OldPath: "/nonexistent/old", NewPath: newDir}); err == nil {
		t.Error("expected error for a nonexistent old path")
	}
}

func TestService_Impact_NewPathError(t *testing.T) {
	oldDir := t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	svc := NewService(nil, nil)
	if _, err := svc.Impact(context.Background(), ImpactOptions{OldPath: oldDir, NewPath: "/nonexistent/new"}); err == nil {
		t.Error("expected error for a nonexistent new path")
	}
}

func TestService_Impact_FleetError(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")

	// A cancelled context is fatal in fleet.Build, so snapshot assembly fails
	// after both revisions resolve.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := NewService(nil, nil)
	if _, err := svc.Impact(ctx, ImpactOptions{
		OldPath: oldDir,
		NewPath: newDir,
		Fleet:   FleetOptions{LocalRoots: []string{oldDir}},
	}); err == nil {
		t.Error("expected error when the fleet build context is cancelled")
	}
}
