package cli

import (
	"net/http"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPFleetEntitiesTemplateOnlyFromDiscovery: a stdio pacto mcp --fleet
// --discover server answers pacto_fleet_entities template=only with the
// golden paths listed under the prefix, and the answer names the listing gap.
func TestMCPFleetEntitiesTemplateOnlyFromDiscovery(t *testing.T) {
	e, svc := discoverFixture(t, http.StatusNotFound)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(t.Context())
	for k, v := range map[string]string{"fleet": "true", "local": "", "target-snapshot": "off", "discover": e.discoverPrefix()} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	server, err := buildMCPServer(cmd, svc, "v", nil, defaultDeps())
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	res, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "pacto_fleet_entities", Arguments: map[string]any{"template": "only"}})
	if err != nil || res.IsError {
		t.Fatalf("pacto_fleet_entities: %v %+v", err, res)
	}
	text := res.Content[0].(*mcpsdk.TextContent).Text
	for _, want := range []string{"go-http-postgres", "node-worker", "REGISTRY_LISTING_INDEX_ONLY", `"asOf"`, `"completeness": "partial"`} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "billing") {
		t.Errorf("template=only names the service billing:\n%s", text)
	}
}
