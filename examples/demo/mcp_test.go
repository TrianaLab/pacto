package main

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoReportsNoMCPEndpoint: the demo says it serves no MCP endpoint and
// where an installed Pacto serves it.
func TestDemoReportsNoMCPEndpoint(t *testing.T) {
	q := fleet.NewQuery(demoFleet(t))
	var caps struct {
		Installation fleet.Installation `json:"installation"`
	}
	h := demoHandler(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }})
	if code := call(t, h, "GET", "/api/capabilities", "", &caps); code != 200 {
		t.Fatalf("capabilities: %d", code)
	}
	if m := caps.Installation.MCP; m.Served || m.Source != demoMCPSource {
		t.Errorf("mcp %+v", m)
	}
}
