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

func TestReadDoc(t *testing.T) {
	fsys := fstest.MapFS{"interfaces/openapi.json": {Data: []byte(sampleSpec)}}
	doc, err := ReadDoc(fsys, "interfaces/openapi.json")
	if err != nil {
		t.Fatalf("ReadDoc: %v", err)
	}
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
		t.Fatalf("non-map scheme should be skipped")
	}
	if _, ok := doc.Components["Refund"]; !ok {
		t.Fatalf("components missing Refund: %v", doc.Components)
	}
	if len(doc.Security) != 1 { // "not-a-map" entry skipped
		t.Fatalf("global security = %v", doc.Security)
	}

	byID := map[string]Operation{}
	for _, op := range doc.Operations {
		byID[op.ID] = op
	}
	get := byID["getUser"]
	if get.Method != "GET" || get.Path != "/users/{id}" || get.Description != "Fetch one user" {
		t.Fatalf("getUser = %+v", get)
	}
	if len(get.Parameters) != 2 { // "not-a-param" skipped
		t.Fatalf("getUser params = %+v", get.Parameters)
	}
	if get.Parameters[0].In != "path" || !get.Parameters[0].Required || get.Parameters[0].Description != "user id" {
		t.Fatalf("path param = %+v", get.Parameters[0])
	}
	post := byID["post_refunds"] // no operationId → derived
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
