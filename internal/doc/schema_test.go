package doc

import (
	"testing"
	"testing/fstest"
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
