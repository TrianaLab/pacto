package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestLoadSnapshotProducesACommand(t *testing.T) {
	svc := app.NewService(nil, nil)
	c := &Context{
		Ctx:   context.Background(),
		Svc:   svc,
		Fleet: app.FleetOptions{},
		Send:  &sender{},
	}
	cmd := loadSnapshot(c)
	if cmd == nil {
		t.Fatal("loadSnapshot returned nil command")
	}
	msg := cmd()
	sm, ok := msg.(snapshotMsg)
	if !ok {
		t.Fatalf("command produced %T, want snapshotMsg", msg)
	}
	if sm.err != nil {
		t.Fatalf("unexpected error: %v", sm.err)
	}
	if sm.snap == nil {
		t.Fatal("want a non-nil snapshot")
	}
}

func TestLoadSnapshotWithCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc := app.NewService(nil, nil)
	c := &Context{
		Ctx:   ctx,
		Svc:   svc,
		Fleet: app.FleetOptions{LocalRoots: []string{t.TempDir()}},
		Send:  &sender{},
	}
	got := loadSnapshot(c)()
	msg, ok := got.(snapshotMsg)
	if !ok {
		t.Fatalf("got %T, want snapshotMsg", got)
	}
	if msg.err == nil {
		t.Fatal("want an error for cancelled context")
	}
}

func TestSnapshotMsgInstallsTheQueryAndSwapsTheScreen(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(snapshotMsg{snap: testSnapshot(t)})
	got := next.(*Model)
	if got.ctx.Query == nil {
		t.Fatal("Query was not installed on the context")
	}
	if got.top().Title() == "Loading" {
		t.Fatal("still on the loading screen after the snapshot arrived")
	}
	if len(got.stack) != 1 {
		t.Fatalf("stack depth = %d, want 1 — the loading screen must be replaced, not pushed over", len(got.stack))
	}
}

func TestSnapshotMsgWithAnErrorSurfacesItAndStops(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(snapshotMsg{err: errBoom})
	got := next.(*Model)
	if got.err == nil {
		t.Fatal("the load error was swallowed")
	}
	if got.ctx.Query != nil {
		t.Fatal("a failed load must not install a query")
	}
}

func TestDepResolvedUpdatesTheLoadingScreen(t *testing.T) {
	m := New(testOptions())
	next, _ := m.Update(depResolvedMsg{})
	if !strings.Contains(next.View().Content, "resolved") {
		t.Fatalf("loading screen did not react to depResolvedMsg:\n%s", next.View().Content)
	}
}

func TestLoadSnapshotSuccess(t *testing.T) {
	root := t.TempDir()
	bundleDir := filepath.Join(root, "test-svc")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pactoYAML := "pactoVersion: \"2.0\"\nservice:\n  name: test-svc\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(bundleDir, "pacto.yaml"), []byte(pactoYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	c := &Context{
		Ctx:   context.Background(),
		Svc:   svc,
		Fleet: app.FleetOptions{LocalRoots: []string{root}},
		Send:  &sender{},
	}
	got := loadSnapshot(c)()
	msg, ok := got.(snapshotMsg)
	if !ok {
		t.Fatalf("got %T, want snapshotMsg", got)
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if msg.snap == nil {
		t.Fatal("want a snapshot")
	}
}

// Fixture digests. These are 64 hex characters because that is what
// go-digest accepts: a short stand-in parses as IdentityMalformed, which would
// silently deny every test the IdentityExact path.
const (
	testDigest    = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	anotherDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// testSnapshot builds the fleet every screen test runs against: two services,
// one reached locally and one from a registry, one target each, one carrying an
// error finding and one compliant, a team owner and a DRI owner. It is built
// through fleet.Build over a memory source rather than hand-assembled, because
// FleetSnapshot's dependency indexes are unexported and only Build fills them.
func testSnapshot(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()

	fixedNow := func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }

	// Build two minimal contracts and bundles.
	testContract := &contract.Contract{
		PactoVersion: "2.0",
		Service: contract.Service{
			Name:    "test-svc",
			Version: "1.0.0",
			Owner: contract.Owner{
				Team: "platform",
			},
		},
	}

	anotherContract := &contract.Contract{
		PactoVersion: "2.0",
		Service: contract.Service{
			Name:    "another-svc",
			Version: "2.0.0",
			Owner: contract.Owner{
				DRI: "alice",
			},
		},
	}

	now := fixedNow()
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{
				Bundle:       &contract.Bundle{Contract: testContract},
				RequestedRef: "file:///tmp/test-svc",
				Digest:       testDigest,
			},
			{
				Bundle:       &contract.Bundle{Contract: anotherContract},
				RequestedRef: "oci://example.com/another-svc:2.0.0",
				ResolvedRef:  "oci://example.com/another-svc@" + anotherDigest,
				Digest:       anotherDigest,
			},
		},
		Targets: []fleet.RawTarget{
			{
				Scope:      "default",
				Kind:       "Deployment",
				Name:       "test-svc-deploy",
				Service:    "test-svc",
				Digest:     testDigest,
				Compliance: "NonCompliant",
				EvidenceAt: &now,
				Findings: []finding.Finding{
					{
						Code:     "TEST_FINDING",
						Severity: finding.SeverityError,
						Category: finding.CategoryPolicyViolation,
						Message:  "test finding message",
					},
				},
			},
			{
				Scope:      "production",
				Kind:       "Deployment",
				Name:       "another-svc-deploy",
				Service:    "another-svc",
				Digest:     anotherDigest,
				Compliance: "Compliant",
				EvidenceAt: &now,
			},
		},
	}

	src := fleet.NewMemorySource("test", "memory", col)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return snap
}
