package openapi

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Doc is the rich, parsed representation of an OpenAPI spec: enough to derive
// executable agent tools (operations, parameters, request bodies) and to
// authenticate against the live service (security schemes and requirements).
type Doc struct {
	Servers         []string
	SecuritySchemes map[string]SecurityScheme
	Security        []SecurityRequirement
	Components      map[string]any
	Operations      []Operation
}

// Operation is a single method+path pair from the spec's paths.
type Operation struct {
	ID          string
	Method      string
	Path        string
	Summary     string
	Description string
	Parameters  []Parameter
	RequestBody *RequestBody
	Security    []SecurityRequirement
}

// Parameter is a single OpenAPI operation parameter (path/query/header/cookie).
type Parameter struct {
	Name        string
	In          string
	Description string
	Required    bool
	Schema      map[string]any
}

// RequestBody holds the application/json request body schema for an operation.
type RequestBody struct {
	Required bool
	Schema   map[string]any
}

// SecurityScheme describes a components.securitySchemes entry.
type SecurityScheme struct {
	Type         string
	In           string
	Name         string
	Scheme       string
	BearerFormat string
}

// SecurityRequirement maps a scheme name to its required scopes.
type SecurityRequirement = map[string][]string

// docMethods is the operation-method scan order (matches ReadOpenAPIEndpoints).
var docMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// ReadDoc parses an OpenAPI spec (YAML or JSON) into a rich Doc.
func ReadDoc(fsys fs.FS, path string) (*Doc, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading OpenAPI spec %s: %w", path, err)
	}
	spec, err := UnmarshalSpec(data, path)
	if err != nil {
		return nil, fmt.Errorf("parsing OpenAPI spec %s: %w", path, err)
	}
	return &Doc{
		Servers:         parseServers(spec),
		SecuritySchemes: parseSecuritySchemes(spec),
		Security:        parseSecurityList(spec["security"]),
		Components:      componentSchemas(spec),
		Operations:      parseOperations(spec),
	}, nil
}

func parseOperations(spec map[string]any) []Operation {
	paths, _ := spec["paths"].(map[string]any)
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var ops []Operation
	for _, p := range keys {
		item, ok := paths[p].(map[string]any)
		if !ok {
			continue
		}
		for _, m := range docMethods {
			raw, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			ops = append(ops, parseOperation(strings.ToUpper(m), p, raw))
		}
	}
	return ops
}

func parseOperation(method, path string, raw map[string]any) Operation {
	op := Operation{
		Method:      method,
		Path:        path,
		ID:          asString(raw["operationId"]),
		Summary:     asString(raw["summary"]),
		Description: asString(raw["description"]),
		Parameters:  parseParameters(raw["parameters"]),
		RequestBody: parseRequestBody(raw["requestBody"]),
		Security:    parseSecurityList(raw["security"]),
	}
	if op.ID == "" {
		op.ID = DeriveOperationID(method, path)
	}
	return op
}

func parseParameters(v any) []Parameter {
	list, _ := v.([]any)
	var params []Parameter
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		params = append(params, Parameter{
			Name:        asString(m["name"]),
			In:          asString(m["in"]),
			Description: asString(m["description"]),
			Required:    asBool(m["required"]),
			Schema:      asMap(m["schema"]),
		})
	}
	return params
}

func parseRequestBody(v any) *RequestBody {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	rb := &RequestBody{Required: asBool(m["required"])}
	if content, ok := m["content"].(map[string]any); ok {
		if j, ok := content["application/json"].(map[string]any); ok {
			rb.Schema = asMap(j["schema"])
		}
	}
	return rb
}

func parseServers(spec map[string]any) []string {
	list, _ := spec["servers"].([]any)
	var out []string
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			if u := asString(m["url"]); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

func parseSecuritySchemes(spec map[string]any) map[string]SecurityScheme {
	comps, _ := spec["components"].(map[string]any)
	raw, _ := comps["securitySchemes"].(map[string]any)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]SecurityScheme, len(raw))
	for name, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out[name] = SecurityScheme{
			Type:         asString(m["type"]),
			In:           asString(m["in"]),
			Name:         asString(m["name"]),
			Scheme:       asString(m["scheme"]),
			BearerFormat: asString(m["bearerFormat"]),
		}
	}
	return out
}

func parseSecurityList(v any) []SecurityRequirement {
	list, _ := v.([]any)
	var out []SecurityRequirement
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		req := SecurityRequirement{}
		for k, sv := range m {
			scopes, _ := sv.([]any)
			strs := make([]string, 0, len(scopes))
			for _, s := range scopes {
				strs = append(strs, asString(s))
			}
			req[k] = strs
		}
		out = append(out, req)
	}
	return out
}

func componentSchemas(spec map[string]any) map[string]any {
	comps, _ := spec["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	return schemas
}

// DeriveOperationID builds a stable id from method+path when operationId is
// absent (e.g. GET /users/{id} -> get_users_id).
func DeriveOperationID(method, path string) string {
	seg := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, path)
	seg = collapseUnderscores(seg)
	if seg == "" {
		return strings.ToLower(method) + "_root"
	}
	return strings.ToLower(method) + "_" + seg
}

func collapseUnderscores(s string) string {
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return strings.Trim(s, "_")
}

func asString(v any) string { s, _ := v.(string); return s }

func asBool(v any) bool { b, _ := v.(bool); return b }

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
