package diff

import (
	"testing"
	"testing/fstest"
)

func TestDiffSchema_BothFSNil(t *testing.T) {
	changes := diffSchema("schema.json", "schema.json", nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_EmptyPath(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffSchema("", "", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_BothReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffSchema("missing.json", "missing.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_OldReadError(t *testing.T) {
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{}}`)},
	}
	changes := diffSchema("schema.json", "schema.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffSchema_NewReadError(t *testing.T) {
	oldFS := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{}}`)},
	}
	newFS := fstest.MapFS{}
	changes := diffSchema("schema.json", "schema.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestReadSchemaProperties_InvalidJSON(t *testing.T) {
	fs := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(`{invalid}`)},
	}
	_, err := readSchemaProperties(fs, "schema.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestDiffSchema_NestedPropertyAdded(t *testing.T) {
	oldSchema := `{
  "type": "object",
  "properties": {
    "existing": {
      "type": "object",
      "properties": {
        "host": { "type": "string", "description": "Hostname" }
      }
    }
  }
}`
	newSchema := `{
  "type": "object",
  "properties": {
    "existing": {
      "type": "object",
      "properties": {
        "host": { "type": "string", "description": "Hostname" }
      }
    },
    "telemetry": {
      "type": "object",
      "properties": {
        "enabled": { "type": "boolean", "default": true },
        "endpoint": { "type": "string" },
        "sample_rate": { "type": "number", "default": 1.0 }
      }
    }
  }
}`
	oldFS := fstest.MapFS{"schema.json": &fstest.MapFile{Data: []byte(oldSchema)}}
	newFS := fstest.MapFS{"schema.json": &fstest.MapFile{Data: []byte(newSchema)}}

	changes := diffSchema("schema.json", "schema.json", oldFS, newFS)

	// The recursive diff detects the new top-level "telemetry" object as a single addition.
	if len(changes) != 1 {
		t.Fatalf("expected 1 change (telemetry added), got %d: %+v", len(changes), changes)
	}
	c := changes[0]
	if c.Type != Added {
		t.Errorf("expected Added change, got %v", c.Type)
	}
	if c.Path != "schema.properties.telemetry" {
		t.Errorf("expected path schema.properties.telemetry, got %s", c.Path)
	}
}

func TestDiffSchema_InvalidOldJSON(t *testing.T) {
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`not json`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for invalid JSON, got %d", len(changes))
	}
}

func TestDiffSchema_InvalidNewJSON(t *testing.T) {
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`not json`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for invalid JSON, got %d", len(changes))
	}
}

func TestDiffSchema_RequiredFieldAddedIsBreaking(t *testing.T) {
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"type":"object",
		"required": ["a"]
	}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"type":"object",
		"required": ["a", "b"]
	}`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Classification != Breaking {
		t.Errorf("expected BREAKING for required change, got %s", changes[0].Classification)
	}
	if changes[0].Type != Added {
		t.Errorf("expected Added, got %s", changes[0].Type)
	}
}

func TestDiffSchema_RequiredFieldRemovedIsBreaking(t *testing.T) {
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"type":"object",
		"required": ["a", "b"]
	}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"type":"object",
		"required": ["a"]
	}`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Classification != Breaking {
		t.Errorf("expected BREAKING for required change, got %s", changes[0].Classification)
	}
	if changes[0].Type != Removed {
		t.Errorf("expected Removed, got %s", changes[0].Type)
	}
}

func TestDiffSchema_ScalarTypeChange(t *testing.T) {
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{"type":"array"}`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Modified {
		t.Errorf("expected Modified, got %s", changes[0].Type)
	}
}

func TestDiffSchema_NonStringArrayPositional(t *testing.T) {
	// Arrays of non-strings use positional comparison (not set comparison).
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"items": [{"type":"string"}, {"type":"number"}]
	}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"items": [{"type":"string"}, {"type":"number"}, {"type":"boolean"}]
	}`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected an Added change for new array element")
	}
}

func TestDiffSchema_ArrayElementRemoved(t *testing.T) {
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"items": [{"type":"string"}, {"type":"number"}]
	}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"items": [{"type":"string"}]
	}`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected a Removed change for removed array element")
	}
}

func TestDiffSchema_PropertyRemoved(t *testing.T) {
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"type":"object",
		"properties": {"a":{"type":"string"}, "b":{"type":"number"}}
	}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{
		"type":"object",
		"properties": {"a":{"type":"string"}}
	}`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Removed {
		t.Errorf("expected Removed, got %s", changes[0].Type)
	}
	if changes[0].Path != "schema.properties.b" {
		t.Errorf("expected path schema.properties.b, got %s", changes[0].Path)
	}
}

func TestDiffSchema_TypeMismatchScalar(t *testing.T) {
	// Old is an object, new is a scalar — type mismatch at top level.
	oldFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)}}
	newFS := fstest.MapFS{"s.json": &fstest.MapFile{Data: []byte(`"just a string"`)}}
	changes := diffSchema("s.json", "s.json", oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Modified {
		t.Errorf("expected Modified for type mismatch, got %s", changes[0].Type)
	}
}

func TestReadSchemaProperties_FileNotFound(t *testing.T) {
	fs := fstest.MapFS{}
	_, err := readSchemaProperties(fs, "missing.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadSchemaProperties_FlattensNested(t *testing.T) {
	schema := `{
  "type": "object",
  "properties": {
    "database": {
      "type": "object",
      "properties": {
        "host": { "type": "string" },
        "connection": {
          "type": "object",
          "properties": {
            "timeout": { "type": "integer" },
            "retries": { "type": "integer" }
          }
        }
      }
    },
    "port": { "type": "integer" }
  }
}`
	fs := fstest.MapFS{"schema.json": &fstest.MapFile{Data: []byte(schema)}}

	props, err := readSchemaProperties(fs, "schema.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]bool{
		"database.host":               true,
		"database.connection.timeout": true,
		"database.connection.retries": true,
		"port":                        true,
	}
	if len(props) != len(want) {
		t.Fatalf("expected %d properties, got %d: %v", len(want), len(props), props)
	}
	for k := range want {
		if !props[k] {
			t.Errorf("missing expected property %q", k)
		}
	}
}
