package doc

import (
	"testing"
	"testing/fstest"
)

func TestReadOpenAPIEndpoints_Basic(t *testing.T) {
	spec := `
openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health check
  /payments:
    get:
      summary: List payments
    post:
      summary: Create a payment
`
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(spec)},
	}

	endpoints, err := readOpenAPIEndpoints(fsys, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(endpoints) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(endpoints))
	}

	// Paths should be sorted alphabetically: /health, /payments
	if endpoints[0].Path != "/health" {
		t.Errorf("expected first path /health, got %s", endpoints[0].Path)
	}
	if endpoints[0].Method != "get" {
		t.Errorf("expected method get, got %s", endpoints[0].Method)
	}
	if endpoints[0].Summary != "Health check" {
		t.Errorf("expected summary 'Health check', got %s", endpoints[0].Summary)
	}

	// /payments GET before POST
	if endpoints[1].Method != "get" || endpoints[1].Path != "/payments" {
		t.Errorf("expected GET /payments second, got %s %s", endpoints[1].Method, endpoints[1].Path)
	}
	if endpoints[2].Method != "post" || endpoints[2].Path != "/payments" {
		t.Errorf("expected POST /payments third, got %s %s", endpoints[2].Method, endpoints[2].Path)
	}
}

func TestReadOpenAPIEndpoints_PathValueNotMap(t *testing.T) {
	spec := `
openapi: "3.0.0"
paths:
  /items: "not a map"
  /health:
    get:
      summary: Health
`
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(spec)},
	}

	endpoints, err := readOpenAPIEndpoints(fsys, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// /items is skipped because its value is not a map, only /health remains
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].Path != "/health" {
		t.Errorf("expected /health, got %s", endpoints[0].Path)
	}
}

func TestReadOpenAPIEndpoints_MethodValueNotMap(t *testing.T) {
	spec := `
openapi: "3.0.0"
paths:
  /items:
    get: "not a map"
    post:
      summary: Create
`
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(spec)},
	}

	endpoints, err := readOpenAPIEndpoints(fsys, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// get is skipped because its value is not a map
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].Method != "post" {
		t.Errorf("expected post, got %s", endpoints[0].Method)
	}
}

func TestReadOpenAPIEndpoints_FiltersNonHTTPKeys(t *testing.T) {
	spec := `
openapi: "3.0.0"
paths:
  /items:
    parameters:
      - name: id
        in: path
    summary: not a method
    get:
      summary: List items
`
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(spec)},
	}

	endpoints, err := readOpenAPIEndpoints(fsys, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint (non-HTTP keys filtered), got %d", len(endpoints))
	}
	if endpoints[0].Method != "get" {
		t.Errorf("expected method get, got %s", endpoints[0].Method)
	}
}

func TestReadOpenAPIEndpoints_PathsNotMap(t *testing.T) {
	spec := `
openapi: "3.0.0"
paths: "not a map"
`
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(spec)},
	}

	endpoints, err := readOpenAPIEndpoints(fsys, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(endpoints) != 0 {
		t.Errorf("expected 0 endpoints for non-map paths, got %d", len(endpoints))
	}
}

func TestReadOpenAPIEndpoints_NoPaths(t *testing.T) {
	spec := `
openapi: "3.0.0"
info:
  title: Empty API
`
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(spec)},
	}

	endpoints, err := readOpenAPIEndpoints(fsys, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(endpoints) != 0 {
		t.Errorf("expected 0 endpoints, got %d", len(endpoints))
	}
}

func TestReadOpenAPIEndpoints_MissingFile(t *testing.T) {
	fsys := fstest.MapFS{}

	_, err := readOpenAPIEndpoints(fsys, "nonexistent.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadOpenAPIEndpoints_InvalidYAML(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.yaml": &fstest.MapFile{Data: []byte(":::invalid")},
	}

	_, err := readOpenAPIEndpoints(fsys, "bad.yaml")
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestReadOpenAPIEndpoints_MethodOrder(t *testing.T) {
	spec := `
openapi: "3.0.0"
paths:
  /resource:
    delete:
      summary: Delete
    get:
      summary: Get
    post:
      summary: Create
    put:
      summary: Update
    patch:
      summary: Patch
`
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(spec)},
	}

	endpoints, err := readOpenAPIEndpoints(fsys, "openapi.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"get", "post", "put", "patch", "delete"}
	if len(endpoints) != len(expected) {
		t.Fatalf("expected %d endpoints, got %d", len(expected), len(endpoints))
	}
	for i, want := range expected {
		if endpoints[i].Method != want {
			t.Errorf("position %d: expected method %s, got %s", i, want, endpoints[i].Method)
		}
	}
}
