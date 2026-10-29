package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
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

func TestRevisionDomain(t *testing.T) {
	tests := []struct {
		name, ref, want string
	}{
		// The bug: a bare multi-segment path resolves as a local bundle but
		// OciDomain alone reads "payments-service" as the artifact under domain
		// "examples/demo", so the changed service never joins the fleet.
		{"bare multi-segment path is local", "examples/demo/payments-service", ""},
		{"file URL is local", "file:///abs/path/payments-service", ""},
		{"dot-relative path is local", "./payments-service/v2.0.0", ""},
		{"absolute path is local", "/abs/payments-service", ""},
		{"oci ref keeps its domain", "oci://ghcr.io/acme/payments:1.0", "ghcr.io/acme"},
		{"oci ref with digest keeps its domain", "oci://localhost:5000/acme/payments@sha256:abc", "localhost:5000/acme"},
		{"single-segment oci ref has the default domain", "oci://payments:1.0", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := revisionDomain(tt.ref); got != tt.want {
				t.Errorf("revisionDomain(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

// TestService_ImpactWithSnapshot_RefusesHiddenService: on a snapshot narrowed
// to the caller, a raw-ref impact on a service the snapshot hides, on either
// side, reads as not found instead of diffing it with the server's
// credentials. A visible service still answers.
func TestService_ImpactWithSnapshot_RefusesHiddenService(t *testing.T) {
	root := t.TempDir()
	write := func(name, version string) string {
		dir := filepath.Join(root, name+"-"+version)
		writeImpactService(t, dir, name, version)
		return dir
	}
	ordersOld, ordersNew := write("orders", "1.0.0"), write("orders", "2.0.0")
	ledgerOld, ledgerNew := write("ledger", "2.0.0"), write("ledger", "2.1.0")
	fleetRoot := t.TempDir()
	writeImpactService(t, filepath.Join(fleetRoot, "orders"), "orders", "2.0.0")

	svc := NewService(nil, nil)
	snap, err := svc.Fleet(context.Background(), FleetOptions{LocalRoots: []string{fleetRoot}})
	if err != nil {
		t.Fatal(err)
	}
	snap.Limitations = append(snap.Limitations, fleet.Limitation{Code: fleet.LimitationHiddenByPermissions})

	for name, o := range map[string]ImpactOptions{
		"hidden new": {OldPath: ledgerOld, NewPath: ledgerNew},
		"hidden old": {OldPath: ledgerOld, NewPath: ordersNew},
	} {
		var nf *oci.ArtifactNotFoundError
		if _, err := svc.ImpactWithSnapshot(context.Background(), o, snap); !errors.As(err, &nf) {
			t.Errorf("%s: err = %v, want not found", name, err)
		}
	}
	if _, err := svc.ImpactWithSnapshot(context.Background(), ImpactOptions{OldPath: ordersOld, NewPath: ordersNew}, snap); err != nil {
		t.Errorf("visible service: %v", err)
	}
}

// writeImpactConsumer writes a bundle `name` declaring a dependency on
// provider with the given range.
func writeImpactConsumer(t *testing.T, dir, name, provider, compat string) {
	t.Helper()
	writeImpactService(t, dir, name, "1.0.0")
	f, err := os.OpenFile(filepath.Join(dir, "pacto.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("dependencies:\n  - name: " + provider + "\n    compatibility: \"" + compat + "\"\n"); err != nil {
		t.Fatal(err)
	}
}

// TestService_Impact_AddsNewRevisionWhenNoSourceHoldsIt proves the evaluation
// trap is closed: a fleet root holding only the consumers used to leave the
// changed service out of the graph and answer with no consumers at all.
func TestService_Impact_AddsNewRevisionWhenNoSourceHoldsIt(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	fleetRoot := t.TempDir()
	writeImpactConsumer(t, filepath.Join(fleetRoot, "webapp"), "webapp", "orders", "^1.0.0")

	res, err := NewService(nil, nil).Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir, Fleet: FleetOptions{LocalRoots: []string{fleetRoot}},
	})
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if !res.NewRevisionAdded {
		t.Error("NewRevisionAdded = false, want true")
	}
	if len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp" || res.Consumers[0].CompatibilityVerdict != impact.CompatibilityIncompatible {
		t.Fatalf("consumers = %+v, want webapp incompatible", res.Consumers)
	}
	for _, l := range res.Limitations {
		if l.Code == "SERVICE_NOT_IN_FLEET" {
			t.Errorf("the added revision still reads as missing: %+v", l)
		}
	}
}

// TestService_Impact_SecondBuildError covers the rebuild failing after the first
// build succeeded: a configured source already claims the added source's id.
func TestService_Impact_SecondBuildError(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeImpactService(t, oldDir, "orders", "1.0.0")
	writeImpactService(t, newDir, "orders", "2.0.0")
	_, err := NewService(nil, nil).Impact(context.Background(), ImpactOptions{
		OldPath: oldDir, NewPath: newDir,
		Fleet: FleetOptions{ObservationSources: []ObservationSourceSpec{{ID: "new-revision", Path: filepath.Join(t.TempDir(), "absent.json")}}},
	})
	if err == nil {
		t.Fatal("expected the rebuild to refuse a duplicate source id")
	}
}

func TestService_ImpactWithSnapshot_ResolveError(t *testing.T) {
	newDir := t.TempDir()
	writeImpactService(t, newDir, "orders", "2.0.0")
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(nil, nil).ImpactWithSnapshot(context.Background(), ImpactOptions{OldPath: "/nonexistent/old", NewPath: newDir}, snap); err == nil {
		t.Error("expected error for a nonexistent old path")
	}
}
