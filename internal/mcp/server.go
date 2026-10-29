// Package mcp provides an MCP (Model Context Protocol) server that exposes
// Pacto contract operations as tools for AI agents.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// baseInstructions describe the always-present contract-authoring tools.
const baseInstructions = "Pacto tells you which services a change breaks before it ships, and checks what is actually running. " +
	"A contract (pacto.yaml) is an operational contract for one cloud-native service. " +
	"Use pacto_create to generate new contracts from intent-level descriptions. " +
	"Use pacto_edit to modify existing contracts. Use pacto_check to validate " +
	"and get actionable improvement suggestions. Call pacto_schema first if you " +
	"need the full JSON Schema reference."

// Validator runs the `pacto validate` path for the bundle in dir: lockfile
// verification, recursive policies[].ref resolution relative to dir, and every
// layer the CLI runs. The command layer supplies it (app.Service.ValidationReport)
// so this package depends on the validation result shape and never on OCI,
// Kubernetes or the app service.
//
// It is the whole validation path rather than a policy resolver because an
// agent looping until pacto_check says valid must stop on a contract `pacto
// validate` and CI also accept. A resolver alone left the lockfile unverified,
// so a drifted pacto.lock passed pacto_check and then failed CI.
type Validator func(ctx context.Context, dir string) (validation.ValidationResult, error)

// NewServer creates a new MCP server with all Pacto authoring tools registered.
//
// A nil validate is not a downgrade: Check falls back to local validation with no
// policy resolver, which fails a ref-based policy closed with
// POLICY_REF_UNRESOLVED, so the agent is told the contract cannot be confirmed
// valid rather than told it is.
func NewServer(validate Validator, version string) *mcpsdk.Server {
	return newServer(version, baseInstructions, validate)
}

// newBareServer builds a server with no tools registered at all. A mode whose
// surface must stay read-only cannot go through newServer, because two of the
// authoring tools write to the filesystem; it still comes through here so every
// mode shares one implementation identity and one place options are set.
func newBareServer(version, instructions string) *mcpsdk.Server {
	return mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "pacto", Version: version},
		&mcpsdk.ServerOptions{Instructions: instructions},
	)
}

// newServer builds a server with the given instructions and the authoring tools.
func newServer(version, instructions string, validate Validator) *mcpsdk.Server {
	server := newBareServer(version, instructions)
	registerTools(server, validate)
	return server
}

// registerTools adds all Pacto tools to the MCP server.
//
// Every tool goes through the generic mcpsdk.AddTool rather than the raw
// Server.AddTool: the generic form validates the incoming arguments against the
// tool's declared schema before the handler runs and unmarshals them into a
// typed struct. The raw form does neither, so `{"max_depth":"3"}` used to decode
// to nothing and be read as the 0 that means "unlimited" — the caller asked to
// bound a traversal and got an unbounded one.
func registerTools(server *mcpsdk.Server, validate Validator) {
	mcpsdk.AddTool(server, createTool(), createHandler())
	mcpsdk.AddTool(server, editTool(), editHandler())
	mcpsdk.AddTool(server, checkTool(), checkHandler(validate))
	mcpsdk.AddTool(server, schemaTool(), schemaHandler())
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

// jsonField is one tool argument declared as a string holding JSON, paired with
// the typed destination it decodes into.
type jsonField struct {
	name string
	raw  string
	dst  any
}

// unmarshalFields decodes the supplied JSON-string arguments, skipping the ones
// that were not sent.
func unmarshalFields(fields []jsonField) error {
	for _, f := range fields {
		if f.raw == "" {
			continue
		}
		if err := json.Unmarshal([]byte(f.raw), f.dst); err != nil {
			return fmt.Errorf("invalid %s JSON: %w", f.name, err)
		}
	}
	return nil
}
