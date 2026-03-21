package validation

import (
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
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

func TestConvertYAMLToJSON_MapInterfaceNested(t *testing.T) {
	input := map[interface{}]interface{}{
		"nested": map[interface{}]interface{}{
			"key": "value",
		},
	}
	result := convertYAMLToJSON(input)
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}
	nested, ok := m["nested"].(map[string]interface{})
	if !ok {
		t.Fatal("expected nested map[string]interface{}")
	}
	if nested["key"] != "value" {
		t.Errorf("expected value, got %v", nested["key"])
	}
}

func TestConvertYAMLToJSON_Slice(t *testing.T) {
	input := []interface{}{"a", "b", map[interface{}]interface{}{"key": "val"}}
	result := convertYAMLToJSON(input)
	s, ok := result.([]interface{})
	if !ok {
		t.Fatal("expected []interface{}")
	}
	if len(s) != 3 {
		t.Errorf("expected 3 items, got %d", len(s))
	}
	m, ok := s[2].(map[string]interface{})
	if !ok {
		t.Fatal("expected nested map in slice")
	}
	if m["key"] != "val" {
		t.Errorf("expected val, got %v", m["key"])
	}
}

func TestConvertYAMLToJSON_MapStringNested(t *testing.T) {
	input := map[string]interface{}{
		"a": []interface{}{1, 2},
	}
	result := convertYAMLToJSON(input)
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{}")
	}
	s, ok := m["a"].([]interface{})
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
	// Missing required fields
	data := map[string]interface{}{
		"pactoVersion": "1.0",
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
