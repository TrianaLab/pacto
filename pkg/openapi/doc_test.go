package openapi

import (
	"testing"
	"testing/fstest"
)

const sampleSpec = `{
  "openapi": "3.1.0",
  "servers": [{"url": "https://api.example.com"}, {"nourl": true}],
  "components": {
    "securitySchemes": {
      "bearerAuth": {"type": "http", "scheme": "bearer"},
      "apiKey": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
      "bogus": ["not", "a", "map"]
    },
    "schemas": {
      "Refund": {"type": "object", "properties": {"amount": {"type": "integer"}}, "required": ["amount"]}
    }
  },
  "security": [{"bearerAuth": []}, "not-a-map"],
  "paths": {
    "/users/{id}": {
      "get": {
        "operationId": "getUser",
        "summary": "Get a user",
        "description": "Fetch one user",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "description": "user id", "schema": {"type": "string"}},
          {"name": "verbose", "in": "query", "schema": {"type": "boolean"}},
          "not-a-param"
        ]
      }
    },
    "/refunds": {
      "post": {
        "summary": "Create refund",
        "security": [{"apiKey": ["read:refunds"]}],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Refund"}}}
        }
      }
    },
    "/notmap": "ignored"
  }
}`

func mustReadSample(t *testing.T) *Doc {
	t.Helper()
	fsys := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(sampleSpec)}}
	doc, err := ReadDoc(fsys, "interfaces/openapi.json")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
	return doc
}

func opByID(doc *Doc, id string) Operation {
	for _, op := range doc.Operations {
		if op.ID == id {
			return op
		}
	}
	return Operation{}
}

func TestReadDocMeta(t *testing.T) {
	doc := mustReadSample(t)
	if len(doc.Servers) != 1 || doc.Servers[0] != "https://api.example.com" {
		t.Fatalf("servers = %v", doc.Servers)
	}
	if doc.SecuritySchemes["bearerAuth"].Scheme != "bearer" {
		t.Fatalf("bearerAuth scheme = %q", doc.SecuritySchemes["bearerAuth"].Scheme)
	}
	if doc.SecuritySchemes["apiKey"].In != "header" || doc.SecuritySchemes["apiKey"].Name != "X-API-Key" {
		t.Fatalf("apiKey = %+v", doc.SecuritySchemes["apiKey"])
	}
	if _, ok := doc.SecuritySchemes["bogus"]; ok {
		t.Fatal("non-map scheme should be skipped")
	}
	if _, ok := doc.Components["Refund"]; !ok {
		t.Fatalf("components missing Refund: %v", doc.Components)
	}
	if len(doc.Security) != 1 { // "not-a-map" entry skipped
		t.Fatalf("global security = %v", doc.Security)
	}
}

func TestReadDocGetOperation(t *testing.T) {
	get := opByID(mustReadSample(t), "getUser")
	if get.Method != "GET" || get.Path != "/users/{id}" || get.Description != "Fetch one user" {
		t.Fatalf("getUser = %+v", get)
	}
	if len(get.Parameters) != 2 { // "not-a-param" skipped
		t.Fatalf("getUser params = %+v", get.Parameters)
	}
	if get.Parameters[0].In != "path" || !get.Parameters[0].Required || get.Parameters[0].Description != "user id" {
		t.Fatalf("path param = %+v", get.Parameters[0])
	}
}

func TestReadDocDerivedPostOperation(t *testing.T) {
	post := opByID(mustReadSample(t), "post_refunds") // no operationId → derived
	if post.Method != "POST" || post.RequestBody == nil || !post.RequestBody.Required {
		t.Fatalf("derived post op = %+v", post)
	}
	if post.RequestBody.Schema["$ref"] != "#/components/schemas/Refund" {
		t.Fatalf("request body schema = %v", post.RequestBody.Schema)
	}
	if len(post.Security) != 1 || len(post.Security[0]["apiKey"]) != 1 || post.Security[0]["apiKey"][0] != "read:refunds" {
		t.Fatalf("op security = %v", post.Security)
	}
}

func TestReadDocYAML(t *testing.T) {
	const y = `openapi: 3.0.0
paths:
  /ping:
    get:
      summary: ping
`
	doc, err := ReadDoc(fstest.MapFS{"openapi.yaml": {Data: []byte(y)}}, "openapi.yaml")
	if err != nil {
		t.Fatalf("ReadDoc yaml: %v", err)
	}
	if len(doc.Operations) != 1 || doc.Operations[0].ID != "get_ping" {
		t.Fatalf("yaml operations = %+v", doc.Operations)
	}
	if doc.Servers != nil || doc.SecuritySchemes != nil {
		t.Fatalf("expected empty servers/schemes, got %v / %v", doc.Servers, doc.SecuritySchemes)
	}
}

func TestReadDocNoRequestBodyContent(t *testing.T) {
	const spec = `{"paths":{"/x":{"post":{"requestBody":{"required":false}}}}}`
	doc, err := ReadDoc(fstest.MapFS{"o.json": {Data: []byte(spec)}}, "o.json")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
	rb := doc.Operations[0].RequestBody
	if rb == nil || rb.Required || rb.Schema != nil {
		t.Fatalf("request body = %+v", rb)
	}
}

func TestReadDocResolvesComponentRefs(t *testing.T) {
	const spec = `{
  "components": {
    "parameters": {"PageSize": {"name": "pageSize", "in": "query", "required": true, "schema": {"type": "integer"}}},
    "requestBodies": {"CreateUser": {"required": true, "content": {"application/json": {"schema": {"type": "object"}}}}}
  },
  "paths": {"/users": {"post": {
    "operationId": "createUser",
    "parameters": [{"$ref": "#/components/parameters/PageSize"}, {"$ref": "#/components/parameters/Missing"}],
    "requestBody": {"$ref": "#/components/requestBodies/CreateUser"}
  }}}
}`
	doc, err := ReadDoc(fstest.MapFS{"o.json": {Data: []byte(spec)}}, "o.json")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
	op := doc.Operations[0]
	// resolved param carries the real name/in from components.parameters
	if op.Parameters[0].Name != "pageSize" || op.Parameters[0].In != "query" || !op.Parameters[0].Required {
		t.Fatalf("resolved param = %+v", op.Parameters[0])
	}
	// unresolvable $ref target falls back to the ref map (empty name)
	if op.Parameters[1].Name != "" {
		t.Fatalf("missing-target param should be empty, got %+v", op.Parameters[1])
	}
	// resolved request body carries the schema + required flag
	if op.RequestBody == nil || !op.RequestBody.Required || op.RequestBody.Schema == nil {
		t.Fatalf("resolved request body = %+v", op.RequestBody)
	}
}

func TestResolveRef(t *testing.T) {
	targets := map[string]any{"X": map[string]any{"name": "x"}}
	// not a ref → unchanged
	plain := map[string]any{"name": "inline"}
	if got := resolveRef(plain, targets, "#/components/parameters/"); got["name"] != "inline" {
		t.Fatalf("non-ref should pass through: %v", got)
	}
	// wrong prefix → unchanged
	wrong := map[string]any{"$ref": "#/components/schemas/X"}
	if got := resolveRef(wrong, targets, "#/components/parameters/"); got["$ref"] == nil {
		t.Fatalf("wrong-prefix ref should pass through: %v", got)
	}
	// resolvable
	ref := map[string]any{"$ref": "#/components/parameters/X"}
	if got := resolveRef(ref, targets, "#/components/parameters/"); got["name"] != "x" {
		t.Fatalf("expected resolved target, got %v", got)
	}
}

func TestDeriveOperationID(t *testing.T) {
	tests := []struct{ method, path, want string }{
		{"GET", "/users/{id}", "get_users_id"},
		{"POST", "/refunds", "post_refunds"},
		{"GET", "/", "get_root"},
		{"delete", "/a/b-c.d", "delete_a_b_c_d"},
	}
	for _, tt := range tests {
		if got := DeriveOperationID(tt.method, tt.path); got != tt.want {
			t.Errorf("DeriveOperationID(%q,%q) = %q, want %q", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestReadDocErrors(t *testing.T) {
	if _, err := ReadDoc(fstest.MapFS{}, "missing.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
	bad := fstest.MapFS{"x.json": {Data: []byte("{ not json")}}
	if _, err := ReadDoc(bad, "x.json"); err == nil {
		t.Fatal("expected parse error")
	}
}
