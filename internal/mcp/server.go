// Package mcp provides an MCP (Model Context Protocol) server that exposes
// Pacto contract operations as tools for AI agents.
package mcp

import (
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/internal/app"
)

// NewServer creates a new MCP server with all Pacto tools registered.
func NewServer(svc *app.Service, version string) *mcpsdk.Server {
	server := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "pacto", Version: version},
		nil,
	)

	registerTools(server, svc)
	return server
}

// registerTools adds all Pacto tools to the MCP server.
func registerTools(server *mcpsdk.Server, svc *app.Service) {
	server.AddTool(validateTool(), validateHandler(svc))
	server.AddTool(inspectTool(), inspectHandler(svc))
	server.AddTool(resolveDependenciesTool(), resolveDependenciesHandler(svc))
	server.AddTool(listInterfacesTool(), listInterfacesHandler(svc))
	server.AddTool(generateDocsTool(), generateDocsHandler(svc))
	server.AddTool(explainTool(), explainHandler(svc))
	server.AddTool(generateContractTool(), generateContractHandler())
	server.AddTool(suggestDependenciesTool(), suggestDependenciesHandler(svc))
}

// jsonResult marshals v to JSON and returns it as a CallToolResult with text content.
func jsonResult(v any) (*mcpsdk.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling result: %w", err)
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(data)},
		},
	}, nil
}

// textResult returns a CallToolResult with plain text content.
func textResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: text},
		},
	}
}

// errorResult returns a CallToolResult indicating an error.
func errorResult(err error) *mcpsdk.CallToolResult {
	r := textResult(err.Error())
	r.IsError = true
	return r
}

// parseArgs unmarshals the raw JSON arguments into a map for field access.
func parseArgs(req *mcpsdk.CallToolRequest) map[string]json.RawMessage {
	if req.Params == nil || len(req.Params.Arguments) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(req.Params.Arguments, &m); err != nil {
		return nil
	}
	return m
}

// parseInput extracts a string field from the CallToolRequest arguments.
func parseInput(req *mcpsdk.CallToolRequest, field string) string {
	args := parseArgs(req)
	if args == nil {
		return ""
	}
	raw, ok := args[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// parseInputBool extracts a boolean field from the CallToolRequest arguments.
func parseInputBool(req *mcpsdk.CallToolRequest, field string) bool {
	args := parseArgs(req)
	if args == nil {
		return false
	}
	raw, ok := args[field]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false
	}
	return b
}
