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

// TestService_ImpactWithSnapshot_BindsGivenSnapshot proves section 2.2: an impact answer
// produced against an already-published snapshot carries THAT snapshot's id, so a
// dashboard can guarantee the impact result matches the graph the user is viewing
// rather than a freshly rebuilt, divergent one.
func TestService_ImpactWithSnapshot_BindsGivenSnapshot(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: []string{fleetRoot}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ImpactWithSnapshot(context.Background(), ImpactOptions{OldPath: oldDir, NewPath: newDir}, snap)
	if err != nil {
		t.Fatalf("ImpactWithSnapshot: %v", err)
	}
	if snap.SnapshotID == "" || res.SnapshotID != snap.SnapshotID {
		t.Errorf("impact snapshotId %q must equal the given snapshot %q", res.SnapshotID, snap.SnapshotID)
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

func TestService_Impact_WithTraces(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")
	// checkout is a REGISTERED fleet service that does not declare orders — so the
	// observed edge makes it a domain-qualified observed-only (shadow) consumer. An
	// unregistered caller name would instead be preserved as an unresolved
	// limitation, never a phantom default-domain consumer.
	writeImpactService(t, filepath.Join(fleetRoot, "checkout"), "checkout", "1.0.0")

	// A trace where checkout calls orders → an observed-only (shadow) consumer.
	trace := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},
	  "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"orders"}}]}]}]}]}`

	svc := NewService(nil, nil)
	res, err := svc.Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir,
		Fleet:           FleetOptions{LocalRoots: []string{fleetRoot}},
		IncludeObserved: true,
		Traces:          []byte(trace),
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	found := false
	for _, c := range res.Consumers {
		if c.Service == "checkout" && c.Confidence == impact.ConfidenceObserved {
			found = true
		}
	}
	if !found {
		t.Errorf("expected observed-only consumer checkout, got %+v", res.Consumers)
	}
}

func TestService_Impact_BadTraces(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	_, err := svc.Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir,
		Fleet:  FleetOptions{LocalRoots: []string{fleetRoot}},
		Traces: []byte("{bad"),
	})
	if err == nil {
		t.Fatal("expected trace parse error")
	}
}
