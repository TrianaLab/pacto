package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// fleetInstructions describe the read-only fleet query tools and how they differ
// from the authoring and generated-service tool families.
const fleetInstructions = "Pacto also exposes READ-ONLY fleet tools over the operational graph: " +
	"pacto_fleet_search, pacto_fleet_get, pacto_fleet_graph, pacto_fleet_status and " +
	"pacto_fleet_explain. Distinguish the three tool families: authoring tools " +
	"(pacto_create/edit/check/schema) create and check contracts; generated service " +
	"tools (derived from a bundle's OpenAPI interfaces) invoke LIVE service operations; " +
	"fleet tools understand the operational system and its state. Fleet tools only " +
	"OBSERVE — they never modify contracts, deploy, invoke services or grant " +
	"authorization; Pacto does not determine authorization. Every fleet answer includes " +
	"'asOf' and 'completeness'. Treat a 'partial' or stale answer as incomplete " +
	"knowledge: a missing result does not prove absence when source coverage is " +
	"incomplete."

// NewFleetServer builds a server with the authoring tools plus the read-only
// fleet query tools backed by q. When q is nil, only authoring tools register
// (identical to NewServer) so a caller with no fleet sources degrades cleanly.
func NewFleetServer(version string, q *fleet.Query) *mcpsdk.Server {
	instructions := baseInstructions
	if q != nil {
		instructions += "\n\n" + fleetInstructions
	}
	server := newServer(version, instructions)
	if q != nil {
		registerFleetTools(server, q)
	}
	return server
}

// registerFleetTools adds the five read-only fleet query tools.
func registerFleetTools(server *mcpsdk.Server, q *fleet.Query) {
	server.AddTool(fleetSearchTool(), fleetSearchHandler(q))
	server.AddTool(fleetGetTool(), fleetGetHandler(q))
	server.AddTool(fleetGraphTool(), fleetGraphHandler(q))
	server.AddTool(fleetStatusTool(), fleetStatusHandler(q))
	server.AddTool(fleetExplainTool(), fleetExplainHandler(q))
}

func fleetSearchTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_search",
		Description: "Search logical services in the operational graph. Read-only. " +
			"Returns a bounded, deterministically ordered list with an asOf time and completeness.",
		InputSchema: inputSchema(map[string]property{
			"text":           {Type: "string", Description: "Substring over service name and owner"},
			"owner":          {Type: "string", Description: "Filter by owner team, DRI or contact"},
			"status":         {Type: "string", Description: "Aggregate status", Enum: []string{"Compliant", "NonCompliant", "Unknown", "Invalid", "NotEvaluated"}},
			"compliance":     {Type: "string", Description: "Filter to services with a target of this compliance"},
			"source":         {Type: "string", Description: "Filter by observing source id"},
			"workload":       {Type: "string", Description: "Workload type", Enum: []string{"service", "job", "scheduled"}},
			"has_capability": {Type: "boolean", Description: "Only services declaring a capability"},
			"has_dependency": {Type: "boolean", Description: "Only services declaring a dependency"},
			"ready":          {Type: "boolean", Description: "Only operationally ready services"},
			"not_ready":      {Type: "boolean", Description: "Only services not operationally ready"},
			"limit":          {Type: "integer", Description: "Maximum results (bounded)"},
		}, nil),
	}
}

func fleetSearchHandler(q *fleet.Query) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		f := fleet.SearchFilter{
			Text: parseInput(req, "text"), Owner: parseInput(req, "owner"),
			Status: parseInput(req, "status"), Compliance: parseInput(req, "compliance"),
			Source: parseInput(req, "source"), Workload: parseInput(req, "workload"),
			HasCapability: parseInputBool(req, "has_capability"), HasDependency: parseInputBool(req, "has_dependency"),
			ReadyOnly: parseInputBool(req, "ready"), NotReady: parseInputBool(req, "not_ready"),
			Limit: intOrZero(parseInputIntPtr(req, "limit")),
		}
		return jsonResult(q.Search(f))
	}
}

func fleetGetTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_get",
		Description: "Inspect a logical service (revisions, targets, dependencies, dependents, tools, skills) " +
			"or an operational target (compliance, findings, coverage, freshness). Read-only.",
		InputSchema: inputSchema(map[string]property{
			"service": {Type: "string", Description: "Logical service name"},
			"target":  {Type: "string", Description: "Operational target key or name (use instead of service)"},
		}, nil),
	}
}

func fleetGetHandler(q *fleet.Query) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if target := parseInput(req, "target"); target != "" {
			tv, err := q.GetTarget(target)
			if err != nil {
				return errorResult(err), nil
			}
			return jsonResult(tv)
		}
		service := parseInput(req, "service")
		if service == "" {
			return errorResult(fmt.Errorf("provide either 'service' or 'target'")), nil
		}
		sv, err := q.GetService(service)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(sv)
	}
}

func fleetGraphTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_graph",
		Description: "Traverse fleet dependencies or dependents from a service. Cycle-safe. " +
			"Reports reached nodes with depth and path, cycles and unresolved dependencies. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"service":    {Type: "string", Description: "Root service name"},
			"direction":  {Type: "string", Description: "Traversal direction", Enum: []string{"dependencies", "dependents"}},
			"transitive": {Type: "boolean", Description: "Traverse transitively"},
			"max_depth":  {Type: "integer", Description: "Maximum transitive depth (0 = unlimited)"},
		}, []string{"service"}),
	}
}

func fleetGraphHandler(q *fleet.Query) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		dir := fleet.DirectionDependencies
		if parseInput(req, "direction") == "dependents" {
			dir = fleet.DirectionDependents
		}
		res, err := q.Graph(fleet.GraphQuery{
			Service: parseInput(req, "service"), Direction: dir,
			Transitive: parseInputBool(req, "transitive"), MaxDepth: intOrZero(parseInputIntPtr(req, "max_depth")),
		})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(res)
	}
}

func fleetStatusTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_status",
		Description: "Report services and targets needing attention: non-compliant, unknown, invalid, " +
			"stale evidence, missing readiness, unresolved dependencies. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"needs_attention":   {Type: "boolean", Description: "Report every attention category"},
			"non_compliant":     {Type: "boolean", Description: "Non-compliant targets"},
			"unknown":           {Type: "boolean", Description: "Targets with unknown compliance"},
			"invalid":           {Type: "boolean", Description: "Structurally invalid contracts"},
			"stale":             {Type: "boolean", Description: "Targets with stale evidence"},
			"missing_readiness": {Type: "boolean", Description: "Revisions without a readiness assessment"},
			"unresolved_deps":   {Type: "boolean", Description: "Unresolved declared dependencies"},
			"limit":             {Type: "integer", Description: "Maximum results (bounded)"},
		}, nil),
	}
}

func fleetStatusHandler(q *fleet.Query) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return jsonResult(q.Status(fleet.StatusQuery{
			NeedsAttention: parseInputBool(req, "needs_attention"), NonCompliant: parseInputBool(req, "non_compliant"),
			Unknown: parseInputBool(req, "unknown"), Invalid: parseInputBool(req, "invalid"),
			StaleEvidence: parseInputBool(req, "stale"), MissingReadiness: parseInputBool(req, "missing_readiness"),
			UnresolvedDeps: parseInputBool(req, "unresolved_deps"), Limit: intOrZero(parseInputIntPtr(req, "limit")),
		}))
	}
}

func fleetExplainTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_explain",
		Description: "Explain the deterministic reasons for a service or target state (findings, missing " +
			"evidence, staleness, unresolved dependencies). Returns structured reasons, not prose. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"subject": {Type: "string", Description: "Service name or operational target key/name"},
		}, []string{"subject"}),
	}
}

func fleetExplainHandler(q *fleet.Query) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		res, err := q.Explain(parseInput(req, "subject"))
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(res)
	}
}

// intOrZero dereferences an optional int, defaulting to 0.
func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
