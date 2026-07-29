package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v3/pkg/impact"
)

// impactProvider resolves the old and new contract revisions, builds a fleet
// snapshot and analyzes the change's blast radius over it. It is supplied by the
// command layer (from app.Service.Impact) so this package depends only on the
// pure result type — never on OCI, Kubernetes or the app service.
type impactProvider func(ctx context.Context, oldRef, newRef string, includeObserved bool) (*impact.Result, error)

func impactTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_impact",
		Description: "Analyze the blast radius of a change from an old to a new contract revision, " +
			"projected onto the operational graph. Reports the semantic classification, breaking " +
			"changes, affected consumers (with confidence and compatibility verdict), active targets " +
			"and owners to review. Read-only: it lists review targets, never recommends or takes actions.",
		InputSchema: inputSchema(map[string]property{
			"old_ref":          {Type: "string", Description: "Old contract revision (local directory or oci:// ref)"},
			"new_ref":          {Type: "string", Description: "New contract revision (local directory or oci:// ref)"},
			"include_observed": {Type: "boolean", Description: "Let observed (runtime) relationships raise consumer confidence"},
		}, []string{"old_ref", "new_ref"}),
	}
}

func impactHandler(provide impactProvider) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		res, err := provide(ctx, parseInput(req, "old_ref"), parseInput(req, "new_ref"), parseInputBool(req, "include_observed"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(res)
	}
}
