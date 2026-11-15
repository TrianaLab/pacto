package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// entityChanges calls pacto_fleet_entity on checkout over a server whose
// fleet reads reached through r, and answers its changes.
func entityChanges(t *testing.T, r ReachedReader, args map[string]any) fleet.ChangeHistoryPreview {
	t.Helper()
	ctx := context.Background()
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(hrefFleet(t)), Opts: FleetOptions{Reached: r}}})
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	s, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	res, err := s.CallTool(ctx, &mcpsdk.CallToolParams{Name: "pacto_fleet_entity", Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	var out struct {
		Service struct {
			Changes fleet.ChangeHistoryPreview `json:"changes"`
		} `json:"service"`
	}
	if err := json.Unmarshal([]byte(toolText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	return out.Service.Changes
}

func TestMCPEntityReachedHTTPNotRead(t *testing.T) {
	h := entityChanges(t, nil, map[string]any{"kind": "service", "key": "checkout", "reached": true})
	if h.Times != fleet.TimesNotRead || h.TimesReason != httpNotRead {
		t.Fatalf("changes = %+v", h)
	}
}

func TestMCPEntityReachedStdioReads(t *testing.T) {
	var asked fleet.ServiceKey
	read := func(_ context.Context, q *fleet.Query, svc fleet.ServiceKey, h *fleet.ChangeHistoryPreview) {
		asked = svc
		q.AttachReached(h, map[string][]fleet.ChangeReach{"sha256:c": {{Stage: "prod", At: "2026-11-01T00:00:00Z", Record: "r1", Verified: true}}}, fleet.TimesRead, "", nil)
	}
	h := entityChanges(t, read, map[string]any{"kind": "service", "key": "checkout", "reached": true})
	if asked != "checkout" || h.Times != fleet.TimesRead || len(h.Items) == 0 || len(h.Items[0].Reached) != 1 || h.Items[0].Reached[0].Record != "r1" {
		t.Fatalf("asked %q, changes = %+v", asked, h)
	}
	// Without reached, the reader is never asked.
	asked = ""
	if h := entityChanges(t, read, map[string]any{"kind": "service", "key": "checkout"}); asked != "" || h.Times != fleet.TimesNotRead {
		t.Fatalf("asked %q, changes = %+v", asked, h)
	}
}
