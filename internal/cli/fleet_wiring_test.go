package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/dashboard"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// TestBuildMCPServer_WithFleet covers the --fleet branch of buildMCPServer: no
// bundle ref, fleet enabled, so a fleet-backed server is returned.
func TestBuildMCPServer_WithFleet(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("fleet", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("local", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	server, err := buildMCPServer(cmd, svc, "v", nil)
	if err != nil {
		t.Fatalf("buildMCPServer --fleet: %v", err)
	}
	if server == nil {
		t.Fatal("expected a fleet MCP server")
	}

	// A cancelled context makes the fleet snapshot build fail, exercising the
	// error branch of the --fleet path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)
	if _, err := buildMCPServer(cmd, svc, "v", nil); err == nil {
		t.Fatal("buildMCPServer --fleet with a cancelled context: expected an error")
	}
}

// TestMCPImpactProvider exercises the pacto_impact provider closure wired into
// the MCP fleet server: a nonexistent old revision makes svc.Impact fail, which
// covers the closure body and its fleet-options capture.
func TestMCPImpactProvider(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(context.Background())
	provide := mcpImpactProvider(cmd, svc)
	if _, err := provide(context.Background(), "/nonexistent/old", "/nonexistent/new", true, ""); err == nil {
		t.Fatal("expected an error resolving nonexistent revisions")
	}
	// A nonexistent traces path is a read error before any resolution.
	if _, err := provide(context.Background(), "/old", "/new", false, "/nonexistent/traces.json"); err == nil {
		t.Fatal("expected a traces read error")
	}
	// A readable traces file is consumed (implying include-observed), then the
	// nonexistent revisions make Impact fail — covering the read-success path.
	tf := filepath.Join(t.TempDir(), "traces.json")
	if err := os.WriteFile(tf, []byte(`{"resourceSpans":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := provide(context.Background(), "/nonexistent/old", "/nonexistent/new", false, tf); err == nil {
		t.Fatal("expected a resolution error after reading traces")
	}
}

// TestImpactProviderForFleet exercises the dashboard /api/fleet/impact provider
// closure. A nonexistent old revision surfaces as an error.
func TestImpactProviderForFleet(t *testing.T) {
	svc := app.NewService(nil, nil)
	provide := impactProviderForFleet(svc, app.FleetOptions{LocalRoots: []string{t.TempDir()}})
	if _, err := provide(context.Background(), "/nonexistent/old", "/nonexistent/new", false); err == nil {
		t.Fatal("expected an error resolving nonexistent revisions")
	}
}

// TestDashboardFleetOptions maps detected sources onto fleet options.
func TestDashboardFleetOptions(t *testing.T) {
	// No sources -> disabled.
	if _, ok := dashboardFleetOptions("", nil, "", &dashboard.DetectResult{}); ok {
		t.Error("expected fleet disabled with no sources")
	}
	// Every source active.
	dr := &dashboard.DetectResult{
		Local: &dashboard.LocalSource{},
		OCI:   &dashboard.OCISource{},
		Cache: &dashboard.CacheSource{},
		K8s:   &dashboard.K8sSource{},
	}
	fopts, ok := dashboardFleetOptions("./svc", []string{"ghcr.io/x/a"}, "prod", dr)
	if !ok {
		t.Fatal("expected fleet enabled")
	}
	if len(fopts.LocalRoots) != 1 || fopts.LocalRoots[0] != "./svc" {
		t.Errorf("LocalRoots = %v", fopts.LocalRoots)
	}
	if len(fopts.OCIRefs) != 1 || fopts.OCIRefs[0] != "ghcr.io/x/a" {
		t.Errorf("OCIRefs = %v", fopts.OCIRefs)
	}
	if !fopts.IncludeCache || !fopts.IncludeK8s || fopts.K8sNamespace != "prod" {
		t.Errorf("cache/k8s wiring wrong: %+v", fopts)
	}
}

// TestManagerFleetProvider covers the dashboard's Manager-backed fleet provider:
// the lazy first build (ErrNoSnapshot -> refresh -> query) and the served-cached
// path, plus the refresh-error path on a cancelled context.
func TestManagerFleetProvider(t *testing.T) {
	svc := app.NewService(nil, nil)
	dir := t.TempDir()
	mgr := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return svc.Fleet(ctx, app.FleetOptions{LocalRoots: []string{dir}})
	}, fleet.ManagerOptions{})
	provider := managerFleetProvider(mgr)

	// First call: no snapshot yet -> coalesced build then query.
	q, err := provider(context.Background())
	if err != nil || q == nil {
		t.Fatalf("first call: q=%v err=%v", q, err)
	}
	// Second call: served from the published snapshot.
	q2, err := provider(context.Background())
	if err != nil || q2.SnapshotID() != q.SnapshotID() {
		t.Fatalf("second call should serve the same snapshot: %v", err)
	}

	// A fresh manager whose first build is triggered with a cancelled context
	// returns the refresh error.
	mgr2 := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
		return svc.Fleet(ctx, app.FleetOptions{LocalRoots: []string{dir}})
	}, fleet.ManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := managerFleetProvider(mgr2)(ctx); err == nil {
		t.Fatal("cancelled first build should return an error")
	}
}
