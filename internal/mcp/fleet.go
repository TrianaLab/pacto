package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/dashboard/productview"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// fleetInstructions describe the read-only fleet query tools and how they differ
// from the authoring and generated-service tool families.
const fleetInstructions = "Pacto also exposes READ-ONLY fleet tools over the operational graph: " +
	"pacto_fleet_search, pacto_fleet_get, pacto_fleet_graph, pacto_fleet_status and " +
	"pacto_fleet_explain, plus pacto_impact, a read-only tool that projects a " +
	"semantic contract diff (old→new revision) onto the graph to answer which consumers a " +
	"change really affects. It reports the semantic classification, breaking changes, affected " +
	"consumers with confidence and compatibility, active targets and owners to review. " +
	"Distinguish the three tool families: authoring tools " +
	"(pacto_create/edit/check/schema) create and check contracts; generated service " +
	"tools (derived from a bundle's OpenAPI interfaces) invoke LIVE service operations; " +
	"fleet tools read the operational system and its state. Fleet tools only " +
	"OBSERVE: they never modify contracts, deploy, invoke services or grant " +
	"authorization. A dependency marked 'resolved' only names a provider that exists; read its " +
	"'rangeStatus' before saying the dependency is met. Every fleet answer includes " +
	"'asOf' and 'completeness'. Treat a 'partial' or stale answer as incomplete " +
	"knowledge: when source coverage is incomplete, a missing result means unknown. " +
	"A service's 'offering' names the platform offering it provides to other teams " +
	"(service.offering) and 'summary' says what it is for; search with 'offering' to find " +
	"every provider of one. Offerings are read from contracts and are unrelated to a " +
	"contract's capabilities, which are assertions a service makes about itself. " +
	"pacto_fleet_versions lists an offering's provider versions and which satisfy a range. " +
	"An offering use's provisioning status is observed evidence from a provider adapter; Unknown " +
	"means no current report, never a failure or success. " +
	"Every fleet answer carries 'meta' (pacto_impact carries the same fields at the top level): " +
	"cite asOf, snapshotId and sources, and list limitations when you answer. Each edge carries " +
	"'provenance' (declared, observed or declared+observed) and a declared edge carries " +
	"'reconciliation'; each graph node says how it was reached. Say which kind each fact is: " +
	"declared in a contract, observed at runtime or inferred. Pacto uses 'inferred' in three " +
	"places: a transitive consumer in pacto_impact, a target linked to a revision by tag or " +
	"version match and a relationship provenance reserved for future use. pacto_fleet_entities " +
	"with template=only lists golden paths, the starting points pacto_create takes as 'from'; a " +
	"golden path is never a consumer, so it is absent from impact and offering consumers. Use " +
	"pacto_fleet_entities to list owners, business domains and offerings, pacto_fleet_entity " +
	"for one of them and pacto_fleet_attention for what needs attention, most severe first. " +
	"pacto_fleet_neighborhood lists the services around one service with their owners, for the " +
	"blast radius of a problem. Observed edges from a source of kind mira come from Mira's sampled " +
	"service map: an edge Mira did not see may still exist. For when a problem started and which " +
	"signals degrade, ask Mira's own MCP tools (list_alerts, query_metric, service_map, correlate, " +
	"get_trace and render_rca) with the service names Pacto reports, and cite them. A Mira read " +
	"with no spans is unknown, never healthy."

// FleetQuerySource returns the query the fleet tools answer from: the current
// snapshot. It is asked once per call, so a long session follows a refreshing
// snapshot while each answer comes from exactly one.
type FleetQuerySource func(ctx context.Context) (*fleet.Query, error)

// FleetOptions configures the fleet tools beyond their query source.
type FleetOptions struct {
	// Impact registers pacto_impact; nil omits it, so a caller that cannot
	// resolve revisions degrades cleanly.
	Impact impactProvider
	// View, when set, narrows every fleet tool, impact included, to the calling
	// agent's view of the snapshot.
	View FleetView
	// DashboardURL, when set, is the base URL of a Pacto dashboard: fleet answers
	// then carry an href to each entity's page there.
	DashboardURL string
}

// NewFleetServer builds a server with the authoring tools plus the read-only
// fleet query tools backed by src. When src is nil, only authoring tools
// register (identical to NewServer) so a caller with no fleet sources degrades
// cleanly.
func NewFleetServer(version string, src FleetQuerySource, a Authoring, opts FleetOptions) *mcpsdk.Server {
	instructions := baseInstructions
	if src != nil {
		instructions += "\n\n" + fleetInstructions
	}
	server := newServer(version, instructions, a)
	if src != nil {
		registerFleetTools(server, src, opts)
	}
	return server
}

// FleetView narrows q to what the verified caller may see (caller is nil over
// stdio). An error is answered as the tool's error, never as a smaller answer.
type FleetView func(ctx context.Context, caller *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error)

// viewed is handler h over the current query from src, narrowed by view when
// one is set.
func viewed[A any](src FleetQuerySource, view FleetView, h func(*fleet.Query) mcpsdk.ToolHandlerFor[A, any]) mcpsdk.ToolHandlerFor[A, any] {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest, a A) (*mcpsdk.CallToolResult, any, error) {
		q, err := src(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if view != nil {
			var caller *auth.TokenInfo
			if req != nil && req.Extra != nil {
				caller = req.Extra.TokenInfo
			}
			if q, err = view(ctx, caller, q); err != nil {
				return errorResult(err), nil, nil
			}
		}
		return h(q)(ctx, req, a)
	}
}

// addFleetTool registers one fleet tool: answered from the current (viewed)
// query and, with a dashboard URL, linked to the dashboard.
func addFleetTool[A any](server *mcpsdk.Server, tool *mcpsdk.Tool, src FleetQuerySource, opts FleetOptions, h func(*fleet.Query) mcpsdk.ToolHandlerFor[A, any]) {
	mcpsdk.AddTool(server, tool, linkHrefs(tool.Name, hrefPrefix(opts), viewed(src, opts.View, h)))
}

// hrefPrefix is the dashboard base every href is prefixed with, empty without
// a dashboard URL.
func hrefPrefix(opts FleetOptions) string {
	if opts.DashboardURL == "" {
		return ""
	}
	return strings.TrimRight(opts.DashboardURL, "/") + "/#"
}

// registerFleetTools adds the read-only fleet query tools, plus the
// read-only pacto_impact tool when an impact provider is supplied.
func registerFleetTools(server *mcpsdk.Server, src FleetQuerySource, opts FleetOptions) {
	link := opts.DashboardURL != ""
	addFleetTool(server, fleetSearchTool(), src, opts, fleetSearchHandler)
	addFleetTool(server, fleetGetTool(), src, opts, fleetGetHandler)
	addFleetTool(server, fleetGraphTool(), src, opts, fleetGraphHandler)
	addFleetTool(server, fleetStatusTool(), src, opts, fleetStatusHandler)
	addFleetTool(server, fleetExplainTool(), src, opts, fleetExplainHandler)
	addFleetTool(server, fleetEntitiesTool(), src, opts, func(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetEntitiesArgs, any] {
		return fleetEntitiesHandler(q, link)
	})
	addFleetTool(server, fleetEntityTool(), src, opts, func(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetEntityArgs, any] {
		return fleetEntityHandler(q, link)
	})
	addFleetTool(server, fleetAttentionTool(), src, opts, func(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetAttentionArgs, any] {
		return fleetAttentionHandler(q, link)
	})
	addFleetTool(server, fleetNeighborhoodTool(), src, opts, func(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetNeighborhoodArgs, any] {
		return fleetNeighborhoodHandler(q, link)
	})
	addFleetTool(server, fleetVersionsTool(), src, opts, fleetVersionsHandler)
	if opts.Impact != nil {
		// Impact analyses over the snapshot the other tools answer from (the
		// caller's view of it when there is one), so one session's answers agree.
		mcpsdk.AddTool(server, impactTool(), linkHrefs("pacto_impact", hrefPrefix(opts), viewed(src, opts.View, func(vq *fleet.Query) mcpsdk.ToolHandlerFor[impactArgs, any] {
			return impactHandler(opts.Impact, vq.SnapshotForSerialization())
		})))
	}
}

func fleetSearchTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_search",
		Description: "Search logical services in the operational graph. Read-only. " +
			"Returns a bounded, deterministically ordered list with an asOf time and completeness. " +
			"Each service's 'owner' is the structured owner (team, dri, contacts), as in pacto_fleet_get.",
		InputSchema: inputSchema(map[string]property{
			"text":            {Type: "string", Description: "Substring over service name, summary and owner"},
			"owner":           {Type: "string", Description: "Filter by owner team, DRI or contact"},
			"business_domain": {Type: "string", Description: "Filter by business domain (service.domain), " + fleet.NoBusinessDomain + " for services that declare none, or " + fleet.UnknownBusinessDomain + " for services whose domain Pacto cannot tell"},
			"offering":        {Type: "string", Description: "Filter by the platform offering a service provides (service.offering), such as postgresql or shared-config"},
			// Read from the fleet's own table, never a hand-kept copy: the copy had
			// gone stale and omitted Warning and Reference, so an agent that trusted
			// the enum could not ask for two reachable statuses at all, and its
			// per-status sweep quietly excluded those services from every answer.
			"status":         {Type: "string", Description: "Aggregate status", Enum: fleet.CanonicalStatuses()},
			"compliance":     {Type: "string", Description: "Filter to services with a target of this compliance"},
			"source":         {Type: "string", Description: "Filter by observing source id"},
			"scope":          {Type: "string", Description: "Correlate to a target with this scope"},
			"workload":       {Type: "string", Description: "Workload type", Enum: []string{"service", "job", "scheduled"}},
			"has_capability": {Type: "boolean", Description: "Only services that declare a capability assertion (health, metrics or an extension) about themselves"},
			"has_dependency": {Type: "boolean", Description: "Only services declaring a dependency"},
			"ready":          {Type: "boolean", Description: "Only operationally ready services"},
			"not_ready":      {Type: "boolean", Description: "Only services not operationally ready"},
			"template":       {Type: "string", Description: "only: keep only golden paths (services whose contract declares service.template and that run nowhere); exclude: leave them out. Empty lists every service, golden paths marked 'template'", Enum: []string{fleet.TemplateOnly, fleet.TemplateExclude}},
			"limit":          {Type: "integer", Description: "Maximum results (bounded)"},
			"offset":         {Type: "integer", Description: "Number of results to skip, for paging"},
		}, nil),
	}
}

// fleetSearchArgs mirrors pacto_fleet_search's declared schema. The field names
// are the wire names, so a mistyped argument is rejected against the schema
// before it reaches the query instead of decoding to a zero that reads as
// "filter not requested".
type fleetSearchArgs struct {
	Text           string `json:"text"`
	Owner          string `json:"owner"`
	BusinessDomain string `json:"business_domain"`
	Offering       string `json:"offering"`
	Status         string `json:"status"`
	Compliance     string `json:"compliance"`
	Source         string `json:"source"`
	Scope          string `json:"scope"`
	Workload       string `json:"workload"`
	HasCapability  bool   `json:"has_capability"`
	HasDependency  bool   `json:"has_dependency"`
	Ready          bool   `json:"ready"`
	NotReady       bool   `json:"not_ready"`
	Template       string `json:"template"`
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
}

func fleetSearchHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetSearchArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetSearchArgs) (*mcpsdk.CallToolResult, any, error) {
		res, err := q.Search(fleet.SearchFilter{
			Text: a.Text, Owner: a.Owner, BusinessDomain: a.BusinessDomain, Offering: a.Offering, Status: a.Status, Compliance: a.Compliance,
			Source: a.Source, Workload: a.Workload, Scope: a.Scope,
			HasCapability: a.HasCapability, HasDependency: a.HasDependency,
			ReadyOnly: a.Ready, NotReady: a.NotReady, Template: a.Template, Limit: a.Limit, Offset: a.Offset,
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
		Description: "Inspect a logical service (owner, revisions, targets, dependencies, dependents, tools, skills) " +
			"or an operational target (compliance, findings, coverage, freshness). A dependency's 'resolved' " +
			"only says the provider exists in the fleet; its 'rangeStatus' (satisfied, unsatisfied, unknown) " +
			"says whether the provider version satisfies the declared range. 'owner' has the same shape " +
			"(team, dri, contacts) here and in pacto_fleet_search. Each revision's 'provenance' says whether its " +
			"bundle signature verified against the signer its own contract declares (verified, unverified, " +
			"unsigned, unknown) and lists its OCI referrers, when a registry source checked them; absent means " +
			"no source checked. Read-only.",
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
			"Reports reached nodes with depth, path and 'provenance' (how the edge that reached the node is known: " +
			"declared, observed or declared+observed), cycles, unresolved dependencies and 'truncated'. " +
			"Each edge carries 'provenance' and, for a declared edge, 'reconciliation'. " +
			"A declared edge appears once per contract revision that declares it ('fromRevision'). " +
			"Rooted at a revision or target, a dependencies traversal takes its first step over that revision's declared edges only; deeper steps are service-level and include observed edges. Read-only.",
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
			IncludeObserved: true,
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
			"stale evidence, missing readiness, unresolved dependencies and dependencies whose provider " +
			"version is outside the declared range (DEPENDENCY_RANGE_UNSATISFIED), services with no declared owner, " +
			"undeclared runtime dependencies and running services that declare service.template (GOLDEN_PATH_DEPLOYED). 'total' and 'truncated' say whether the limit cut the list. With no category " +
			"selected, including empty input, it reports every category. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"needs_attention":      {Type: "boolean", Description: "Report every attention category"},
			"non_compliant":        {Type: "boolean", Description: "Non-compliant targets"},
			"unknown":              {Type: "boolean", Description: "Targets with unknown compliance"},
			"invalid":              {Type: "boolean", Description: "Structurally invalid contracts"},
			"stale":                {Type: "boolean", Description: "Targets with stale evidence"},
			"missing_readiness":    {Type: "boolean", Description: "Revisions without a readiness assessment"},
			"unresolved_deps":      {Type: "boolean", Description: "Unresolved declared dependencies"},
			"unsatisfied_ranges":   {Type: "boolean", Description: "Dependencies whose provider version is outside the declared range"},
			"unowned":              {Type: "boolean", Description: "Services with no declared owner"},
			"undeclared":           {Type: "boolean", Description: "Runtime calls no contract declares and callers with no contract"},
			"golden_path_deployed": {Type: "boolean", Description: "Running services that declare service.template (GOLDEN_PATH_DEPLOYED)"},
			"limit":                {Type: "integer", Description: "Maximum results (bounded)"},
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
	// UnsatisfiedRanges selects DEPENDENCY_RANGE_UNSATISFIED items.
	UnsatisfiedRanges bool `json:"unsatisfied_ranges"`
	Unowned           bool `json:"unowned"`
	Undeclared        bool `json:"undeclared"`
	// GoldenPathDeployed selects GOLDEN_PATH_DEPLOYED items.
	GoldenPathDeployed bool `json:"golden_path_deployed"`
	Limit              int  `json:"limit"`
}

func fleetStatusHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetStatusArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetStatusArgs) (*mcpsdk.CallToolResult, any, error) {
		r, err := jsonResult(q.Status(fleet.StatusQuery{
			NeedsAttention: a.NeedsAttention, NonCompliant: a.NonCompliant,
			Unknown: a.Unknown, Invalid: a.Invalid,
			StaleEvidence: a.Stale, MissingReadiness: a.MissingReadiness,
			UnresolvedDeps: a.UnresolvedDeps, UnsatisfiedRanges: a.UnsatisfiedRanges,
			Unowned: a.Unowned, Undeclared: a.Undeclared, GoldenPathDeployed: a.GoldenPathDeployed, Limit: a.Limit,
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

func fleetEntitiesTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_entities",
		Description: "List services, owners, business domains or platform offerings, with paging ('total', " +
			"'truncated', 'nextOffset') and an aggregate over everything matched. Use kind=owner to learn the " +
			"exact owner keys (team:NAME, dri:NAME) that 'owner_key' takes, ownership=unowned for services with " +
			"no declared owner and sort=health for the least healthy first. template=only lists golden paths: " +
			"starting points for pacto_create from. created_from lists the services created from one. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"text":            {Type: "string", Description: "Substring over name, key and owner"},
			"kind":            {Type: "string", Description: "Entity kind (empty lists all four)", Enum: fleet.ProductKinds()},
			"owner":           {Type: "string", Description: "Free-text owner search over team, DRI and contacts"},
			"owner_key":       {Type: "string", Description: "Exact owner key, such as team:platform or dri:alice"},
			"business_domain": {Type: "string", Description: "Exact business domain, " + fleet.NoBusinessDomain + " for none or " + fleet.UnknownBusinessDomain + " for unknown"},
			"offering":        {Type: "string", Description: "Services that provide this platform offering, and the offering itself"},
			"consumes":        {Type: "string", Description: "Services that depend on or reference a provider of this platform offering"},
			"ownership":       {Type: "string", Description: "How a service's revisions declare ownership", Enum: []string{fleet.OwnershipConsistent, fleet.OwnershipConflicting, fleet.OwnershipUnowned}},
			"status":          {Type: "string", Description: "Canonical compliance status", Enum: fleet.CanonicalStatuses()},
			"template":        {Type: "string", Description: "only: keep only golden paths and, with no kind, list services only; exclude: leave golden paths and their revisions out", Enum: []string{fleet.TemplateOnly, fleet.TemplateExclude}},
			"created_from":    {Type: "string", Description: "Services created from this golden path, by service key"},
			"sort":            {Type: "string", Description: "Order: by name, least healthy first or owners with the most services first", Enum: []string{fleet.SortName, fleet.SortHealth, fleet.SortServices}},
			"limit":           {Type: "integer", Description: "Maximum results (bounded)"},
			"offset":          {Type: "integer", Description: "Number of results to skip, for paging"},
		}, nil),
	}
}

type fleetEntitiesArgs struct {
	Text           string `json:"text"`
	Kind           string `json:"kind"`
	Owner          string `json:"owner"`
	OwnerKey       string `json:"owner_key"`
	BusinessDomain string `json:"business_domain"`
	Offering       string `json:"offering"`
	Consumes       string `json:"consumes"`
	Ownership      string `json:"ownership"`
	Status         string `json:"status"`
	Template       string `json:"template"`
	CreatedFrom    string `json:"created_from"`
	Sort           string `json:"sort"`
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
}

func fleetEntitiesHandler(q *fleet.Query, link bool) mcpsdk.ToolHandlerFor[fleetEntitiesArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetEntitiesArgs) (*mcpsdk.CallToolResult, any, error) {
		kinds := fleet.ProductKinds()
		if a.Kind != "" {
			kinds = []string{a.Kind}
		} else if a.Template == fleet.TemplateOnly {
			kinds = []string{string(fleet.KindService)}
		}
		var ks []fleet.EntityKind
		for _, k := range kinds {
			ks = append(ks, fleet.EntityKind(k))
		}
		return productResult(link, productview.ToEntityList)(q.Entities(fleet.EntityFilter{
			Text: a.Text, Kinds: ks, Owner: a.Owner, OwnerKey: a.OwnerKey, BusinessDomain: a.BusinessDomain,
			Offering: a.Offering, Consumes: a.Consumes, Ownership: a.Ownership, Status: a.Status, Sort: a.Sort, Limit: a.Limit, Offset: a.Offset,
			Template: a.Template, CreatedFrom: a.CreatedFrom,
		}))
	}
}

func fleetEntityTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_entity",
		Description: "Inspect one entity's full detail: a service, revision, target, owner, source, business domain " +
			"or platform offering. A domain or owner answer summarizes its services; an offering answer lists " +
			"providers and consumers. Each offering use carries 'provisioning': observed evidence a provider " +
			"adapter reports per environment, Unknown until a provider reports. A status with 'verified' false " +
			"comes from an unsigned target-state file. A golden path's service answer carries 'template': the " +
			"plugins its service.template names, its requirements, the services created from it and the " +
			"pacto_create call. A service created from one carries 'origin', its service.origin as declared, " +
			"and 'creation', a checklist derived from what sources show: each step is unknown until a source " +
			"shows otherwise. Tallies cover every visible item, never the preview: an " +
			"offering's consumers carry 'range' and 'phases' and name up to 3 'failed' consumers; a service carries " +
			"'dependentsStatus'. Hidden services are only counted, in the hidden fields. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"kind": {Type: "string", Description: "Entity kind", Enum: fleet.EntityKinds()},
			"key":  {Type: "string", Description: "Entity key, as pacto_fleet_entities or pacto_fleet_search reports it"},
		}, []string{"kind", "key"}),
	}
}

type fleetEntityArgs struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

func fleetEntityHandler(q *fleet.Query, link bool) mcpsdk.ToolHandlerFor[fleetEntityArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetEntityArgs) (*mcpsdk.CallToolResult, any, error) {
		return productResult(link, productview.ToEntityDetail)(q.EntityDetail(fleet.EntityKind(a.Kind), a.Key))
	}
}

func fleetVersionsTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_versions",
		Description: "List the versions of each provider of a platform offering, newest first, with whether a " +
			"target runs each one and the service lifecycle it declares. With 'range', each version carries " +
			"'rangeStatus' and each provider its 'latestCompatible' version. The list comes from the contract " +
			"revisions the snapshot holds; the registry may publish others. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"offering": {Type: "string", Description: "Platform offering slug, such as postgresql"},
			"provider": {Type: "string", Description: "Only this provider: its key, or a name only one domain uses"},
			"range":    {Type: "string", Description: "Semver range to check each version against, such as ^16.0.0"},
		}, []string{"offering"}),
	}
}

type fleetVersionsArgs struct {
	Offering string `json:"offering"`
	Provider string `json:"provider"`
	Range    string `json:"range"`
}

func fleetVersionsHandler(q *fleet.Query) mcpsdk.ToolHandlerFor[fleetVersionsArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetVersionsArgs) (*mcpsdk.CallToolResult, any, error) {
		res, err := q.OfferingVersions(a.Offering, a.Provider, a.Range)
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(res)
		return r, nil, err
	}
}

func fleetAttentionTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_attention",
		Description: "List what needs attention, most severe first, each item with 'severity', 'category', " +
			"'reason' and 'nextStep', paged ('total', 'truncated', 'nextOffset'), with 'severities' and " +
			"'categories' tallied over every match. category=undeclared lists runtime calls no contract " +
			"declares and callers with no contract. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"category":        {Type: "string", Description: "Attention category", Enum: fleet.AttentionCategoriesList()},
			"severity":        {Type: "string", Description: "Attention severity", Enum: []string{"error", "warning", "info"}},
			"owner":           {Type: "string", Description: "Free-text owner search over team, DRI and contacts"},
			"owner_key":       {Type: "string", Description: "Exact owner key, such as team:platform or dri:alice"},
			"business_domain": {Type: "string", Description: "Exact business domain, " + fleet.NoBusinessDomain + " for none or " + fleet.UnknownBusinessDomain + " for unknown"},
			"service":         {Type: "string", Description: "Items of this service: its key, or a name only one domain uses"},
			"limit":           {Type: "integer", Description: "Maximum results (bounded)"},
			"offset":          {Type: "integer", Description: "Number of results to skip, for paging"},
		}, nil),
	}
}

type fleetAttentionArgs struct {
	Category       string `json:"category"`
	Severity       string `json:"severity"`
	Owner          string `json:"owner"`
	OwnerKey       string `json:"owner_key"`
	BusinessDomain string `json:"business_domain"`
	Service        string `json:"service"`
	Limit          int    `json:"limit"`
	Offset         int    `json:"offset"`
}

func fleetAttentionHandler(q *fleet.Query, link bool) mcpsdk.ToolHandlerFor[fleetAttentionArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetAttentionArgs) (*mcpsdk.CallToolResult, any, error) {
		return productResult(link, productview.ToAttentionList)(q.Attention(fleet.AttentionFilter{
			Category: a.Category, Severity: a.Severity, Owner: a.Owner, OwnerKey: a.OwnerKey,
			BusinessDomain: a.BusinessDomain, Service: a.Service, Limit: a.Limit, Offset: a.Offset,
		}))
	}
}

func fleetNeighborhoodTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_fleet_neighborhood",
		Description: "Show the services around one service: who it depends on and who depends on it, to the " +
			"requested depth, each with its owner and status. Edges say whether they are declared, observed or " +
			"both; views defaults to expected,observed. hiddenDependents counts the services, or callers with no " +
			"contract, that your permissions hide; pacto_fleet_attention with category=undeclared and service lists " +
			"the callers with no contract. Use it " +
			"to find the affected services and the teams that own them. Read-only.",
		InputSchema: inputSchema(map[string]property{
			"service":   {Type: "string", Description: "Service name, or its key when the name is used in more than one domain"},
			"direction": {Type: "string", Description: "Which side to walk (default both)", Enum: []string{"dependencies", "dependents", "both"}},
			"depth":     {Type: "integer", Description: fmt.Sprintf("How many hops to walk (0 = %d, at most %d)", fleet.DefaultNeighborhoodDepth, fleet.MaxNeighborhoodDepth)},
			"views":     {Type: "string", Description: "Comma-separated knowledge views: expected, observed, differences (empty = expected,observed)"},
			"max_nodes": {Type: "integer", Description: fmt.Sprintf("Maximum services (0 = %d, at most %d)", fleet.DefaultMaxNodes, fleet.MaxNeighborhoodNodes)},
		}, []string{"service"}),
	}
}

type fleetNeighborhoodArgs struct {
	Service   string `json:"service"`
	Direction string `json:"direction"`
	Depth     int    `json:"depth"`
	Views     string `json:"views"`
	MaxNodes  int    `json:"max_nodes"`
}

func fleetNeighborhoodHandler(q *fleet.Query, link bool) mcpsdk.ToolHandlerFor[fleetNeighborhoodArgs, any] {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest, a fleetNeighborhoodArgs) (*mcpsdk.CallToolResult, any, error) {
		return productResult(link, productview.ToNeighborhood)(q.Neighborhood(fleet.NeighborhoodQuery{
			Kind: fleet.KindService, Key: a.Service, Direction: fleet.Direction(a.Direction),
			Depth: a.Depth, Views: SplitViews(a.Views), MaxNodes: a.MaxNodes,
		}))
	}
}

// SplitViews reads a comma-separated list of knowledge views. Blank parts are
// dropped, and an empty list means expected and observed, so a neighbourhood
// shows the dependents only observation has seen.
func SplitViews(s string) []fleet.KnowledgeView {
	var out []fleet.KnowledgeView
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, fleet.KnowledgeView(p))
		}
	}
	if len(out) == 0 {
		return []fleet.KnowledgeView{fleet.ViewExpected, fleet.ViewObserved}
	}
	return out
}

// productResult answers a product query's result, or its error as the tool's
// error. With link set it answers the dashboard's transport type instead, whose
// references carry hrefs.
func productResult[T, P any](link bool, to func(*T) P) func(*T, error) (*mcpsdk.CallToolResult, any, error) {
	return func(res *T, err error) (*mcpsdk.CallToolResult, any, error) {
		if err != nil {
			return errorResult(err), nil, nil
		}
		var v any = res
		if link {
			v = to(res)
		}
		r, err := jsonResult(v)
		return r, nil, err
	}
}
