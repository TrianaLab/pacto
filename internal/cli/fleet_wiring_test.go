package cli

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
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

// TestFleetProviderForRoot covers both the success and error paths of the
// dashboard fleet-provider closure.
func TestFleetProviderForRoot(t *testing.T) {
	svc := app.NewService(nil, nil)
	provider := fleetProviderForRoot(svc, t.TempDir())

	q, err := provider(context.Background())
	if err != nil || q == nil {
		t.Fatalf("provider(ok): q=%v err=%v", q, err)
	}

	// A cancelled context propagates through Build as a fatal error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider(ctx); err == nil {
		t.Fatal("provider(cancelled): expected an error")
	}
}
