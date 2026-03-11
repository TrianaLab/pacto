package diff

import (
	"testing"
	"testing/fstest"
)

func makeOpenAPIFS(content string) fstest.MapFS {
	return fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(content)},
	}
}

const baseSpec = `openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
      responses:
        "200":
          description: OK
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
`

func TestDiffOpenAPI_BothFSNil(t *testing.T) {
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_EmptyPath(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffOpenAPI("", "", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_OldFSNil(t *testing.T) {
	newFS := makeOpenAPIFS(baseSpec)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", nil, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_BothReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffOpenAPI("missing.yaml", "missing.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_OldReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := makeOpenAPIFS(baseSpec)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffOpenAPI_NewReadError(t *testing.T) {
	oldFS := makeOpenAPIFS(baseSpec)
	newFS := fstest.MapFS{}
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestReadOpenAPISpec_InvalidYAML(t *testing.T) {
	fs := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(`{invalid`)},
	}
	_, err := readOpenAPISpec(fs, "openapi.yaml")
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestReadOpenAPISpec_MissingFile(t *testing.T) {
	fs := fstest.MapFS{}
	_, err := readOpenAPISpec(fs, "missing.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadOpenAPISpec_Valid(t *testing.T) {
	fs := makeOpenAPIFS(baseSpec)
	spec, err := readOpenAPISpec(fs, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.Paths) != 2 {
		t.Errorf("expected 2 paths, got %d", len(spec.Paths))
	}
	if _, ok := spec.Paths["/health"]; !ok {
		t.Error("expected /health path")
	}
	if _, ok := spec.Paths["/users"]; !ok {
		t.Error("expected /users path")
	}
}

func TestDiffOpenAPI_IdenticalSpecs(t *testing.T) {
	oldFS := makeOpenAPIFS(baseSpec)
	newFS := makeOpenAPIFS(baseSpec)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for identical specs, got %d: %v", len(changes), changes)
	}
}

func TestDiffOpenAPI_PathRemoved(t *testing.T) {
	oldFS := makeOpenAPIFS(baseSpec)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users]" && c.Type == Removed && c.Classification == Breaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected openapi.paths[/users] Removed BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_PathAdded(t *testing.T) {
	oldFS := makeOpenAPIFS(baseSpec)
	newFS := makeOpenAPIFS(baseSpec + `  /orders:
    get:
      summary: List orders
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/orders]" && c.Type == Added && c.Classification == NonBreaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected openapi.paths[/orders] Added NON_BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_MethodRemoved(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
    delete:
      summary: Delete all users
      responses:
        "204":
          description: Deleted
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[DELETE]" && c.Type == Removed && c.Classification == Breaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DELETE method removed as BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_MethodAdded(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
    post:
      summary: Create user
      responses:
        "201":
          description: Created
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[POST]" && c.Type == Added && c.Classification == NonBreaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected POST method added as NON_BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_NonMethodKeysIgnored(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    summary: Users endpoint
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    summary: Users endpoint (updated)
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[SUMMARY]" {
			t.Error("summary should not be treated as an HTTP method")
		}
	}
}

func TestDiffOpenAPI_RequestBodyAdded(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      summary: Create user
      responses:
        "201":
          description: Created
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      summary: Create user
      requestBody:
        content:
          application/json:
            schema:
              type: object
      responses:
        "201":
          description: Created
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[POST].request-body" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Errorf("expected request body added, got %v", changes)
	}
}

func TestDiffOpenAPI_RequestBodyRemoved(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      summary: Create user
      requestBody:
        content:
          application/json:
            schema:
              type: object
      responses:
        "201":
          description: Created
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      summary: Create user
      responses:
        "201":
          description: Created
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[POST].request-body" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Errorf("expected request body removed, got %v", changes)
	}
}

func TestDiffOpenAPI_RequestBodyModified(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      summary: Create user
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
      responses:
        "201":
          description: Created
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      summary: Create user
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
                email:
                  type: string
      responses:
        "201":
          description: Created
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[POST].request-body" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Errorf("expected request body modified, got %v", changes)
	}
}

func TestDiffOpenAPI_ResponseAdded(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
        "404":
          description: Not Found
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[GET].responses[404]" && c.Type == Added && c.Classification == NonBreaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected response 404 added as NON_BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_ResponseRemoved(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
        "404":
          description: Not Found
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[GET].responses[404]" && c.Type == Removed && c.Classification == Breaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected response 404 removed as BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_ResponseModified(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    name:
                      type: string
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    name:
                      type: string
                    email:
                      type: string
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[GET].responses[200]" && c.Type == Modified && c.Classification == PotentialBreaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected response 200 modified as POTENTIAL_BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_OperationBothNilMaps(t *testing.T) {
	changes := diffOperation("/test", "GET", nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for nil operations, got %d", len(changes))
	}
}

func TestDiffOpenAPI_OperationOneNilMap(t *testing.T) {
	changes := diffOperation("/test", "GET", map[string]any{"summary": "test"}, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes when new op is nil (no responses/requestBody), got %d", len(changes))
	}
}

func TestDiffOpenAPI_ResponsesNilBothSides(t *testing.T) {
	changes := diffResponses("/test", "GET", nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for nil responses, got %d", len(changes))
	}
}

func TestToStringMap(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		isNil  bool
		length int
	}{
		{"nil input", nil, true, 0},
		{"string map", map[string]any{"a": 1}, false, 1},
		{"non-map", "hello", true, 0},
		{"int value", 42, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toStringMap(tt.input)
			if tt.isNil && result != nil {
				t.Errorf("expected nil, got %v", result)
			}
			if !tt.isNil && len(result) != tt.length {
				t.Errorf("expected length %d, got %d", tt.length, len(result))
			}
		})
	}
}

func TestYamlEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{"identical strings", "hello", "hello", true},
		{"different strings", "hello", "world", false},
		{"identical maps", map[string]any{"a": 1}, map[string]any{"a": 1}, true},
		{"different maps", map[string]any{"a": 1}, map[string]any{"a": 2}, false},
		{"nil values", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := yamlEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("yamlEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDiffOpenAPISpecs_BothEmpty(t *testing.T) {
	old := &openAPISpec{Paths: map[string]map[string]any{}}
	new := &openAPISpec{Paths: map[string]map[string]any{}}
	changes := diffOpenAPISpecs(old, new)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffPathMethods_NoHTTPMethods(t *testing.T) {
	old := map[string]any{"summary": "old", "description": "old desc"}
	new := map[string]any{"summary": "new", "description": "new desc"}
	changes := diffPathMethods("/test", old, new)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for non-HTTP keys, got %d", len(changes))
	}
}

func TestDiffOpenAPI_MultipleChanges(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health
      responses:
        "200":
          description: OK
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
    delete:
      summary: Delete users
      responses:
        "204":
          description: Deleted
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health
      responses:
        "200":
          description: OK
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
        "500":
          description: Internal Server Error
    post:
      summary: Create user
      requestBody:
        content:
          application/json:
            schema:
              type: object
      responses:
        "201":
          description: Created
  /orders:
    get:
      summary: List orders
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)

	expectations := map[string]ChangeType{
		"openapi.paths[/orders]":                                    Added,
		"openapi.paths[/users].methods[DELETE]":                     Removed,
		"openapi.paths[/users].methods[POST]":                       Added,
		"openapi.paths[/users].methods[GET].responses[500]":         Added,
	}

	for path, ct := range expectations {
		found := false
		for _, c := range changes {
			if c.Path == path && c.Type == ct {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected change {path=%s, type=%s} not found in %v", path, ct, changes)
		}
	}
}

func TestDiffOpenAPI_RequestBodyIdentical(t *testing.T) {
	spec := `openapi: "3.0.0"
paths:
  /users:
    post:
      summary: Create user
      requestBody:
        content:
          application/json:
            schema:
              type: object
      responses:
        "201":
          description: Created
`
	oldFS := makeOpenAPIFS(spec)
	newFS := makeOpenAPIFS(spec)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for identical request bodies, got %d: %v", len(changes), changes)
	}
}

func TestDiffOpenAPI_ParameterAdded(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      parameters:
        - name: filter
          in: query
          schema:
            type: string
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[GET].parameters[filter:query]" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Errorf("expected parameter added, got %v", changes)
	}
}

func TestDiffOpenAPI_ParameterRemoved(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      parameters:
        - name: filter
          in: query
          schema:
            type: string
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[GET].parameters[filter:query]" && c.Type == Removed && c.Classification == Breaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected parameter removed as BREAKING, got %v", changes)
	}
}

func TestDiffOpenAPI_ParameterModified(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      parameters:
        - name: filter
          in: query
          required: false
          schema:
            type: string
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      parameters:
        - name: filter
          in: query
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "openapi.paths[/users].methods[GET].parameters[filter:query]" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Errorf("expected parameter modified, got %v", changes)
	}
}

func TestDiffOpenAPI_ParameterIdentical(t *testing.T) {
	spec := `openapi: "3.0.0"
paths:
  /users:
    get:
      summary: List users
      parameters:
        - name: page
          in: query
          schema:
            type: integer
      responses:
        "200":
          description: OK
`
	oldFS := makeOpenAPIFS(spec)
	newFS := makeOpenAPIFS(spec)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for identical parameters, got %d: %v", len(changes), changes)
	}
}

func TestDiffOpenAPI_MultipleParameters(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users/{id}:
    get:
      summary: Get user
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: fields
          in: query
          schema:
            type: string
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users/{id}:
    get:
      summary: Get user
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: expand
          in: query
          schema:
            type: boolean
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)

	expectations := map[string]ChangeType{
		"openapi.paths[/users/{id}].methods[GET].parameters[fields:query]": Removed,
		"openapi.paths[/users/{id}].methods[GET].parameters[expand:query]": Added,
	}
	for path, ct := range expectations {
		found := false
		for _, c := range changes {
			if c.Path == path && c.Type == ct {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected change {path=%s, type=%s} not found in %v", path, ct, changes)
		}
	}
}

func TestDiffParameters_BothNil(t *testing.T) {
	changes := diffParameters("/test", "GET", nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for nil parameters, got %d", len(changes))
	}
}

func TestIndexParams_SkipsInvalidEntries(t *testing.T) {
	params := []any{
		"not a map",
		map[string]any{},
	}
	result := indexParams(params)
	if len(result) != 0 {
		t.Errorf("expected 0 indexed params, got %d", len(result))
	}
}

func TestToSlice(t *testing.T) {
	tests := []struct {
		name  string
		input any
		isNil bool
	}{
		{"nil input", nil, true},
		{"valid slice", []any{"a", "b"}, false},
		{"non-slice", "hello", true},
		{"int value", 42, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toSlice(tt.input)
			if tt.isNil && result != nil {
				t.Errorf("expected nil, got %v", result)
			}
			if !tt.isNil && result == nil {
				t.Error("expected non-nil result")
			}
		})
	}
}

func TestParamKey(t *testing.T) {
	p := map[string]any{"name": "id", "in": "path"}
	if got := paramKey(p); got != "id:path" {
		t.Errorf("expected 'id:path', got %q", got)
	}
}

func TestParamLabel(t *testing.T) {
	p := map[string]any{"name": "filter", "in": "query"}
	if got := paramLabel(p); got != "query param 'filter'" {
		t.Errorf("expected \"query param 'filter'\", got %q", got)
	}
}
