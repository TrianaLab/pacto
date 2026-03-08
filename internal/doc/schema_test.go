package doc

import (
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"
)

func TestReadSchemaProperties_Basic(t *testing.T) {
	schema := `{
  "type": "object",
  "properties": {
    "PORT": {
      "type": "integer",
      "description": "HTTP server port",
      "default": 8080
    },
    "REDIS_URL": {
      "type": "string",
      "description": "Redis connection string"
    }
  },
  "required": ["PORT", "REDIS_URL"]
}`
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(schema)},
	}

	props, err := readSchemaProperties(fsys, "schema.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(props) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(props))
	}

	// Sorted alphabetically: PORT, REDIS_URL
	if props[0].Name != "PORT" {
		t.Errorf("expected first property PORT, got %s", props[0].Name)
	}
	if props[0].Type != "integer" {
		t.Errorf("expected type integer, got %s", props[0].Type)
	}
	if props[0].Default != "8080" {
		t.Errorf("expected default 8080, got %s", props[0].Default)
	}
	if !props[0].Required {
		t.Error("expected PORT to be required")
	}

	if props[1].Name != "REDIS_URL" {
		t.Errorf("expected second property REDIS_URL, got %s", props[1].Name)
	}
	if props[1].Default != "" {
		t.Errorf("expected empty default, got %s", props[1].Default)
	}
	if !props[1].Required {
		t.Error("expected REDIS_URL to be required")
	}
}

func TestReadSchemaProperties_NoRequired(t *testing.T) {
	schema := `{
  "type": "object",
  "properties": {
    "DEBUG": {
      "type": "boolean",
      "description": "Enable debug mode",
      "default": false
    }
  }
}`
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(schema)},
	}

	props, err := readSchemaProperties(fsys, "schema.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(props) != 1 {
		t.Fatalf("expected 1 property, got %d", len(props))
	}

	if props[0].Required {
		t.Error("expected DEBUG to not be required")
	}
	if props[0].Default != "false" {
		t.Errorf("expected default 'false', got %s", props[0].Default)
	}
}

func TestReadSchemaProperties_EmptyProperties(t *testing.T) {
	schema := `{"type": "object"}`
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(schema)},
	}

	props, err := readSchemaProperties(fsys, "schema.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(props) != 0 {
		t.Errorf("expected 0 properties, got %d", len(props))
	}
}

func TestReadSchemaProperties_MissingFile(t *testing.T) {
	fsys := fstest.MapFS{}

	_, err := readSchemaProperties(fsys, "nonexistent.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadSchemaProperties_InvalidJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.json": &fstest.MapFile{Data: []byte("{invalid")},
	}

	_, err := readSchemaProperties(fsys, "bad.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// staticFS serves the same content for any path, bypassing fs.ValidPath checks.
type staticFS struct{ data []byte }

func (f staticFS) Open(name string) (fs.File, error) {
	return &staticFile{data: f.data}, nil
}

type staticFile struct {
	data   []byte
	offset int
}

func (f *staticFile) Stat() (fs.FileInfo, error) {
	return staticFileInfo{size: int64(len(f.data))}, nil
}
func (f *staticFile) Read(b []byte) (int, error) {
	if f.offset >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(b, f.data[f.offset:])
	f.offset += n
	return n, nil
}
func (f *staticFile) Close() error { return nil }

type staticFileInfo struct{ size int64 }

func (fi staticFileInfo) Name() string       { return "schema.json" }
func (fi staticFileInfo) Size() int64        { return fi.size }
func (fi staticFileInfo) Mode() fs.FileMode  { return 0o444 }
func (fi staticFileInfo) ModTime() time.Time { return time.Time{} }
func (fi staticFileInfo) IsDir() bool        { return false }
func (fi staticFileInfo) Sys() any           { return nil }

func TestReadSchemaProperties_InvalidResourceURL(t *testing.T) {
	fsys := staticFS{data: []byte(`{"type": "object"}`)}

	_, err := readSchemaProperties(fsys, "://invalid")
	if err == nil {
		t.Error("expected error for invalid resource URL")
	}
}

func TestReadSchemaProperties_RefDefinition(t *testing.T) {
	schema := `{
  "$schema": "http://json-schema.org/draft-06/schema#",
  "$ref": "#/definitions/Config",
  "definitions": {
    "Config": {
      "type": "object",
      "properties": {
        "log_level": {
          "type": "string",
          "description": "Logging level"
        },
        "port": {
          "type": "integer",
          "description": "Server port",
          "default": 8080
        }
      },
      "required": ["port"]
    }
  }
}`
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(schema)},
	}

	props, err := readSchemaProperties(fsys, "schema.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(props) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(props))
	}
	// Sorted: log_level, port
	if props[0].Name != "log_level" {
		t.Errorf("expected log_level, got %s", props[0].Name)
	}
	if props[0].Required {
		t.Error("log_level should not be required")
	}
	if props[1].Name != "port" {
		t.Errorf("expected port, got %s", props[1].Name)
	}
	if !props[1].Required {
		t.Error("port should be required")
	}
	if props[1].Default != "8080" {
		t.Errorf("expected default 8080, got %s", props[1].Default)
	}
}

func TestReadSchemaProperties_RefMissingDefinition(t *testing.T) {
	schema := `{
  "$ref": "#/definitions/Missing",
  "definitions": {}
}`
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(schema)},
	}

	_, err := readSchemaProperties(fsys, "schema.json")
	if err == nil {
		t.Error("expected error for missing $ref definition")
	}
}

func TestReadSchemaProperties_RefNonLocalIgnored(t *testing.T) {
	schema := `{
  "$ref": "https://example.com/schema.json"
}`
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(schema)},
	}

	_, err := readSchemaProperties(fsys, "schema.json")
	if err == nil {
		t.Error("expected error for unresolvable external $ref")
	}
}
