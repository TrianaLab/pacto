package validation

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

//go:embed schema/pacto-v2.0.schema.json
var schemaV20Bytes []byte

//go:embed schema/pacto-v2.1.schema.json
var schemaV21Bytes []byte

//go:embed schema/pacto-v2.2.schema.json
var schemaV22Bytes []byte

//go:embed schema/pacto-v2.3.schema.json
var schemaV23Bytes []byte

// SchemaBytes returns the raw embedded JSON Schema bytes of the newest version
// (2.3, a superset of 2.2, 2.1 and 2.0). The doc package extracts field descriptions
// from it.
func SchemaBytes() []byte { return schemaV23Bytes }

// schemaBytes is the embedded JSON Schema of each pactoVersion. The versions
// supported are contract.SupportedVersions; init panics on one without a
// schema here.
var schemaBytes = map[string][]byte{"2.0": schemaV20Bytes, "2.1": schemaV21Bytes, "2.2": schemaV22Bytes, "2.3": schemaV23Bytes}

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

	var mcp []int
	if version == "2.0" || version == "2.1" {
		data = needs22(&result, data)
	}
	if version == "2.0" {
		data, mcp = needs21(&result, data)
	}
	addSchemaError(&result, schema.Validate(data))
	if len(mcp) > 0 {
		result.Errors = slices.DeleteFunc(result.Errors, func(e contract.ValidationError) bool {
			if e.Code != "SCHEMA_VIOLATION" {
				return false
			}
			for _, i := range mcp {
				at := fmt.Sprintf("interfaces.%d", i)
				if e.Path == at || strings.HasPrefix(e.Path, at+".") {
					return true
				}
			}
			return false
		})
	}
	return result
}

// needs22 records a NEEDS_PACTO_2_2 error for each field only pactoVersion 2.2
// has (an interface lifecycle, compatibilityEpoch or guarantees and a
// dependency requires) in a 2.0 or 2.1 contract, in place of the older schema's
// generic "not allowed", and returns data without them for that schema to
// check the rest. Every entry stays in place so its other errors keep their
// index.
func needs22(result *ValidationResult, data any) any {
	const fix = `; set pactoVersion: "2.2", which keeps every older field`
	m, _ := data.(map[string]any) // an older pactoVersion was read from it
	out := maps.Clone(m)
	for _, s := range []struct{ list, key, what string }{
		{"interfaces", "lifecycle", "an interface lifecycle needs"},
		{"interfaces", "compatibilityEpoch", "compatibilityEpoch needs"},
		{"interfaces", "guarantees", "interface guarantees need"},
		{"dependencies", "requires", "requires on a dependency needs"},
	} {
		items, ok := out[s.list].([]any) // out keeps what an earlier key removed
		if !ok {
			continue
		}
		kept := slices.Clone(items)
		for i, it := range items {
			im, _ := it.(map[string]any)
			if _, ok := im[s.key]; ok {
				result.AddError(fmt.Sprintf("%s.%d.%s", s.list, i, s.key), string(finding.CodeNeedsPacto22), s.what+` pactoVersion "2.2"`+fix)
				im = maps.Clone(im)
				delete(im, s.key)
				kept[i] = im
			}
		}
		out[s.list] = kept
	}
	return out
}

// needs21 records a NEEDS_PACTO_2_1 error for each part of data only
// pactoVersion 2.1 has (publish, checks, an external dependency and an mcp
// interface), in place of the 2.0 schema's generic "not allowed". It returns
// data without publish, checks and the dependencies' external and version for
// the rest of the 2.0 schema to check, and the indexes of the mcp interfaces,
// whose schema errors the caller drops. The interfaces stay in place so every
// other interface's errors keep its own index.
func needs21(result *ValidationResult, data any) (any, []int) {
	const fix = `; set pactoVersion: "2.1", which keeps every 2.0 field`
	m, _ := data.(map[string]any) // a 2.0 pactoVersion was read from it
	out := maps.Clone(m)
	for _, key := range []string{"publish", "checks"} {
		if _, ok := m[key]; ok {
			result.AddError(key, string(finding.CodeNeedsPacto21), key+` needs pactoVersion "2.1"`+fix)
			delete(out, key)
		}
	}
	if deps, ok := m["dependencies"].([]any); ok {
		kept := make([]any, len(deps))
		for i, d := range deps {
			dm, _ := d.(map[string]any)
			kept[i] = d
			for _, key := range []string{"external", "version"} {
				if _, ok := dm[key]; ok {
					result.AddError(fmt.Sprintf("dependencies.%d.%s", i, key), string(finding.CodeNeedsPacto21), `an external dependency needs pactoVersion "2.1"`+fix)
					dm = maps.Clone(dm)
					delete(dm, key)
					kept[i] = dm
				}
			}
		}
		out["dependencies"] = kept
	}
	ifaces, _ := m["interfaces"].([]any)
	var mcp []int
	for i, it := range ifaces {
		if im, _ := it.(map[string]any); im["type"] == "mcp" {
			result.AddError(fmt.Sprintf("interfaces.%d.type", i), string(finding.CodeNeedsPacto21), `an mcp interface needs pactoVersion "2.1"`+fix)
			mcp = append(mcp, i)
		}
	}
	return out, mcp
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
		result.AddError(path, "SCHEMA_VIOLATION", ve.Error()+enumSuggestion(ve.ErrorKind))
		return
	}
	for _, cause := range ve.Causes {
		collectErrors(result, cause)
	}
}

// enumSuggestion names the allowed value closest to a mistyped one.
func enumSuggestion(k jsonschema.ErrorKind) string {
	e, ok := k.(*kind.Enum)
	if !ok {
		return ""
	}
	got, _ := e.Got.(string)
	var want []string
	for _, w := range e.Want {
		if s, ok := w.(string); ok {
			want = append(want, s)
		}
	}
	return contract.DidYouMean(got, want)
}

func instancePath(ve *jsonschema.ValidationError) string {
	parts := make([]string, 0, len(ve.InstanceLocation))
	for _, p := range ve.InstanceLocation {
		parts = append(parts, fmt.Sprintf("%v", p))
	}
	return strings.Join(parts, ".")
}
