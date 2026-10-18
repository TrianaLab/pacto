// Package capability turns a bundle's OpenAPI interface into executable agent
// tools: it derives tool descriptors from operations (BuildTools) and invokes
// the live service (Invoke). It is HTTP/OpenAPI-specific today; when a second
// interface type is added, the shared shape it reveals should be extracted then
// rather than guessed now.
package capability

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/openapi"
)

// Tool is a single agent-invocable operation derived from an OpenAPI operation.
type Tool struct {
	Name        string
	Method      string
	Path        string
	Summary     string
	Description string
	Mutating    bool
	InputSchema map[string]any
	Op          openapi.Operation
}

var mutatingMethods = map[string]bool{"POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// IsMutating reports whether an HTTP method changes server state.
func IsMutating(method string) bool { return mutatingMethods[strings.ToUpper(method)] }

// BuildTools derives one Tool per operation. When allowWrites is false, mutating
// operations (POST/PUT/PATCH/DELETE) are omitted. Duplicate tool names (which can
// arise when operationIds are absent and derived ids collide) are disambiguated
// with a numeric suffix so no operation is silently dropped.
func BuildTools(doc *openapi.Doc, allowWrites bool) []Tool {
	var tools []Tool
	seen := map[string]int{}
	for _, op := range doc.Operations {
		mut := IsMutating(op.Method)
		if mut && !allowWrites {
			continue
		}
		name := op.ID
		if n := seen[op.ID]; n > 0 {
			name = fmt.Sprintf("%s_%d", op.ID, n+1)
		}
		seen[op.ID]++
		tools = append(tools, Tool{
			Name:        name,
			Method:      op.Method,
			Path:        op.Path,
			Summary:     op.Summary,
			Description: op.Description,
			Mutating:    mut,
			InputSchema: buildInputSchema(op, doc.Components),
			Op:          op,
		})
	}
	return tools
}

// InterfaceTools is one OpenAPI interface's tools as every surface names them.
type InterfaceTools struct {
	Interface contract.Interface
	// Doc is the parsed OpenAPI document; nil when Err is set.
	Doc *openapi.Doc
	// Err is why the interface's document could not be read. Each surface
	// decides whether that is fatal (MCP) or a gap to skip (fleet, dashboard).
	Err error
	// Tools carry the interface-prefixed name (see ToolsForContract).
	Tools []Tool
}

// ToolsForContract builds the tools of every OpenAPI interface with a ref, in
// declaration order. When the contract declares more than one, each tool name is
// prefixed "<interface>_" so two interfaces cannot collide; with exactly one the
// operation name stands alone. MCP, the fleet and the dashboard all name tools
// through here, so an operation is called the same thing everywhere.
func ToolsForContract(c *contract.Contract, fsys fs.FS, allowWrites bool) []InterfaceTools {
	var out []InterfaceTools
	for _, iface := range c.Interfaces {
		if iface.Type == contract.InterfaceTypeOpenAPI && iface.Ref != "" {
			out = append(out, InterfaceTools{Interface: iface})
		}
	}
	for i := range out {
		it := &out[i]
		if it.Doc, it.Err = openapi.ReadDoc(fsys, it.Interface.Ref); it.Err != nil {
			continue
		}
		it.Tools = BuildTools(it.Doc, allowWrites)
		if len(out) > 1 {
			for j := range it.Tools {
				it.Tools[j].Name = it.Interface.Name + "_" + it.Tools[j].Name
			}
		}
	}
	return out
}

// buildInputSchema builds a self-contained JSON Schema for the tool: path/query/
// header params plus a "body" property, with components inlined under $defs and
// #/components/schemas/X refs rewritten to #/$defs/X.
func buildInputSchema(op openapi.Operation, components map[string]any) map[string]any {
	props := map[string]any{}
	var required []string
	usedRef := false

	for _, p := range op.Parameters {
		if p.In == "cookie" {
			continue
		}
		schema := p.Schema
		if schema == nil {
			schema = map[string]any{"type": "string"}
		}
		s := rewriteRefs(schema, &usedRef)
		if p.Description != "" {
			s = withDescription(s, p.Description)
		}
		props[p.Name] = s
		if p.Required {
			required = append(required, p.Name)
		}
	}

	if op.RequestBody != nil && op.RequestBody.Schema != nil {
		props["body"] = rewriteRefs(op.RequestBody.Schema, &usedRef)
		if op.RequestBody.Required {
			required = append(required, "body")
		}
	}

	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	if usedRef && len(components) > 0 {
		schema["$defs"] = inlineDefs(components)
	}
	return schema
}

func inlineDefs(components map[string]any) map[string]any {
	defs := map[string]any{}
	for name, c := range components {
		used := false
		defs[name] = rewriteRefs(c, &used)
	}
	return defs
}

// rewriteRefs deep-copies a schema, rewriting #/components/schemas/X refs to
// #/$defs/X. It sets *used when any such ref is found.
func rewriteRefs(v any, used *bool) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					if rewritten, ok := rewriteRefString(s); ok {
						*used = true
						out[k] = rewritten
						continue
					}
				}
			}
			out[k] = rewriteRefs(val, used)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = rewriteRefs(e, used)
		}
		return out
	default:
		return v
	}
}

func rewriteRefString(s string) (string, bool) {
	if name, ok := strings.CutPrefix(s, "#/components/schemas/"); ok {
		return "#/$defs/" + name, true
	}
	return s, false
}

func withDescription(v any, desc string) any {
	schema, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if _, exists := schema["description"]; exists {
		return schema
	}
	schema["description"] = desc
	return schema
}
