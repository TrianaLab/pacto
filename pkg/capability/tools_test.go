package capability

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/openapi"
)

func sampleDoc() *openapi.Doc {
	return &openapi.Doc{
		Components: map[string]any{
			"Refund":  map[string]any{"type": "object", "properties": map[string]any{"amount": map[string]any{"type": "integer"}}},
			"Nested":  map[string]any{"$ref": "#/components/schemas/Refund"},
			"bogus":   "not-a-map",
			"listref": map[string]any{"type": "array", "items": []any{map[string]any{"$ref": "#/components/schemas/Refund"}}},
		},
		Operations: []openapi.Operation{
			{ID: "getUser", Method: "GET", Path: "/users/{id}", Summary: "Get user",
				Parameters: []openapi.Parameter{
					{Name: "id", In: "path", Required: true, Description: "the id", Schema: map[string]any{"type": "string"}},
					{Name: "verbose", In: "query"},
					{Name: "sid", In: "cookie"},
					// a non-components $ref must pass through unchanged (not a $defs rewrite)
					{Name: "ext", In: "query", Schema: map[string]any{"$ref": "https://example.com/schema.json"}},
				}},
			{ID: "createRefund", Method: "POST", Path: "/refunds",
				RequestBody: &openapi.RequestBody{Required: true, Schema: map[string]any{"$ref": "#/components/schemas/Refund"}}},
		},
	}
}

func TestBuildToolsWriteGate(t *testing.T) {
	ro := BuildTools(sampleDoc(), false)
	if len(ro) != 1 || ro[0].Name != "getUser" {
		t.Fatalf("read-only tools = %+v", ro)
	}
	all := BuildTools(sampleDoc(), true)
	if len(all) != 2 {
		t.Fatalf("all tools = %d", len(all))
	}
}

func toolByName(t *testing.T, name string) Tool {
	t.Helper()
	for _, tl := range BuildTools(sampleDoc(), true) {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found", name)
	return Tool{}
}

func TestBuildToolsInputSchemaParams(t *testing.T) {
	get := toolByName(t, "getUser")
	props := get.InputSchema["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Fatalf("missing id prop: %v", props)
	}
	if _, ok := props["sid"]; ok {
		t.Fatal("cookie param must be excluded")
	}
	if props["verbose"].(map[string]any)["type"] != "string" {
		t.Fatalf("verbose default schema = %v", props["verbose"])
	}
	if props["id"].(map[string]any)["description"] != "the id" {
		t.Fatalf("id description not applied: %v", props["id"])
	}
	if props["ext"].(map[string]any)["$ref"] != "https://example.com/schema.json" {
		t.Fatalf("non-components $ref must pass through: %v", props["ext"])
	}
	req := toStringSet(get.InputSchema["required"])
	if !req["id"] || req["verbose"] {
		t.Fatalf("required = %v (want id only)", req)
	}
	if _, hasDefs := get.InputSchema["$defs"]; hasDefs {
		t.Fatal("getUser uses no components refs; $defs must be absent")
	}
}

func TestBuildToolsInputSchemaBody(t *testing.T) {
	post := toolByName(t, "createRefund")
	if !post.Mutating {
		t.Fatal("post must be mutating")
	}
	bodyProp := post.InputSchema["properties"].(map[string]any)["body"].(map[string]any)
	if bodyProp["$ref"] != "#/$defs/Refund" {
		t.Fatalf("body $ref = %v (want #/$defs/Refund)", bodyProp["$ref"])
	}
	if !toStringSet(post.InputSchema["required"])["body"] {
		t.Fatal("body must be required")
	}
	defs := post.InputSchema["$defs"].(map[string]any)
	if _, ok := defs["Refund"]; !ok {
		t.Fatalf("$defs missing Refund: %v", defs)
	}
	if defs["Nested"].(map[string]any)["$ref"] != "#/$defs/Refund" {
		t.Fatalf("nested ref not rewritten: %v", defs["Nested"])
	}
	items := defs["listref"].(map[string]any)["items"].([]any)
	if items[0].(map[string]any)["$ref"] != "#/$defs/Refund" {
		t.Fatalf("array item ref not rewritten: %v", items)
	}
}

func TestBuildToolsBodyNotRequiredNoRefs(t *testing.T) {
	doc := &openapi.Doc{Operations: []openapi.Operation{
		{ID: "put_thing", Method: "PUT", Path: "/thing",
			RequestBody: &openapi.RequestBody{Required: false, Schema: map[string]any{"type": "object"}}},
	}}
	tools := BuildTools(doc, true)
	if _, ok := tools[0].InputSchema["required"]; ok {
		t.Fatal("no required fields expected")
	}
	if _, ok := tools[0].InputSchema["$defs"]; ok {
		t.Fatal("$defs must be absent when no refs used")
	}
}

func TestWithDescriptionNonMap(t *testing.T) {
	if got := withDescription("scalar", "d"); got != "scalar" {
		t.Fatalf("non-map schema should pass through, got %v", got)
	}
	m := map[string]any{"description": "keep"}
	if withDescription(m, "new").(map[string]any)["description"] != "keep" {
		t.Fatal("existing description must be preserved")
	}
}

func TestBuildToolsDedupNames(t *testing.T) {
	// Two distinct paths with no operationId derive the same id; names must be
	// disambiguated so neither operation is dropped.
	doc := &openapi.Doc{Operations: []openapi.Operation{
		{ID: "get_a_b", Method: "GET", Path: "/a.b"},
		{ID: "get_a_b", Method: "GET", Path: "/a/b"},
	}}
	tools := BuildTools(doc, true)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "get_a_b" || tools[1].Name != "get_a_b_2" {
		t.Fatalf("names = %q, %q (want get_a_b, get_a_b_2)", tools[0].Name, tools[1].Name)
	}
}

func TestIsMutating(t *testing.T) {
	for _, m := range []string{"POST", "put", "Patch", "DELETE"} {
		if !IsMutating(m) {
			t.Errorf("%s should be mutating", m)
		}
	}
	for _, m := range []string{"GET", "head", "OPTIONS"} {
		if IsMutating(m) {
			t.Errorf("%s should not be mutating", m)
		}
	}
}

func toStringSet(v any) map[string]bool {
	out := map[string]bool{}
	if list, ok := v.([]string); ok {
		for _, s := range list {
			out[s] = true
		}
	}
	return out
}
