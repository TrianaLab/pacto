package validation

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema/pacto-v1.0.schema.json
var schemaV10Bytes []byte

//go:embed schema/pacto-v1.1.schema.json
var schemaV11Bytes []byte

// SchemaBytes returns the raw embedded JSON Schema bytes for the latest
// supported version (1.1). This is used by the doc package to extract field
// descriptions; v1.1 is a superset of v1.0, so it carries every description.
func SchemaBytes() []byte { return schemaV11Bytes }

// supportedSchemaVersions lists the supported pactoVersion values in ascending
// order. It drives both schema compilation and the unsupported-version message.
var supportedSchemaVersions = []string{"1.0", "1.1"}

// compiledSchemas maps each supported pactoVersion to its compiled JSON Schema.
var compiledSchemas map[string]*jsonschema.Schema

func init() {
	compiledSchemas = map[string]*jsonschema.Schema{
		"1.0": mustCompileSchema(schemaV10Bytes),
		"1.1": mustCompileSchema(schemaV11Bytes),
	}
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

// schemaResourceURL is the internal resource key used when compiling a schema.
// It is independent of the schema's $id and only needs to be consistent between
// AddResource and Compile for a single compilation.
const schemaResourceURL = "pacto.schema.json"

func compileSchema(data []byte) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()

	var schemaDoc interface{}
	if err := json.Unmarshal(data, &schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	if err := addResourceFn(c, schemaResourceURL, schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to add schema resource: %w", err)
	}

	schema, err := c.Compile(schemaResourceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	return schema, nil
}

// Function variable for testing.
var schemaValidateFn = func(schema *jsonschema.Schema, data interface{}) error {
	return schema.Validate(data)
}

// pactoVersionOf extracts the declared pactoVersion from a generic contract doc.
// It returns an empty string when the doc is not an object or omits the field.
func pactoVersionOf(data interface{}) string {
	m, ok := data.(map[string]interface{})
	if !ok {
		return ""
	}
	v, _ := m["pactoVersion"].(string)
	return v
}

// ValidateStructural performs Layer 1 validation using JSON Schema.
// It takes the generic (JSON-compatible) contract data, selects the schema that
// matches the declared pactoVersion, and validates against it. An unrecognized
// or missing pactoVersion is a hard error (fail closed) rather than silently
// validating against an arbitrary schema.
func ValidateStructural(data interface{}) ValidationResult {
	var result ValidationResult

	version := pactoVersionOf(data)
	schema, ok := compiledSchemas[version]
	if !ok {
		result.AddError(
			"pactoVersion",
			"UNSUPPORTED_PACTO_VERSION",
			fmt.Sprintf("unsupported pactoVersion %q; supported versions: %s", version, strings.Join(supportedSchemaVersions, ", ")),
		)
		return result
	}

	err := schemaValidateFn(schema, data)
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
