package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v2/pkg/capability"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/openapi"
)

// CapabilityOptions configures capability-tool registration for a bundle.
type CapabilityOptions struct {
	BaseURL     string
	Creds       capability.Credentials
	AllowWrites bool
	HTTPClient  *http.Client
}

// RegisterCapabilities registers one executable MCP tool per OpenAPI operation
// in the bundle's http interfaces, plus a pacto_skill tool exposing skills/*.md.
// Mutating operations (POST/PUT/PATCH/DELETE) are only registered when
// opts.AllowWrites is set.
func RegisterCapabilities(server *mcpsdk.Server, bundle *contract.Bundle, opts CapabilityOptions, stderr io.Writer) error {
	ifaces := httpInterfaces(bundle.Contract)
	for _, iface := range ifaces {
		if err := registerInterface(server, bundle.FS, iface, len(ifaces) > 1, opts, stderr); err != nil {
			return err
		}
	}
	server.AddTool(skillTool(), skillHandler(bundle.FS))
	return nil
}

func httpInterfaces(c *contract.Contract) []contract.Interface {
	var out []contract.Interface
	for _, iface := range c.Interfaces {
		if iface.Type == contract.InterfaceTypeHTTP && iface.Contract != "" {
			out = append(out, iface)
		}
	}
	return out
}

func registerInterface(server *mcpsdk.Server, fsys fs.FS, iface contract.Interface, prefixed bool, opts CapabilityOptions, stderr io.Writer) error {
	doc, err := openapi.ReadDoc(fsys, iface.Contract)
	if err != nil {
		return fmt.Errorf("interface %q: %w", iface.Name, err)
	}
	base := opts.BaseURL
	if base == "" && len(doc.Servers) > 0 {
		base = doc.Servers[0]
	}
	if base == "" {
		return fmt.Errorf("interface %q: no base URL (set --base-url or add servers[] to the spec)", iface.Name)
	}

	tools := capability.BuildTools(doc, opts.AllowWrites)
	if !opts.AllowWrites {
		if n := countMutating(doc); n > 0 {
			_, _ = fmt.Fprintf(stderr, "pacto mcp: skipped %d mutating operation(s) in interface %q (use --allow-writes to expose)\n", n, iface.Name)
		}
	}

	prefix := ""
	if prefixed {
		prefix = iface.Name + "_"
	}
	for _, tool := range tools {
		server.AddTool(&mcpsdk.Tool{
			Name:        prefix + tool.Name,
			Description: toolDesc(tool),
			InputSchema: tool.InputSchema,
		}, capabilityHandler(tool.Op, doc, base, opts))
	}
	return nil
}

func countMutating(doc *openapi.Doc) int {
	n := 0
	for _, op := range doc.Operations {
		if capability.IsMutating(op.Method) {
			n++
		}
	}
	return n
}

func toolDesc(tool capability.Tool) string {
	switch {
	case tool.Summary != "":
		return tool.Summary
	case tool.Description != "":
		return tool.Description
	default:
		return tool.Method + " " + tool.Path
	}
}

func capabilityHandler(op openapi.Operation, doc *openapi.Doc, baseURL string, opts CapabilityOptions) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		args := map[string]any{}
		if req.Params != nil && len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errorResult(fmt.Errorf("invalid arguments: %w", err)), nil
			}
		}
		res, err := capability.Invoke(ctx, opts.HTTPClient, op, doc, baseURL, args, opts.Creds)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(res)
	}
}

func skillTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_skill",
		Description: "Lists the bundle's domain skills (skills/*.md) when called with no arguments, " +
			"or returns one skill's markdown content when given its name.",
		InputSchema: inputSchema(map[string]property{
			"name": {Type: "string", Description: "Skill file name (e.g. refund_customer.md). Omit to list all skills."},
		}, nil),
	}
}

func skillHandler(fsys fs.FS) mcpsdk.ToolHandler {
	return func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		name := parseInput(req, "name")
		if name == "" {
			names, _ := capability.Skills(fsys)
			return jsonResult(names)
		}
		content, err := capability.Skill(fsys, name)
		if err != nil {
			return errorResult(err), nil
		}
		return textResult(content), nil
	}
}
