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

	// Should detect all three child properties, not just "telemetry".
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes (telemetry.enabled, telemetry.endpoint, telemetry.sample_rate), got %d: %+v", len(changes), changes)
	}
	added := make(map[string]bool)
	for _, c := range changes {
		if c.Type != Added {
			t.Errorf("expected Added change, got %v", c.Type)
		}
		added[c.NewValue.(string)] = true
	}
	for _, want := range []string{"telemetry.enabled", "telemetry.endpoint", "telemetry.sample_rate"} {
		if !added[want] {
			t.Errorf("expected %q to be added, got changes: %+v", want, changes)
		}
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
