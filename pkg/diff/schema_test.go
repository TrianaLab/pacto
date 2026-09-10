package diff

import (
	"encoding/json"
	"math"
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

// A key under `content` that is not a media type — no type/subtype slash — is
// not a payload, so a malformed spec cannot escalate one.
func TestIsResponsePayload_ContentChildIsNotAMediaType(t *testing.T) {
	if isResponsePayload("openapi.paths[/u].methods[GET].responses[200].content.plain") {
		t.Error("a content child without a slash is not a media type")
	}
}

// A `properties` block outside the schema — inside an `example`, say — is not
// the payload's field set, so it keeps the default grade. The nested case is the
// sharp one: an endpoint that serves JSON Schema documents has an example that
// itself contains `schema.properties`, and rewriting an example changes nothing
// a consumer parses.
func TestIsResponsePayload_PropertiesOutsideTheSchema(t *testing.T) {
	base := "openapi.paths[/u].methods[GET].responses[200].content.application/json."
	for _, tail := range []string{
		"example.properties",
		"example.schema.properties",
		"example.schema",
		"examples.default.schema.properties",
	} {
		if isResponsePayload(base + tail) {
			t.Errorf("%s is documentation, not the payload's field set", tail)
		}
	}
}

// A media type whose own name contains a media-type field word still counts as
// the payload when it is removed whole: the exclusion is about the path passing
// THROUGH a field, not about the letters appearing in it.
func TestIsResponsePayload_MediaTypeNamedLikeAField(t *testing.T) {
	base := "openapi.paths[/u].methods[GET].responses[200].content."
	if !isResponsePayload(base + "application/vnd.schema+json") {
		t.Error("removing a whole media type is the payload, whatever it is called")
	}
	if !isResponsePayload(base + "application/vnd.schema+json.schema.properties") {
		t.Error("that media type's own schema properties are still the payload")
	}
}

// encoding/json refuses a non-finite float, so jsonSafe renders one as its text
// form. Not null: a diff value is read by a human, and null would claim the
// field was absent when it was present and infinite.
func TestJSONSafe_NonFiniteFloats(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"positive infinity", math.Inf(1), "+Inf"},
		{"negative infinity", math.Inf(-1), "-Inf"},
		{"not a number", math.NaN(), "NaN"},
		{"finite float64", 1.5, 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonSafe(map[string]any{"maximum": tt.in})
			if got.(map[string]any)["maximum"] != tt.want {
				t.Errorf("expected %v, got %#v", tt.want, got)
			}
			if _, err := json.Marshal(got); err != nil {
				t.Errorf("must marshal as JSON: %v", err)
			}
		})
	}
}

// Two keys of one YAML mapping can stringify to the same string, and ranging
// the Go map picked a different winner per run.
func TestJSONSafe_CollidingStringifiedKeysAreDeterministic(t *testing.T) {
	for i := 0; i < 200; i++ {
		got := jsonSafe(map[any]any{200: "int", "200": "str", "other": "x"}).(map[string]any)
		if len(got) != 2 || got["200"] != "int" {
			t.Fatalf("expected the first key in sorted order to win, got %#v", got)
		}
	}
}
