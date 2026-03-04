package validation

import (
	"fmt"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCompileSchema_InvalidJSON(t *testing.T) {
	_, err := compileSchema([]byte("{invalid json!"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCompileSchema_InvalidSchema(t *testing.T) {
	_, err := compileSchema([]byte(`{"type": 12345}`))
	if err == nil {
		t.Error("expected error for invalid schema")
	}
}

func TestMustCompileSchema_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid schema data")
		}
	}()
	mustCompileSchema([]byte("bad"))
}

func TestValidateStructural_NonValidationError(t *testing.T) {
	old := schemaValidateFn
	schemaValidateFn = func(interface{}) error { return fmt.Errorf("internal error") }
	defer func() { schemaValidateFn = old }()

	result := ValidateStructural(nil)
	if result.IsValid() {
		t.Error("expected invalid result")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	if result.Errors[0].Code != "SCHEMA_ERROR" {
		t.Errorf("expected SCHEMA_ERROR, got %s", result.Errors[0].Code)
	}
}

func TestCompileSchema_AddResourceError(t *testing.T) {
	old := addResourceFn
	addResourceFn = func(_ *jsonschema.Compiler, _ string, _ any) error {
		return fmt.Errorf("injected AddResource error")
	}
	defer func() { addResourceFn = old }()

	_, err := compileSchema([]byte(`{"type": "object"}`))
	if err == nil {
		t.Fatal("expected error from AddResource")
	}
	if got := err.Error(); got != "failed to add schema resource: injected AddResource error" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestYamlToGeneric_UnmarshalError(t *testing.T) {
	old := jsonUnmarshalFn
	jsonUnmarshalFn = func([]byte, interface{}) error {
		return fmt.Errorf("injected unmarshal error")
	}
	defer func() { jsonUnmarshalFn = old }()

	_, err := yamlToGeneric([]byte(`key: value`))
	if err == nil {
		t.Fatal("expected error from json.Unmarshal")
	}
	if got := err.Error(); got != "injected unmarshal error" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestConvertYAMLToJSON_NonStringKey(t *testing.T) {
	// Simulate a map[interface{}]interface{} with a non-string key,
	// which can occur in YAML when keys are integers or booleans.
	input := map[interface{}]interface{}{
		42:     "int-key-value",
		"name": "string-key-value",
	}

	result := convertYAMLToJSON(input)
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	if m["42"] != "int-key-value" {
		t.Errorf("expected key '42' with value 'int-key-value', got %v", m["42"])
	}
	if m["name"] != "string-key-value" {
		t.Errorf("expected key 'name' with value 'string-key-value', got %v", m["name"])
	}
}
