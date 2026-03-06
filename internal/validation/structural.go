package validation

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema/pacto-v1.0.schema.json
var schemaBytes []byte

// SchemaBytes returns the raw embedded JSON Schema bytes.
// This is used by the doc package to extract field descriptions.
func SchemaBytes() []byte { return schemaBytes }

var compiledSchema *jsonschema.Schema

func init() {
	compiledSchema = mustCompileSchema(schemaBytes)
}

func mustCompileSchema(data []byte) *jsonschema.Schema {
	s, err := compileSchema(data)
	if err != nil {
		panic(err)
	}
	return s
}

// Function variable for testing.
var addResourceFn = func(c *jsonschema.Compiler, url string, doc any) error {
	return c.AddResource(url, doc)
}

func compileSchema(data []byte) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()

	var schemaDoc interface{}
	if err := json.Unmarshal(data, &schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	if err := addResourceFn(c, "pacto-v1.0.schema.json", schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to add schema resource: %w", err)
	}

	schema, err := c.Compile("pacto-v1.0.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	return schema, nil
}

// Function variable for testing.
var schemaValidateFn = func(data interface{}) error {
	return compiledSchema.Validate(data)
}

// ValidateStructural performs Layer 1 validation using JSON Schema.
// It takes the raw YAML bytes (converted to a generic interface{}) and validates
// against the embedded pacto v1.0 JSON Schema.
func ValidateStructural(data interface{}) ValidationResult {
	var result ValidationResult

	err := schemaValidateFn(data)
	if err == nil {
		return result
	}

	validationErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		result.AddError("", "SCHEMA_ERROR", fmt.Sprintf("schema validation failed: %v", err))
		return result
	}

	collectErrors(&result, validationErr)
	return result
}

func collectErrors(result *ValidationResult, ve *jsonschema.ValidationError) {
	if len(ve.Causes) == 0 {
		path := instancePath(ve)
		result.AddError(path, "SCHEMA_VIOLATION", ve.Error())
		return
	}
	for _, cause := range ve.Causes {
		collectErrors(result, cause)
	}
}

func instancePath(ve *jsonschema.ValidationError) string {
	parts := make([]string, 0, len(ve.InstanceLocation))
	for _, p := range ve.InstanceLocation {
		parts = append(parts, fmt.Sprintf("%v", p))
	}
	return strings.Join(parts, ".")
}
