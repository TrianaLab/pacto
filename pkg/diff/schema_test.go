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

// worst returns the most severe classification among changes, and whether
// there was any change at all.
func worst(changes []Change) (Classification, bool) {
	w := NonBreaking
	for _, c := range changes {
		w = max(w, c.Classification)
	}
	return w, len(changes) > 0
}

// TestPositionalCompatibility is the compatibility model as a table: each
// schema edit judged in the three positions a schema can sit in. A request
// (old caller, new provider) tolerates widening, a response (new provider, old
// reader) tolerates narrowing, and a record that flows both ways tolerates
// neither. Doc-only edits pass everywhere; an edit the model cannot place is
// breaking everywhere. A property a request stops declaring stays
// POTENTIAL_BREAKING: the provider drops it unread, which may or may not
// matter to the caller.
func TestPositionalCompatibility(t *testing.T) {
	const (
		nb = NonBreaking
		pb = PotentialBreaking
		br = Breaking
	)
	tests := []struct {
		name          string
		old, new      string
		req, resp, rc Classification
	}{
		{"enum value added", `{"enum":["a"]}`, `{"enum":["a","b"]}`, nb, br, br},
		{"enum value removed", `{"enum":["a","b"]}`, `{"enum":["a"]}`, br, nb, br},
		{"numeric enum value added", `{"enum":[1]}`, `{"enum":[1,2]}`, nb, br, br},
		{"numeric enum value removed", `{"enum":[1,2]}`, `{"enum":[1]}`, br, nb, br},
		{"numeric enum value replaced", `{"enum":[1,2]}`, `{"enum":[1,3]}`, br, br, br},
		{"enum introduced", `{"type":"string"}`, `{"type":"string","enum":["a"]}`, br, nb, br},
		{"enum dropped", `{"type":"string","enum":["a"]}`, `{"type":"string"}`, nb, br, br},
		{"const introduced", `{}`, `{"const":"a"}`, br, nb, br},
		{"oneOf variant added", `{"oneOf":[{"type":"string"}]}`, `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, nb, br, br},
		{"oneOf variant removed", `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, `{"oneOf":[{"type":"string"}]}`, br, nb, br},
		{"anyOf variant added", `{"anyOf":[{"type":"string"}]}`, `{"anyOf":[{"type":"string"},{"type":"integer"}]}`, nb, br, br},
		{"allOf member added", `{"allOf":[{"type":"object"}]}`, `{"allOf":[{"type":"object"},{"required":["a"]}]}`, br, nb, br},
		{"allOf member removed", `{"allOf":[{"type":"object"},{"required":["a"]}]}`, `{"allOf":[{"type":"object"}]}`, nb, br, br},
		{"maxLength tightened", `{"maxLength":10}`, `{"maxLength":5}`, br, nb, br},
		{"maxLength loosened", `{"maxLength":5}`, `{"maxLength":10}`, nb, br, br},
		{"maxLength introduced", `{}`, `{"maxLength":5}`, br, nb, br},
		{"maxLength dropped", `{"maxLength":5}`, `{}`, nb, br, br},
		{"minLength tightened", `{"minLength":1}`, `{"minLength":3}`, br, nb, br},
		{"minLength loosened", `{"minLength":3}`, `{"minLength":1}`, nb, br, br},
		{"minimum tightened", `{"minimum":0}`, `{"minimum":1}`, br, nb, br},
		{"maximum loosened", `{"maximum":10}`, `{"maximum":100}`, nb, br, br},
		{"maxItems loosened", `{"maxItems":1}`, `{"maxItems":2}`, nb, br, br},
		{"minItems tightened", `{"minItems":0}`, `{"minItems":1}`, br, nb, br},
		{"exclusiveMaximum flag set", `{"maximum":5,"exclusiveMaximum":false}`, `{"maximum":5,"exclusiveMaximum":true}`, br, nb, br},
		{"exclusiveMinimum value loosened", `{"exclusiveMinimum":5}`, `{"exclusiveMinimum":1}`, nb, br, br},
		{"exclusiveMaximum flag to value", `{"exclusiveMaximum":true}`, `{"exclusiveMaximum":5}`, br, br, br},
		{"uniqueItems introduced", `{}`, `{"uniqueItems":true}`, br, nb, br},
		{"pattern introduced", `{}`, `{"pattern":"^a"}`, br, nb, br},
		{"pattern dropped", `{"pattern":"^a"}`, `{}`, nb, br, br},
		{"pattern changed", `{"pattern":"^a"}`, `{"pattern":"^b"}`, br, br, br},
		{"format introduced", `{"type":"string"}`, `{"type":"string","format":"uuid"}`, br, nb, br},
		{"multipleOf changed", `{"multipleOf":2}`, `{"multipleOf":3}`, br, br, br},
		{"required added", `{"required":["a"]}`, `{"required":["a","b"]}`, br, nb, br},
		{"required removed", `{"required":["a","b"]}`, `{"required":["a"]}`, nb, br, br},
		{"additionalProperties closed", `{"type":"object"}`, `{"type":"object","additionalProperties":false}`, br, nb, br},
		{"additionalProperties opened", `{"type":"object","additionalProperties":false}`, `{"type":"object"}`, nb, br, br},
		{"additionalProperties true to false", `{"additionalProperties":true}`, `{"additionalProperties":false}`, br, nb, br},
		{"additionalProperties false to schema", `{"additionalProperties":false}`, `{"additionalProperties":{"type":"string"}}`, nb, br, br},
		{"additionalProperties schema loosened", `{"additionalProperties":{"maxLength":1}}`, `{"additionalProperties":{"maxLength":2}}`, nb, br, br},
		{"additionalProperties true spelled out", `{}`, `{"additionalProperties":true}`, nb, nb, nb},
		{"type widened integer to number", `{"type":"integer"}`, `{"type":"number"}`, nb, br, br},
		{"type narrowed number to integer", `{"type":"number"}`, `{"type":"integer"}`, br, nb, br},
		{"type changed string to integer", `{"type":"string"}`, `{"type":"integer"}`, br, br, br},
		{"type made nullable", `{"type":"string"}`, `{"type":["string","null"]}`, nb, br, br},
		{"type list gains a member", `{"type":["string"]}`, `{"type":["string","null"]}`, nb, br, br},
		{"type list loses a member", `{"type":["string","null"]}`, `{"type":["string"]}`, br, nb, br},
		{"type introduced", `{}`, `{"type":"string"}`, br, nb, br},
		{"redundant integer dropped from type list", `{"type":["integer","number"]}`, `{"type":"number"}`, nb, nb, nb},
		{"type list with a non-string member", `{"type":"string"}`, `{"type":["string",1]}`, br, br, br},
		{"items turned into a tuple", `{"items":{"type":"string"}}`, `{"items":[{"type":"string"}]}`, br, br, br},
		{"tuple member added", `{"items":[{"type":"string"}]}`, `{"items":[{"type":"string"},{"type":"string"}]}`, br, br, br},
		{"nullable set", `{"type":"string"}`, `{"type":"string","nullable":true}`, nb, br, br},
		{"items retyped", `{"items":{"type":"string"}}`, `{"items":{"type":"integer"}}`, br, br, br},
		{"items loosened", `{"items":{"maxLength":1}}`, `{"items":{"maxLength":2}}`, nb, br, br},
		{"items introduced", `{"type":"array"}`, `{"type":"array","items":{"type":"string"}}`, br, nb, br},
		{"optional property added", `{"properties":{}}`, `{"properties":{"a":{"type":"string"}}}`, nb, nb, nb},
		{"optional property removed", `{"properties":{"a":{"type":"string"}}}`, `{"properties":{}}`, pb, br, br},
		{"property added to a closed object", `{"additionalProperties":false,"properties":{}}`, `{"additionalProperties":false,"properties":{"a":{}}}`, nb, br, br},
		{"property removed from a closed object", `{"additionalProperties":false,"properties":{"a":{}}}`, `{"additionalProperties":false,"properties":{}}`, br, br, br},
		{"properties block introduced", `{"type":"object"}`, `{"type":"object","properties":{"a":{}}}`, nb, nb, nb},
		{"property type changed", `{"properties":{"a":{"type":"string"}}}`, `{"properties":{"a":{"type":"integer"}}}`, br, br, br},
		{"property named description removed", `{"properties":{"description":{"type":"string"}}}`, `{"properties":{}}`, pb, br, br},
		{"property named title retyped", `{"properties":{"title":{"type":"string"}}}`, `{"properties":{"title":{"type":"integer"}}}`, br, br, br},
		{"description changed", `{"description":"a"}`, `{"description":"b"}`, nb, nb, nb},
		{"title added", `{}`, `{"title":"T"}`, nb, nb, nb},
		{"examples changed", `{"examples":["a"]}`, `{"examples":["b"]}`, nb, nb, nb},
		{"comment added", `{}`, `{"$comment":"why"}`, nb, nb, nb},
		{"nested example changed", `{"properties":{"a":{"example":{"x":1}}}}`, `{"properties":{"a":{"example":{"x":2}}}}`, nb, nb, nb},
		{"deprecated set", `{"properties":{"a":{"type":"string"}}}`, `{"properties":{"a":{"type":"string","deprecated":true}}}`, nb, nb, nb},
		{"default changed", `{"default":"a"}`, `{"default":"b"}`, br, br, br},
		{"unknown keyword", `{"x-kind":"a"}`, `{"x-kind":"b"}`, br, br, br},
		{"not body changed", `{"not":{"type":"string"}}`, `{"not":{"type":["string","null"]}}`, br, br, br},
		{"ref repointed", `{"$ref":"#/a"}`, `{"$ref":"#/b"}`, br, br, br},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var old, new any
			if err := json.Unmarshal([]byte(tt.old), &old); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.new), &new); err != nil {
				t.Fatal(err)
			}
			for _, pos := range []struct {
				dir  schemaDirection
				want Classification
			}{{dirRequest, tt.req}, {dirResponse, tt.resp}, {dirRecord, tt.rc}} {
				got, changed := worst(diffJSON(pos.dir, "body.schema", old, new))
				if !changed {
					t.Fatalf("no change detected")
				}
				if got != pos.want {
					t.Errorf("direction %d: got %s, want %s", pos.dir, got, pos.want)
				}
			}
		})
	}
}

// yaml.v3 decodes an integer bound as int, encoding/json as float64; both
// rank. A bound that is not a number cannot be ranked and fails closed.
func TestKeywordEffect_BoundValues(t *testing.T) {
	if got := keywordEffect("maxLength", false, Modified, 5, 10); got != effectWiden {
		t.Errorf("int bound: got %d", got)
	}
	if got := keywordEffect("maxLength", false, Modified, 5, "ten"); got != effectUnknown {
		t.Errorf("non-number bound: got %d", got)
	}
}

// A configuration or policy schema keeps its conservative grading: a
// structural edit stays POTENTIAL_BREAKING and `required` stays BREAKING both
// ways. Only a doc-only edit changes, because documentation never counts.
func TestUnknownDirection_KeepsDefaultsButIgnoresDocs(t *testing.T) {
	var old, new any
	_ = json.Unmarshal([]byte(`{"description":"a","enum":["x"]}`), &old)
	_ = json.Unmarshal([]byte(`{"description":"b","enum":["x","y"]}`), &new)
	got := map[string]Classification{}
	for _, c := range diffJSON(dirUnknown, "schema", old, new) {
		got[c.Path] = c.Classification
	}
	if got["schema.description"] != NonBreaking {
		t.Errorf("description: got %s", got["schema.description"])
	}
	if got["schema.enum[y]"] != PotentialBreaking {
		t.Errorf("enum: got %s", got["schema.enum[y]"])
	}
}
