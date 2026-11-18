package cli

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
)

// TestMCPCatalogIsTheServers: pactify over MCP classifies with the catalog
// the server was started with; a tag leaves the default alone and says why.
func TestMCPCatalogIsTheServers(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	if a := mcpAuthoring(cmd, svc); a.Remote || a.Catalog == nil || a.CatalogUnavailable != "" {
		t.Fatalf("stdio authoring: %+v", a)
	}
	if err := cmd.Flags().Set("external-catalog", "oci://example.com/cat:latest"); err != nil {
		t.Fatal(err)
	}
	cat, why := mcpCatalog(cmd, svc)
	if cat != nil || !strings.Contains(why, "pin it by digest") || svc.ExternalCatalogRef != "oci://example.com/cat:latest" {
		t.Fatalf("tag: %v %q", cat, why)
	}
}

// TestMCPServerRefusesATagAtStartup: a tag stops the server before any tool
// is served, with or without --fleet.
func TestMCPServerRefusesATagAtStartup(t *testing.T) {
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	if err := cmd.Flags().Set("external-catalog", "oci://example.com/cat:latest"); err != nil {
		t.Fatal(err)
	}
	if _, err := buildMCPServer(cmd, svc, "v", nil, defaultDeps()); err == nil || !strings.Contains(err.Error(), "pin it by digest") {
		t.Fatalf("started: %v", err)
	}
}
