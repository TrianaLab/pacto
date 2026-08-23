package validation

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func TestYamlToGeneric_InvalidYAML(t *testing.T) {
	_, err := yamlToGeneric([]byte(`{invalid yaml`))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestYamlToGeneric_ValidScalar(t *testing.T) {
	result, err := yamlToGeneric([]byte(`42`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After JSON round-trip, int becomes float64
	if result != float64(42) {
		t.Errorf("expected 42, got %v", result)
	}
}

// TestYamlToGeneric_TimestampScalarsStayVerbatim: the schema layer must see the
// literal text in the document. Resolving these to time.Time would show the
// schema an RFC3339 string for a bare date; reformatting them would show it a
// bare date for an explicit timestamp. Both are values that are not in the file.
func TestYamlToGeneric_TimestampScalarsStayVerbatim(t *testing.T) {
	result, err := yamlToGeneric([]byte("expires: 2099-12-31\nname: 2024-01-15T00:00:00Z\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["expires"] != "2099-12-31" {
		t.Errorf("expires: got %v, want 2099-12-31", m["expires"])
	}
	if m["name"] != "2024-01-15T00:00:00Z" {
		t.Errorf("name: got %v, want 2024-01-15T00:00:00Z", m["name"])
	}
}

// TestValidateStructuralRaw_UnquotedTimestampFailsPattern is the fail-open guard:
// an unquoted timestamp in a pattern-constrained field must still be rejected.
// Truncating it to a bare date would make it match ^[a-z0-9-]+$ and turn a
// rejected contract into an accepted one.
func TestValidateStructuralRaw_UnquotedTimestampFailsPattern(t *testing.T) {
	raw := []byte(`pactoVersion: "2.0"
service:
  name: 2024-01-15T00:00:00Z
  version: "1.0.0"
workload: service
`)
	result := ValidateStructuralRaw(raw)
	if result.IsValid() {
		t.Fatal("expected service.name to be rejected, got a valid result")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "SCHEMA_VIOLATION" && e.Path == "service.name" && strings.Contains(e.Message, "2024-01-15T00:00:00Z") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a SCHEMA_VIOLATION on service.name naming the literal scalar, got %+v", result.Errors)
	}
}

func TestConvertYAMLToJSON_MapInterfaceNested(t *testing.T) {
	input := map[any]any{
		"nested": map[any]any{
			"key": "value",
		},
	}
	result := convertYAMLToJSON(input)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}
	nested, ok := m["nested"].(map[string]any)
	if !ok {
		t.Fatal("expected nested map[string]interface{}")
	}
	if nested["key"] != "value" {
		t.Errorf("expected value, got %v", nested["key"])
	}
}

func TestConvertYAMLToJSON_Slice(t *testing.T) {
	input := []any{"a", "b", map[any]any{"key": "val"}}
	result := convertYAMLToJSON(input)
	s, ok := result.([]any)
	if !ok {
		t.Fatal("expected []interface{}")
	}
	if len(s) != 3 {
		t.Errorf("expected 3 items, got %d", len(s))
	}
	m, ok := s[2].(map[string]any)
	if !ok {
		t.Fatal("expected nested map in slice")
	}
	if m["key"] != "val" {
		t.Errorf("expected val, got %v", m["key"])
	}
}

func TestConvertYAMLToJSON_MapStringNested(t *testing.T) {
	input := map[string]any{
		"a": []any{1, 2},
	}
	result := convertYAMLToJSON(input)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}
	s, ok := m["a"].([]any)
	if !ok {
		t.Fatal("expected []interface{}")
	}
	if len(s) != 2 {
		t.Errorf("expected 2 items, got %d", len(s))
	}
}

func TestConvertYAMLToJSON_Scalar(t *testing.T) {
	if result := convertYAMLToJSON(42); result != 42 {
		t.Errorf("expected 42, got %v", result)
	}
	if result := convertYAMLToJSON("hello"); result != "hello" {
		t.Errorf("expected hello, got %v", result)
	}
	if result := convertYAMLToJSON(true); result != true {
		t.Errorf("expected true, got %v", result)
	}
}

func TestValidate_InvalidYAMLBytes(t *testing.T) {
	c := &contract.Contract{} // dummy contract, won't be used
	result := Validate(c, []byte(`{{{invalid yaml`), nil)
	if result.IsValid() {
		t.Error("expected error for invalid YAML bytes")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	if result.Errors[0].Code != "YAML_PARSE_ERROR" {
		t.Errorf("expected YAML_PARSE_ERROR, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructural_InvalidData(t *testing.T) {
	data := map[string]any{
		"pactoVersion": "2.0",
	}
	result := ValidateStructural(data)
	if result.IsValid() {
		t.Error("expected structural validation to fail for incomplete data")
	}
}

func TestYamlToGeneric_NaN(t *testing.T) {
	// .nan in YAML produces math.NaN, which json.Marshal cannot encode
	_, err := yamlToGeneric([]byte("value: .nan"))
	if err == nil {
		t.Error("expected error for NaN value that json.Marshal cannot handle")
	}
}
