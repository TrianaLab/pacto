package mcp

import (
	"context"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/pkg/contract"
)

// --- pacto_validate ---

func validateTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_validate",
		Description: "Validates a pacto contract file. Returns validation errors and warnings.",
		InputSchema: inputSchema(map[string]property{
			"path": {Type: "string", Description: "Path to directory containing pacto.yaml or oci:// reference"},
		}, []string{"path"}),
	}
}

func validateHandler(svc *app.Service) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		path := parseInput(req, "path")
		result, err := svc.Validate(ctx, app.ValidateOptions{Path: path})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result)
	}
}

// --- pacto_inspect ---

func inspectTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_inspect",
		Description: "Returns the structured representation of a contract including name, interfaces, dependencies, runtime, and configuration.",
		InputSchema: inputSchema(map[string]property{
			"ref": {Type: "string", Description: "OCI reference or local path to contract directory"},
		}, []string{"ref"}),
	}
}

func inspectHandler(svc *app.Service) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		ref := parseInput(req, "ref")
		result, err := svc.Explain(ctx, app.ExplainOptions{Path: ref})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result)
	}
}

// --- pacto_resolve_dependencies ---

func resolveDependenciesTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_resolve_dependencies",
		Description: "Resolves the full dependency graph for a contract, detecting cycles and version conflicts.",
		InputSchema: inputSchema(map[string]property{
			"ref": {Type: "string", Description: "OCI reference or local path to contract directory"},
		}, []string{"ref"}),
	}
}

func resolveDependenciesHandler(svc *app.Service) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		ref := parseInput(req, "ref")
		result, err := svc.Graph(ctx, app.GraphOptions{Path: ref})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(result)
	}
}

// --- pacto_list_interfaces ---

func listInterfacesTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_list_interfaces",
		Description: "Returns the interfaces exposed by a service contract.",
		InputSchema: inputSchema(map[string]property{
			"ref": {Type: "string", Description: "OCI reference or local path to contract directory"},
		}, []string{"ref"}),
	}
}

type interfacesResult struct {
	Interfaces []app.ExplainInterface `json:"interfaces"`
}

func listInterfacesHandler(svc *app.Service) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		ref := parseInput(req, "ref")
		result, err := svc.Explain(ctx, app.ExplainOptions{Path: ref})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(interfacesResult{Interfaces: result.Interfaces})
	}
}

// --- pacto_generate_docs ---

func generateDocsTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_generate_docs",
		Description: "Generates Markdown documentation for a contract.",
		InputSchema: inputSchema(map[string]property{
			"ref": {Type: "string", Description: "OCI reference or local path to contract directory"},
		}, []string{"ref"}),
	}
}

func generateDocsHandler(svc *app.Service) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		ref := parseInput(req, "ref")
		result, err := svc.Doc(ctx, app.DocOptions{Path: ref})
		if err != nil {
			return errorResult(err), nil
		}
		return textResult(result.Markdown), nil
	}
}

// --- pacto_explain ---

func explainTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_explain",
		Description: "Returns a human-readable explanation of the contract including a summary, interfaces, and dependencies.",
		InputSchema: inputSchema(map[string]property{
			"ref": {Type: "string", Description: "OCI reference or local path to contract directory"},
		}, []string{"ref"}),
	}
}

type explainSummary struct {
	Summary      string `json:"summary"`
	Interfaces   string `json:"interfaces"`
	Dependencies string `json:"dependencies"`
}

func explainHandler(svc *app.Service) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		ref := parseInput(req, "ref")
		result, err := svc.Explain(ctx, app.ExplainOptions{Path: ref})
		if err != nil {
			return errorResult(err), nil
		}
		summary := buildExplainSummary(result)
		return jsonResult(summary)
	}
}

func buildExplainSummary(r *app.ExplainResult) explainSummary {
	summary := fmt.Sprintf("%s@%s is a %s %s workload (pacto %s)",
		r.Name, r.Version, r.Runtime.StateType, r.Runtime.WorkloadType, r.PactoVersion)
	if r.Owner != "" {
		summary += fmt.Sprintf(", owned by %s", r.Owner)
	}

	var ifaces []string
	for _, i := range r.Interfaces {
		desc := fmt.Sprintf("%s (%s", i.Name, i.Type)
		if i.Port != nil {
			desc += fmt.Sprintf(", port %d", *i.Port)
		}
		if i.Visibility != "" {
			desc += fmt.Sprintf(", %s", i.Visibility)
		}
		desc += ")"
		ifaces = append(ifaces, desc)
	}

	var deps []string
	for _, d := range r.Dependencies {
		req := "optional"
		if d.Required {
			req = "required"
		}
		deps = append(deps, fmt.Sprintf("%s (%s, %s)", d.Ref, d.Compatibility, req))
	}

	return explainSummary{
		Summary:      summary,
		Interfaces:   strings.Join(ifaces, "; "),
		Dependencies: strings.Join(deps, "; "),
	}
}

// --- pacto_generate_contract ---

func generateContractTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_generate_contract",
		Description: "Generates a new pacto contract YAML from structured inputs.",
		InputSchema: inputSchema(map[string]property{
			"service_name":   {Type: "string", Description: "Name of the service"},
			"language":       {Type: "string", Description: "Programming language (e.g. go, python, java)"},
			"exposes_http":   {Type: "boolean", Description: "Whether the service exposes an HTTP interface"},
			"exposes_grpc":   {Type: "boolean", Description: "Whether the service exposes a gRPC interface"},
			"needs_database": {Type: "boolean", Description: "Whether the service needs a database dependency"},
			"needs_cache":    {Type: "boolean", Description: "Whether the service needs a cache dependency"},
		}, []string{"service_name"}),
	}
}

type generateInput struct {
	ServiceName string
	Language    string
	ExposesHTTP bool
	ExposesGRPC bool
	NeedsDB     bool
	NeedsCache  bool
}

func generateContractHandler() mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		input := generateInput{
			ServiceName: parseInput(req, "service_name"),
			Language:    parseInput(req, "language"),
			ExposesHTTP: parseInputBool(req, "exposes_http"),
			ExposesGRPC: parseInputBool(req, "exposes_grpc"),
			NeedsDB:     parseInputBool(req, "needs_database"),
			NeedsCache:  parseInputBool(req, "needs_cache"),
		}
		if input.ServiceName == "" {
			return errorResult(fmt.Errorf("service_name is required")), nil
		}
		yaml := buildContractYAML(input)
		return textResult(yaml), nil
	}
}

func buildContractYAML(input generateInput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "pactoVersion: \"1.0\"\n\nservice:\n  name: %s\n  version: \"0.1.0\"\n", input.ServiceName)

	if input.Language != "" {
		fmt.Fprintf(&b, "\nmetadata:\n  language: %s\n", input.Language)
	}

	writeInterfaces(&b, input)
	writeDependencies(&b, input)
	writeRuntime(&b, input)

	return b.String()
}

func writeInterfaces(b *strings.Builder, input generateInput) {
	if !input.ExposesHTTP && !input.ExposesGRPC {
		return
	}
	fmt.Fprintln(b, "\ninterfaces:")
	if input.ExposesHTTP {
		fmt.Fprintln(b, "  - name: http-api")
		fmt.Fprintln(b, "    type: http")
		fmt.Fprintln(b, "    port: 8080")
		fmt.Fprintln(b, "    visibility: public")
	}
	if input.ExposesGRPC {
		fmt.Fprintln(b, "  - name: grpc-api")
		fmt.Fprintln(b, "    type: grpc")
		fmt.Fprintln(b, "    port: 9090")
		fmt.Fprintln(b, "    visibility: internal")
	}
}

func writeDependencies(b *strings.Builder, input generateInput) {
	if !input.NeedsDB && !input.NeedsCache {
		return
	}
	fmt.Fprintln(b, "\ndependencies:")
	if input.NeedsDB {
		fmt.Fprintln(b, "  - ref: postgres")
		fmt.Fprintln(b, "    required: true")
		fmt.Fprintln(b, "    compatibility: \"^1.0.0\"")
	}
	if input.NeedsCache {
		fmt.Fprintln(b, "  - ref: redis")
		fmt.Fprintln(b, "    required: false")
		fmt.Fprintln(b, "    compatibility: \"^1.0.0\"")
	}
}

func writeRuntime(b *strings.Builder, input generateInput) {
	stateType := "stateless"
	scope := "local"
	durability := "ephemeral"
	dataCrit := "low"
	if input.NeedsDB {
		stateType = "stateful"
		scope = "shared"
		durability = "persistent"
		dataCrit = "medium"
	}

	fmt.Fprintln(b, "\nruntime:")
	fmt.Fprintln(b, "  workload: service")
	fmt.Fprintln(b, "  state:")
	fmt.Fprintf(b, "    type: %s\n", stateType)
	fmt.Fprintln(b, "    persistence:")
	fmt.Fprintf(b, "      scope: %s\n", scope)
	fmt.Fprintf(b, "      durability: %s\n", durability)
	fmt.Fprintf(b, "    dataCriticality: %s\n", dataCrit)

	if input.ExposesHTTP {
		fmt.Fprintln(b, "  health:")
		fmt.Fprintln(b, "    interface: http-api")
		fmt.Fprintln(b, "    path: /health")
	}
}

// --- pacto_suggest_dependencies ---

func suggestDependenciesTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_suggest_dependencies",
		Description: "Suggests likely service dependencies based on a contract's interfaces, runtime, and existing dependencies.",
		InputSchema: inputSchema(map[string]property{
			"contract": {Type: "string", Description: "Path to directory containing pacto.yaml"},
		}, []string{"contract"}),
	}
}

type suggestResult struct {
	Suggested []string `json:"suggested"`
}

func suggestDependenciesHandler(svc *app.Service) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		path := parseInput(req, "contract")
		result, err := svc.Explain(ctx, app.ExplainOptions{Path: path})
		if err != nil {
			return errorResult(err), nil
		}
		suggestions := suggestDependencies(result)
		return jsonResult(suggestResult{Suggested: suggestions})
	}
}

func suggestDependencies(r *app.ExplainResult) []string {
	existing := make(map[string]bool)
	for _, d := range r.Dependencies {
		existing[d.Ref] = true
	}

	var suggestions []string
	add := func(name string) {
		if !existing[name] {
			suggestions = append(suggestions, name)
			existing[name] = true
		}
	}

	suggestFromInterfaces(r.Interfaces, add)
	suggestFromRuntime(r.Runtime, add)

	return suggestions
}

func suggestFromInterfaces(ifaces []app.ExplainInterface, add func(string)) {
	for _, iface := range ifaces {
		if iface.Type == contract.InterfaceTypeHTTP && iface.Visibility == contract.VisibilityPublic {
			add("api-gateway")
		}
	}
}

func suggestFromRuntime(rt app.ExplainRuntime, add func(string)) {
	if rt.StateType == "stateful" {
		if rt.Durability == "persistent" {
			add("postgres")
		}
		if rt.Scope == "shared" {
			add("redis")
		}
	}

	if rt.StateType == "stateless" {
		add("redis")
	}

	if rt.WorkloadType == "service" {
		add("prometheus")
	}
}

// --- schema helpers ---

type property struct {
	Type        string
	Description string
}

func inputSchema(props map[string]property, required []string) map[string]any {
	propMap := make(map[string]any, len(props))
	for name, p := range props {
		propMap[name] = map[string]any{
			"type":        p.Type,
			"description": p.Description,
		}
	}
	return map[string]any{
		"type":       "object",
		"properties": propMap,
		"required":   required,
	}
}
