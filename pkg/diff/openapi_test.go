package diff

import (
	"encoding/json"
	"errors"
	"strings"
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
	// The request body is now DEEP-diffed, so a changed body surfaces the specific
	// field (here an added optional property) rather than one shallow change.
	found := false
	for _, c := range changes {
		if strings.Contains(c.Path, "request-body") && strings.Contains(c.Path, "email") && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Errorf("expected deep-diffed request body change for the added field, got %v", changes)
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
	// The response is DEEP-diffed like the request body, so a changed response
	// surfaces the specific field rather than one shallow change for the whole
	// response object. An optional field appearing on a response is the
	// POTENTIAL_BREAKING default: a strict client can choke on it, but nothing a
	// consumer already reads has moved.
	found := false
	for _, c := range changes {
		if strings.Contains(c.Path, "responses[200]") && strings.Contains(c.Path, "email") && c.Type == Added {
			found = true
			if c.Classification != PotentialBreaking {
				t.Errorf("expected POTENTIAL_BREAKING for an added response property, got %s", c.Classification)
			}
		}
	}
	if !found {
		t.Errorf("expected deep-diffed response change for the added field, got %v", changes)
	}
}

// Audit finding 7's own scenario, in the shape the repo's fixtures actually
// take: a 200 response schema with NO `required` array loses a property. If this
// only reached POTENTIAL_BREAKING the finding would still be open — `pacto diff`
// exits 0 on anything below BREAKING.
func TestDiffOpenAPI_ResponsePropertyRemovedWithoutRequiredIsBreaking(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
                  email: {type: string}
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Type == Removed && strings.HasSuffix(c.Path, "schema.properties.email") {
			found = true
			if c.Classification != Breaking {
				t.Errorf("expected BREAKING for a dropped response property, got %s", c.Classification)
			}
		}
	}
	if !found {
		t.Errorf("expected the dropped response property reported at its own path, got %v", changes)
	}
}

// The four quadrants of `required`. Which side supplies the data decides whether
// an entry appearing there is a new obligation or a stronger promise, and the
// classification has to follow: a response that guarantees MORE must not fail
// the release gate, and one that guarantees less must.
func TestDiffOpenAPI_RequiredIsDirectionAware(t *testing.T) {
	spec := func(method, required string) string {
		body := `                type: object
                required: [` + required + `]
                properties:
                  id: {type: string}
                  email: {type: string}
`
		if method == "post" {
			return `openapi: "3.0.0"
paths:
  /users:
    post:
      requestBody:
        content:
          application/json:
            schema:
` + body
		}
		return `openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
` + body
	}

	tests := []struct {
		name         string
		method       string
		old, new     string
		wantType     ChangeType
		wantClassify Classification
	}{
		{"request required added", "post", "id", "id, email", Added, Breaking},
		{"request required removed", "post", "id, email", "id", Removed, NonBreaking},
		{"response required added", "get", "id", "id, email", Added, NonBreaking},
		{"response required removed", "get", "id, email", "id", Removed, Breaking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := diffOpenAPI("openapi.yaml", "openapi.yaml",
				makeOpenAPIFS(spec(tt.method, tt.old)), makeOpenAPIFS(spec(tt.method, tt.new)))
			found := false
			for _, c := range changes {
				if !strings.HasSuffix(c.Path, "schema.required[email]") {
					continue
				}
				found = true
				if c.Type != tt.wantType || c.Classification != tt.wantClassify {
					t.Errorf("got %s/%s, want %s/%s", c.Type, c.Classification, tt.wantType, tt.wantClassify)
				}
			}
			if !found {
				t.Errorf("expected a required[email] change, got %v", changes)
			}
		})
	}
}

// yaml.v3 decodes a mapping with a non-string key into
// map[interface{}]interface{}, which encoding/json cannot marshal. When the
// equality check treated that failure as "equal", one legal `example: {200: ok}`
// anywhere in a response silenced every other change in the same response —
// including a deleted property. The rest of the diff must survive it.
func TestDiffOpenAPI_NonStringKeyDoesNotSilenceTheResponseDiff(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              example:
                200: ok
              schema:
                type: object
                properties:
                  id: {type: string}
                  email: {type: string}
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: still OK
          content:
            application/json:
              example:
                200: ok
              schema:
                type: object
                properties:
                  email: {type: string}
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)

	var dropped, described bool
	for _, c := range changes {
		if c.Type == Removed && strings.HasSuffix(c.Path, "schema.properties.id") && c.Classification == Breaking {
			dropped = true
		}
		if strings.HasSuffix(c.Path, "responses[200].description") {
			described = true
		}
		if strings.Contains(c.Path, "example") {
			t.Errorf("the unchanged non-string-keyed example must not read as a change: %+v", c)
		}
	}
	if !dropped {
		t.Errorf("expected the dropped property reported as BREAKING, got %v", changes)
	}
	if !described {
		t.Errorf("expected the changed description reported, got %v", changes)
	}
}

// Dropping a guaranteed property from a success response is the commonest way a
// provider breaks its consumers. It must reach BREAKING, because that is the only
// classification the `pacto diff` exit code and ReleaseBlocking() act on.
func TestDiffOpenAPI_ResponseDroppedRequiredField_Breaking(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id, email]
                properties:
                  id: {type: string}
                  email: {type: string}
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id: {type: string}
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	var breaking, property bool
	for _, c := range changes {
		if c.Classification == Breaking && strings.Contains(c.Path, "responses[200]") && strings.Contains(c.Path, "required") {
			breaking = true
		}
		if c.Type == Removed && strings.Contains(c.Path, "properties.email") {
			property = true
		}
	}
	if !breaking {
		t.Errorf("expected the dropped response guarantee classified Breaking, got %v", changes)
	}
	if !property {
		t.Errorf("expected the removed response property reported at its own path, got %v", changes)
	}
}

func TestDiffOpenAPI_OperationBothNilMaps(t *testing.T) {
	changes := diffOperation("/test", "GET", nil, nil, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for nil operations, got %d", len(changes))
	}
}

func TestDiffOpenAPI_OperationOneNilMap(t *testing.T) {
	changes := diffOperation("/test", "GET", map[string]any{"summary": "test"}, nil, nil, nil)
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

// unmarshalableYAML fails to encode, so yamlEqual cannot compare it.
type unmarshalableYAML struct{}

func (unmarshalableYAML) MarshalYAML() (any, error) { return nil, errors.New("nope") }

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
		{"non-string keys", map[any]any{200: "ok"}, map[any]any{200: "ok"}, true},
		{"nil values", nil, nil, true},
		// A value that cannot be encoded reads as CHANGED even against itself:
		// reporting "equal" on a failed comparison is how a diff goes missing.
		{"unencodable", unmarshalableYAML{}, unmarshalableYAML{}, false},
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
		"openapi.paths[/orders]":                            Added,
		"openapi.paths[/users].methods[DELETE]":             Removed,
		"openapi.paths[/users].methods[POST]":               Added,
		"openapi.paths[/users].methods[GET].responses[500]": Added,
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
			if c.OldValue != "required=false" {
				t.Errorf("expected OldValue 'required=false', got %q", c.OldValue)
			}
			if c.NewValue != "required=true" {
				t.Errorf("expected NewValue 'required=true', got %q", c.NewValue)
			}
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

func TestFlattenMap(t *testing.T) {
	m := map[string]any{
		"required": true,
		"schema":   map[string]any{"type": "string", "title": "Filter"},
	}
	flat := flattenMap(m, "")
	if flat["required"] != "true" {
		t.Errorf("expected required=true, got %q", flat["required"])
	}
	if flat["schema.type"] != "string" {
		t.Errorf("expected schema.type=string, got %q", flat["schema.type"])
	}
	if flat["schema.title"] != "Filter" {
		t.Errorf("expected schema.title=Filter, got %q", flat["schema.title"])
	}
}

func TestFlattenMap_WithPrefix(t *testing.T) {
	m := map[string]any{"type": "string"}
	flat := flattenMap(m, "schema")
	if flat["schema.type"] != "string" {
		t.Errorf("expected schema.type=string, got %q", flat["schema.type"])
	}
}

func TestFlattenMap_Empty(t *testing.T) {
	flat := flattenMap(map[string]any{}, "")
	if len(flat) != 0 {
		t.Errorf("expected empty map, got %v", flat)
	}
}

func TestFlattenMap_Nil(t *testing.T) {
	flat := flattenMap(nil, "")
	if len(flat) != 0 {
		t.Errorf("expected empty map, got %v", flat)
	}
}

func TestMapDelta_PropertyChanged(t *testing.T) {
	old := map[string]any{"required": false, "schema": map[string]any{"type": "string"}}
	new := map[string]any{"required": true, "schema": map[string]any{"type": "string"}}
	oldS, newS := mapDelta(old, new, nil)
	if oldS != "required=false" {
		t.Errorf("expected old 'required=false', got %q", oldS)
	}
	if newS != "required=true" {
		t.Errorf("expected new 'required=true', got %q", newS)
	}
}

func TestMapDelta_PropertyAdded(t *testing.T) {
	old := map[string]any{"schema": map[string]any{"type": "string"}}
	new := map[string]any{"required": true, "schema": map[string]any{"type": "string"}}
	oldS, newS := mapDelta(old, new, nil)
	if oldS != "" {
		t.Errorf("expected empty old, got %q", oldS)
	}
	if newS != "required=true" {
		t.Errorf("expected new 'required=true', got %q", newS)
	}
}

func TestMapDelta_PropertyRemoved(t *testing.T) {
	old := map[string]any{"required": true, "description": "A filter", "schema": map[string]any{"type": "string"}}
	new := map[string]any{"required": true, "schema": map[string]any{"type": "string"}}
	oldS, newS := mapDelta(old, new, nil)
	if oldS != "description=A filter" {
		t.Errorf("expected old 'description=A filter', got %q", oldS)
	}
	if newS != "" {
		t.Errorf("expected empty new, got %q", newS)
	}
}

func TestMapDelta_WithSkipKeys(t *testing.T) {
	old := map[string]any{"name": "filter", "in": "query", "required": false}
	new := map[string]any{"name": "filter", "in": "query", "required": true}
	skip := map[string]bool{"name": true, "in": true}
	oldS, newS := mapDelta(old, new, skip)
	if oldS != "required=false" {
		t.Errorf("expected old 'required=false', got %q", oldS)
	}
	if newS != "required=true" {
		t.Errorf("expected new 'required=true', got %q", newS)
	}
}

func TestMapDelta_Identical(t *testing.T) {
	m := map[string]any{"required": true, "schema": map[string]any{"type": "string"}}
	oldS, newS := mapDelta(m, m, nil)
	if oldS != "" || newS != "" {
		t.Errorf("expected empty for identical maps, got old=%q new=%q", oldS, newS)
	}
}

func TestMapDelta_NilMaps(t *testing.T) {
	oldS, newS := mapDelta(nil, nil, nil)
	if oldS != "" || newS != "" {
		t.Errorf("expected empty for nil maps, got old=%q new=%q", oldS, newS)
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

// --- Audit regression tests: OpenAPI diff false-negatives ---

// Path-item-level parameters (shared by all operations) must be diffed; removing a
// shared required parameter is BREAKING, not silently NON_BREAKING.
func TestDiffOpenAPI_PathLevelParamRemoved_Breaking(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    parameters:
      - name: tenant
        in: query
        required: true
    get:
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Type == Removed && c.Classification == Breaking && strings.Contains(c.Path, "parameters[tenant:query]") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected removed shared path-level required param classified Breaking, got %v", changes)
	}
}

// A newly required property in the request body must be BREAKING (deep schema diff),
// not one shallow POTENTIAL_BREAKING for the whole body.
func TestDiffOpenAPI_RequestBodyNewRequiredField_Breaking(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name: {type: string}
      responses:
        "201":
          description: Created
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [email]
              properties:
                name: {type: string}
                email: {type: string}
      responses:
        "201":
          description: Created
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Classification == Breaking && strings.Contains(c.Path, "request-body") && strings.Contains(c.Path, "required") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected new required request-body field classified Breaking, got %v", changes)
	}
}

// Flipping a parameter from optional to required is BREAKING.
func TestDiffOpenAPI_ParamOptionalToRequired_Breaking(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      parameters:
        - {name: filter, in: query, required: false}
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      parameters:
        - {name: filter, in: query, required: true}
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Type == Modified && c.Classification == Breaking && strings.Contains(c.Path, "parameters[filter:query]") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected optional->required param classified Breaking, got %v", changes)
	}
}

// Adding a new required parameter is BREAKING.
func TestDiffOpenAPI_AddedRequiredParam_Breaking(t *testing.T) {
	oldFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      responses:
        "200":
          description: OK
`)
	newFS := makeOpenAPIFS(`openapi: "3.0.0"
paths:
  /users:
    get:
      parameters:
        - {name: tenant, in: query, required: true}
      responses:
        "200":
          description: OK
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Type == Added && c.Classification == Breaking && strings.Contains(c.Path, "parameters[tenant:query]") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected added required param classified Breaking, got %v", changes)
	}
}

// responseSpec wraps a GET /users 200 response body in a whole document.
func responseSpec(response string) fstest.MapFS {
	return makeOpenAPIFS("paths:\n  /users:\n    get:\n      responses:\n        " + response)
}

// A value yaml.v3 decoded as map[interface{}]interface{} used to reach
// Change.OldValue untouched, and encoding/json refuses that type: the whole
// Result stopped marshalling, so `pacto diff --output-format json` printed no
// diff and the dashboard's /api/diff answered 500.
func TestDiffOpenAPI_NonStringKeyedNodeMarshalsAsJSON(t *testing.T) {
	oldFS := responseSpec(`"200":
          description: OK
          content:
            application/json:
              example: {200: ok}
`)
	newFS := responseSpec(`"200":
          description: OK
          content:
            application/json:
              example: {200: fine}
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}

	out, err := json.Marshal(&Result{Classification: PotentialBreaking, Changes: changes})
	if err != nil {
		t.Fatalf("Result must marshal as JSON: %v", err)
	}
	var got struct {
		Changes []struct {
			OldValue map[string]string `json:"oldValue"`
			NewValue map[string]string `json:"newValue"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if got.Changes[0].OldValue["200"] != "ok" || got.Changes[0].NewValue["200"] != "fine" {
		t.Errorf("expected the integer key stringified to \"200\", got %s", out)
	}
}

// Dropping a response's whole payload is a strictly worse break than dropping
// one of its fields, so it cannot be graded more leniently than one.
func TestDiffOpenAPI_ResponsePayloadRemovalIsBreaking(t *testing.T) {
	full := `"200":
          description: OK
          content:
            application/json:
              example: {sample: 1}
              schema:
                type: object
                properties:
                  id: {type: string}
`
	tests := []struct {
		name     string
		new      string
		path     string
		expected Classification
	}{
		{"content block removed", `"200":
          description: OK
`, "openapi.paths[/users].methods[GET].responses[200].content", Breaking},
		{"media type removed", `"200":
          description: OK
          content:
            application/xml: {}
`, "openapi.paths[/users].methods[GET].responses[200].content.application/json", Breaking},
		{"schema removed", `"200":
          description: OK
          content:
            application/json:
              example: {sample: 1}
`, "openapi.paths[/users].methods[GET].responses[200].content.application/json.schema", Breaking},
		// Losing the whole `properties` block loses every field at once. Losing one
		// of them is already BREAKING, so this cannot grade below it.
		{"properties block removed", `"200":
          description: OK
          content:
            application/json:
              example: {sample: 1}
              schema:
                type: object
`, "openapi.paths[/users].methods[GET].responses[200].content.application/json.schema.properties", Breaking},
		// Documentation beside the payload: removing it takes nothing away from a
		// consumer, so it must not be escalated.
		{"description removed", `"200":
          content:
            application/json:
              example: {sample: 1}
              schema:
                type: object
                properties:
                  id: {type: string}
`, "openapi.paths[/users].methods[GET].responses[200].description", PotentialBreaking},
		{"example removed", `"200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
`, "openapi.paths[/users].methods[GET].responses[200].content.application/json.example", PotentialBreaking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := diffOpenAPI("openapi.yaml", "openapi.yaml", responseSpec(full), responseSpec(tt.new))
			c, ok := findChange(changes, tt.path, Removed)
			if !ok {
				t.Fatalf("expected a removal at %s, got %+v", tt.path, changes)
			}
			if c.Classification != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, c.Classification)
			}
		})
	}
}

// A property may be named `required`. Reading it as the `required` LIST let a
// request body silently lose a field at NON_BREAKING.
func TestDiffOpenAPI_PropertyNamedRequired(t *testing.T) {
	body := func(props string) fstest.MapFS {
		return makeOpenAPIFS(`paths:
  /users:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
` + props)
	}
	oldFS := body("                id: {type: string}\n                required: {type: boolean}\n")
	newFS := body("                id: {type: string}\n")

	path := "openapi.paths[/users].methods[POST].request-body.content.application/json.schema.properties.required"
	c, ok := findChange(diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS), path, Removed)
	if !ok {
		t.Fatalf("expected a removal at %s", path)
	}
	if c.Classification != PotentialBreaking {
		t.Errorf("a property named required is a property, expected POTENTIAL_BREAKING, got %s", c.Classification)
	}
}

// Status codes are legal unquoted in YAML, and yaml.v3 then decodes `responses`
// with integer keys. That used to make toStringMap return nil and every
// response change in the document vanish.
func TestDiffOpenAPI_UnquotedStatusCodes(t *testing.T) {
	unquoted := func(props string) fstest.MapFS {
		return makeOpenAPIFS(`paths:
  /users:
    get:
      responses:
        200:
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
` + props)
	}
	oldFS := unquoted("                  id: {type: string}\n                  name: {type: string}\n")
	newFS := unquoted("                  id: {type: string}\n")

	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	path := "openapi.paths[/users].methods[GET].responses[200].content.application/json.schema.properties.name"
	c, ok := findChange(changes, path, Removed)
	if !ok {
		t.Fatalf("expected a removal at %s, got %+v", path, changes)
	}
	if c.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", c.Classification)
	}
}

func TestToStringMap_NonStringKeys(t *testing.T) {
	got := toStringMap(map[any]any{200: "ok", true: "yes"})
	if got["200"] != "ok" || got["true"] != "yes" {
		t.Errorf("expected keys stringified, got %#v", got)
	}
}

// Nothing inside a response HEADER is the response payload. Removing a whole
// header grades POTENTIAL_BREAKING, so every shape nested inside one has to
// grade the same or milder: escalating any of them makes strictly less removal
// carry a strictly worse verdict, and a false BREAKING hard-blocks a release.
// A header owns a `schema` — with `properties` of its own — and may own a
// `content` block, so all three routes into the payload rules run through here.
func TestDiffOpenAPI_ResponseHeaderRemovalGradesMonotonically(t *testing.T) {
	full := `"200":
          description: OK
          headers:
            X-Rate-Limit:
              description: quota
              schema: {type: integer}
            X-Ctx:
              schema:
                type: object
                properties:
                  tenant: {type: string}
                  region: {type: string}
            X-Body:
              content:
                application/json:
                  schema: {type: string}
`
	// Every variant drops exactly one thing from `full`; the rest is repeated so
	// each subtest reads as the document it actually diffs.
	rest := `            X-Ctx:
              schema:
                type: object
                properties:
                  tenant: {type: string}
                  region: {type: string}
            X-Body:
              content:
                application/json:
                  schema: {type: string}
`
	limitAndBody := `            X-Rate-Limit:
              description: quota
              schema: {type: integer}
            X-Body:
              content:
                application/json:
                  schema: {type: string}
`
	limitAndCtx := `            X-Rate-Limit:
              description: quota
              schema: {type: integer}
            X-Ctx:
              schema:
                type: object
                properties:
                  tenant: {type: string}
                  region: {type: string}
`
	tests := []struct {
		name string
		new  string
		path string
	}{
		{"header schema removed", `"200":
          description: OK
          headers:
            X-Rate-Limit:
              description: quota
` + rest, "openapi.paths[/users].methods[GET].responses[200].headers.X-Rate-Limit.schema"},
		{"header schema property removed", `"200":
          description: OK
          headers:
` + limitAndBody + `            X-Ctx:
              schema:
                type: object
                properties:
                  region: {type: string}
`, "openapi.paths[/users].methods[GET].responses[200].headers.X-Ctx.schema.properties.tenant"},
		{"header content block removed", `"200":
          description: OK
          headers:
` + limitAndCtx + `            X-Body: {}
`, "openapi.paths[/users].methods[GET].responses[200].headers.X-Body.content"},
		{"whole header removed", `"200":
          description: OK
          headers:
` + rest, "openapi.paths[/users].methods[GET].responses[200].headers.X-Rate-Limit"},
		{"headers block removed", `"200":
          description: OK
`, "openapi.paths[/users].methods[GET].responses[200].headers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := diffOpenAPI("openapi.yaml", "openapi.yaml", responseSpec(full), responseSpec(tt.new))
			c, ok := findChange(changes, tt.path, Removed)
			if !ok {
				t.Fatalf("expected a removal at %s, got %+v", tt.path, changes)
			}
			if c.Classification != PotentialBreaking {
				t.Errorf("expected POTENTIAL_BREAKING, got %s", c.Classification)
			}
		})
	}
}

// `.inf` and `.nan` are legal YAML numbers, and encoding/json refuses the
// float they decode to: one in any numeric constraint used to make the whole
// Result unmarshalable, so `pacto diff --output-format json` printed nothing
// and the dashboard's /api/diff, which copies the value verbatim, answered 500.
func TestDiffOpenAPI_NonFiniteNumberMarshalsAsJSON(t *testing.T) {
	oldFS := responseSpec(`"200":
          description: OK
`)
	newFS := responseSpec(`"200":
          description: OK
          content:
            application/json:
              schema: {type: number, maximum: .inf}
`)
	changes := diffOpenAPI("openapi.yaml", "openapi.yaml", oldFS, newFS)
	out, err := json.Marshal(&Result{Changes: changes})
	if err != nil {
		t.Fatalf("Result must marshal as JSON: %v", err)
	}
	if !strings.Contains(string(out), `"maximum":"+Inf"`) {
		t.Errorf("expected the infinity rendered as \"+Inf\", got %s", out)
	}
}

// Two keys of one YAML mapping can stringify to the same string (`1:` and
// `1.0:` decode to an int and a float that print alike, and yaml.v3 keeps both),
// and ranging the Go map picked a different winner per run: the same two specs
// diffed differently on consecutive runs.
func TestToStringMap_CollidingStringifiedKeysAreDeterministic(t *testing.T) {
	for i := 0; i < 200; i++ {
		got := toStringMap(map[any]any{200: "int", "200": "str", "other": "x"})
		if len(got) != 2 || got["200"] != "int" {
			t.Fatalf("expected the first key in sorted order to win, got %#v", got)
		}
	}
}

// The colliding entries can tie on the plain rendering of their values too —
// `true` and `"true"` print alike — and those two marshal to different JSON, so
// leaving that tie to the map iteration changed the diff output run to run.
func TestToStringMap_CollidingKeysWithAlikeValuesAreDeterministic(t *testing.T) {
	for i := 0; i < 200; i++ {
		got := toStringMap(map[any]any{1: true, 1.0: "true"})
		if len(got) != 1 || got["1"] != "true" {
			t.Fatalf("expected the same winner on every run, got %#v", got)
		}
	}
}
