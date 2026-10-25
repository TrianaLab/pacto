package validation

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

//go:embed schema/pacto-v2.0.schema.json
var schemaV20Bytes []byte

//go:embed schema/pacto-v2.1.schema.json
var schemaV21Bytes []byte

// SchemaBytes returns the raw embedded JSON Schema bytes of the newest version
// (2.1, a superset of 2.0 that adds publish). The doc package extracts field
// descriptions from it.
func SchemaBytes() []byte { return schemaV21Bytes }

// schemaBytes is the embedded JSON Schema of each pactoVersion. The versions
// supported are contract.SupportedVersions; init panics on one without a
// schema here.
var schemaBytes = map[string][]byte{"2.0": schemaV20Bytes, "2.1": schemaV21Bytes}

// compiledSchemas maps each supported pactoVersion to its compiled JSON Schema.
var compiledSchemas = map[string]*jsonschema.Schema{}

func init() {
	for _, v := range contract.SupportedVersions {
		compiledSchemas[v] = mustCompileSchema(schemaBytes[v])
	}
}

func mustCompileSchema(data []byte) *jsonschema.Schema {
	s, err := compileSchema(data, schemaResourceURL)
	if err != nil {
		panic(err)
	}
	return s
}

// schemaResourceURL is the internal resource key used when compiling a schema.
// It is independent of the schema's $id and only needs to be consistent between
// AddResource and Compile for a single compilation.
const schemaResourceURL = "pacto.schema.json"

// compileSchema compiles data registered under the resource key url.
func compileSchema(data []byte, url string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()

	var schemaDoc any
	if err := json.Unmarshal(data, &schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal schema: %w", err)
	}

	if err := c.AddResource(url, schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to add schema resource: %w", err)
	}

	schema, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	return schema, nil
}

// pactoVersionOf extracts the declared pactoVersion from a generic contract doc.
// It returns an empty string when the doc is not an object or omits the field.
func pactoVersionOf(data any) string {
	m, ok := data.(map[string]any)
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
func ValidateStructural(data any) ValidationResult {
	var result ValidationResult

	version := pactoVersionOf(data)
	schema, ok := compiledSchemas[version]
	if !ok {
		result.AddError(
			"pactoVersion",
			"UNSUPPORTED_PACTO_VERSION",
			fmt.Sprintf("unsupported pactoVersion %q; supported versions: %s", version, strings.Join(contract.SupportedVersions, ", ")),
		)
		return result
	}

	addSchemaError(&result, schema.Validate(data))
	return result
}

// addSchemaError records a schema validation outcome: nothing for nil, every
// violation of a *jsonschema.ValidationError, and a SCHEMA_ERROR for anything
// else, so an unexpected failure never passes as valid.
func addSchemaError(result *ValidationResult, err error) {
	if err == nil {
		return
	}
	validationErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		result.AddError("", "SCHEMA_ERROR", fmt.Sprintf("schema validation failed: %v", err))
		return
	}
	collectErrors(result, validationErr)
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
