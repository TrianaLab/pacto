package capability

import (
	"strings"

	"github.com/trianalab/pacto/v2/pkg/openapi"
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
// operations (POST/PUT/PATCH/DELETE) are omitted.
func BuildTools(doc *openapi.Doc, allowWrites bool) []Tool {
	var tools []Tool
	for _, op := range doc.Operations {
		mut := IsMutating(op.Method)
		if mut && !allowWrites {
			continue
		}
		tools = append(tools, Tool{
			Name:        op.ID,
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
