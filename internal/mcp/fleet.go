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
	"pacto_fleet_explain, plus pacto_impact — a fourth read-only capability that projects a " +
	"semantic contract diff (old→new revision) onto the graph to report the real blast radius " +
	"of a change: breaking changes, affected consumers with confidence and compatibility, " +
	"active targets and owners to review. Distinguish the three tool families: authoring tools " +
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
// When provideImpact is non-nil the pacto_impact tool is registered too; a nil
// provider omits it, so a caller that cannot resolve revisions degrades cleanly.
func NewFleetServer(version string, q *fleet.Query, provideImpact impactProvider, resolverFor PolicyResolverFor) *mcpsdk.Server {
	instructions := baseInstructions
	if q != nil {
		instructions += "\n\n" + fleetInstructions
	}
	server := newServer(version, instructions, resolverFor)
	if q != nil {
		registerFleetTools(server, q, provideImpact)
	}
	return server
}

// registerFleetTools adds the five read-only fleet query tools, plus the
// read-only pacto_impact tool when an impact provider is supplied.
func registerFleetTools(server *mcpsdk.Server, q *fleet.Query, provideImpact impactProvider) {
	mcpsdk.AddTool(server, fleetSearchTool(), fleetSearchHandler(q))
	mcpsdk.AddTool(server, fleetGetTool(), fleetGetHandler(q))
	mcpsdk.AddTool(server, fleetGraphTool(), fleetGraphHandler(q))
	mcpsdk.AddTool(server, fleetStatusTool(), fleetStatusHandler(q))
	mcpsdk.AddTool(server, fleetExplainTool(), fleetExplainHandler(q))
	if provideImpact != nil {
		mcpsdk.AddTool(server, impactTool(), impactHandler(provideImpact))
	}
}

func fleetSearchTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_search",
		Description: "Search logical services in the operational graph. Read-only. " +
			"Returns a bounded, deterministically ordered list with an asOf time and completeness.",
		InputSchema: inputSchema(map[string]property{
			"text":  {Type: "string", Description: "Substring over service name and owner"},
			"owner": {Type: "string", Description: "Filter by owner team, DRI or contact"},
			// Read from the fleet's own table, never a hand-kept copy: the copy had
			// gone stale and omitted Warning and Reference, so an agent that trusted
			// the enum could not ask for two reachable statuses at all, and its
			// per-status sweep quietly excluded those services from every answer.
			"status":         {Type: "string", Description: "Aggregate status", Enum: fleet.CanonicalStatuses()},
			"compliance":     {Type: "string", Description: "Filter to services with a target of this compliance"},
			"source":         {Type: "string", Description: "Filter by observing source id"},
			"scope":          {Type: "string", Description: "Correlate to a target with this scope"},
			"workload":       {Type: "string", Description: "Workload type", Enum: []string{"service", "job", "scheduled"}},
			"has_capability": {Type: "boolean", Description: "Only services declaring a capability"},
			"has_dependency": {Type: "boolean", Description: "Only services declaring a dependency"},
			"ready":          {Type: "boolean", Description: "Only operationally ready services"},
			"not_ready":      {Type: "boolean", Description: "Only services not operationally ready"},
			"limit":          {Type: "integer", Description: "Maximum results (bounded)"},
		}, nil),
	}
}

// fleetSearchArgs mirrors pacto_fleet_search's declared schema. The field names
// are the wire names, so a mistyped argument is rejected against the schema
// before it reaches the query instead of decoding to a zero that reads as
// "filter not requested".
type fleetSearchArgs struct {
	Text          string `json:"text"`
	Owner         string `json:"owner"`
	Status        string `json:"status"`
	Compliance    string `json:"compliance"`
	Source        string `json:"source"`
	Scope         string `json:"scope"`
	Workload      string `json:"workload"`
	HasCapability bool   `json:"has_capability"`
	HasDependency bool   `json:"has_dependency"`
	Ready         bool   `json:"ready"`
	NotReady      bool   `json:"not_ready"`
	Limit         int    `json:"limit"`
}

func fleetSearchHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetSearchArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetSearchArgs) (*mcpsdk.CallToolResult, any, error) {
		res, err := q.Search(fleet.SearchFilter{
			Text: a.Text, Owner: a.Owner, Status: a.Status, Compliance: a.Compliance,
			Source: a.Source, Workload: a.Workload, Scope: a.Scope,
			HasCapability: a.HasCapability, HasDependency: a.HasDependency,
			ReadyOnly: a.Ready, NotReady: a.NotReady, Limit: a.Limit,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(res)
		return r, nil, err
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

type fleetGetArgs struct {
	Service string `json:"service"`
	Target  string `json:"target"`
}

func fleetGetHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetGetArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetGetArgs) (*mcpsdk.CallToolResult, any, error) {
		var (
			res any
			err error
		)
		switch {
		case a.Target != "":
			res, err = q.GetTarget(a.Target)
		case a.Service != "":
			res, err = q.GetService(a.Service)
		default:
			// Exactly one of the two is required, which no schema keyword the
			// declared map can carry expresses, so the handler still checks it.
			return errorResult(fmt.Errorf("provide either 'service' or 'target'")), nil, nil
		}
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(res)
		return r, nil, err
	}
}

func fleetGraphTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_graph",
		Description: "Traverse fleet dependencies or dependents from a service. Cycle-safe. " +
			"Reports reached nodes with depth and path, cycles and unresolved dependencies. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"service":    {Type: "string", Description: "Root logical service name (aggregates across its revisions)"},
			"revision":   {Type: "string", Description: "Root a specific contract revision key (exact, not latest)"},
			"target":     {Type: "string", Description: "Root the revision linked to this operational target key or name"},
			"direction":  {Type: "string", Description: "Traversal direction", Enum: []string{"dependencies", "dependents"}},
			"transitive": {Type: "boolean", Description: "Traverse transitively"},
			"max_depth":  {Type: "integer", Description: "Maximum transitive depth (0 = unlimited)"},
		}, nil),
	}
}

type fleetGraphArgs struct {
	Service    string `json:"service"`
	Revision   string `json:"revision"`
	Target     string `json:"target"`
	Direction  string `json:"direction"`
	Transitive bool   `json:"transitive"`
	MaxDepth   int    `json:"max_depth"`
}

func fleetGraphHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetGraphArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetGraphArgs) (*mcpsdk.CallToolResult, any, error) {
		dir := fleet.DirectionDependencies
		if a.Direction == "dependents" {
			dir = fleet.DirectionDependents
		}
		res, err := q.Graph(fleet.GraphQuery{
			Service:    a.Service,
			Revision:   fleet.RevisionKey(a.Revision),
			Target:     a.Target,
			Direction:  dir,
			Transitive: a.Transitive, MaxDepth: a.MaxDepth,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(res)
		return r, nil, err
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

type fleetStatusArgs struct {
	NeedsAttention   bool `json:"needs_attention"`
	NonCompliant     bool `json:"non_compliant"`
	Unknown          bool `json:"unknown"`
	Invalid          bool `json:"invalid"`
	Stale            bool `json:"stale"`
	MissingReadiness bool `json:"missing_readiness"`
	UnresolvedDeps   bool `json:"unresolved_deps"`
	Limit            int  `json:"limit"`
}

func fleetStatusHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetStatusArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetStatusArgs) (*mcpsdk.CallToolResult, any, error) {
		r, err := jsonResult(q.Status(fleet.StatusQuery{
			NeedsAttention: a.NeedsAttention, NonCompliant: a.NonCompliant,
			Unknown: a.Unknown, Invalid: a.Invalid,
			StaleEvidence: a.Stale, MissingReadiness: a.MissingReadiness,
			UnresolvedDeps: a.UnresolvedDeps, Limit: a.Limit,
		}))
		return r, nil, err
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

type fleetExplainArgs struct {
	Subject string `json:"subject"`
}

func fleetExplainHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetExplainArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetExplainArgs) (*mcpsdk.CallToolResult, any, error) {
		res, err := q.Explain(a.Subject)
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(res)
		return r, nil, err
	}
}
