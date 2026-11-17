package validation

import (
	"cmp"
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

//go:embed schema/pacto-v2.4.schema.json
var schemaV24Bytes []byte

//go:embed schema/pacto-v2.5.schema.json
var schemaV25Bytes []byte

// SchemaBytes returns the raw embedded JSON Schema bytes of the newest version
// (contract.Current(), a superset of every older one). The doc package
// extracts field descriptions from it.
func SchemaBytes() []byte { return schemaBytes[contract.Current()] }

// schemaBytes is the embedded JSON Schema of each pactoVersion. The versions
// supported are contract.SupportedVersions; init panics on one without a
// schema here.
var schemaBytes = map[string][]byte{"2.0": schemaV20Bytes, "2.1": schemaV21Bytes, "2.2": schemaV22Bytes, "2.3": schemaV23Bytes, "2.4": schemaV24Bytes, "2.5": schemaV25Bytes}

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
// matches the declared pactoVersion, and validates against it. An absent
// pactoVersion is contract.Current() and an empty document is an empty object;
// an unrecognized pactoVersion is a hard error (fail closed) rather than
// silently validating against an arbitrary schema.
func ValidateStructural(data any) ValidationResult {
	var result ValidationResult

	if data == nil {
		data = map[string]any{}
	}
	version := cmp.Or(pactoVersionOf(data), contract.Current())
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
	if olderThan(version, "2.5") {
		data = needs25(&result, data)
	}
	if olderThan(version, "2.4") {
		data = needs24(&result, data)
	}
	if olderThan(version, "2.3") {
		data = needs23(&result, data)
	}
	if olderThan(version, "2.2") {
		data = needs22(&result, data)
	}
	if olderThan(version, "2.1") {
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

// RefValuesAllowed reports whether a contract at version lets a configuration
// that takes its schema by ref carry values, and checks that configuration as
// needed: from pactoVersion 2.4. An unknown version is refused.
func RefValuesAllowed(version string) bool { return !olderThan(version, "2.4") }

// PublishNeedsRaise reports whether a contract at version must raise
// pactoVersion before it may declare publish: a known version before 2.1.
func PublishNeedsRaise(version string) bool {
	return contract.Known(version) && olderThan(version, "2.1")
}

// olderThan reports whether the supported pactoVersion version comes before
// than, so each later version keeps every field an earlier one added.
func olderThan(version, than string) bool {
	return slices.Index(contract.SupportedVersions, version) < slices.Index(contract.SupportedVersions, than)
}

// needsServiceFields records a NEEDS_PACTO_2_4 error for each of the service
// fields only pactoVersion 2.4 has, in an older contract in place of the older
// schema's generic "not allowed", and returns data with a service without
// them for that schema to check the rest.
func needsServiceFields(result *ValidationResult, data any, fields ...string) any {
	m, _ := data.(map[string]any) // an older pactoVersion was read from it
	svc, _ := m["service"].(map[string]any)
	var found []string
	for _, f := range fields {
		if _, ok := svc[f]; ok {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return data
	}
	svc = maps.Clone(svc)
	for _, f := range found {
		result.AddError("service."+f, string(finding.CodeNeedsPacto24), "service."+f+` needs pactoVersion "2.4"; set pactoVersion: "2.4", which keeps every older field`)
		delete(svc, f)
	}
	out := maps.Clone(m)
	out["service"] = svc
	return out
}

// needs24 records a NEEDS_PACTO_2_4 error for each field only pactoVersion 2.4
// has (service.domain, service.offering, service.summary, service.lifecycle,
// service.template and service.origin, values or a configuration selector
// beside a configuration ref, and an environment's lifetime, expires and owner)
// in an older contract, in place of the older schema's generic error, and
// returns data without them for that schema to check the rest. Every
// configuration and environment stays in place so its other errors keep their
// index.
func needs24(result *ValidationResult, data any) any {
	data = needsServiceFields(result, data, "domain", "offering", "summary", "lifecycle", "template", "origin")
	m, _ := data.(map[string]any) // an older pactoVersion was read from it
	m = needsItems(result, m, "2.4", finding.CodeNeedsPacto24, "configurations", func(im map[string]any) []needsKey {
		_, ref := im["ref"]
		return []needsKey{{"values", "values beside a ref need ", ref, nil}, {"configuration", "a configuration selector needs ", true, nil}}
	})
	return needsItems(result, m, "2.4", finding.CodeNeedsPacto24, "environments", func(map[string]any) []needsKey {
		return []needsKey{{"lifetime", "lifetime on an environment needs ", true, nil}, {"expires", "expires on an environment needs ", true, nil}, {"owner", "owner on an environment needs ", true, nil}}
	})
}

// needs25 records a NEEDS_PACTO_2_5 error for each part only pactoVersion 2.5
// has (compatibility: unknown on a dependency, a port on an interface and the
// breaks and delivery sections) in an older contract, in place of the older schema's generic
// error, and returns data without it for that schema to check the rest. Every
// dependency and interface stays in place so its other errors keep their index.
func needs25(result *ValidationResult, data any) any {
	m, _ := data.(map[string]any) // an older pactoVersion was read from it
	if _, ok := m["breaks"]; ok {
		result.AddError("breaks", string(finding.CodeNeedsPacto25), `breaks need pactoVersion "2.5"; set pactoVersion: "2.5", which keeps every older field`)
		m = maps.Clone(m)
		delete(m, "breaks")
	}
	if _, ok := m["delivery"]; ok {
		result.AddError("delivery", string(finding.CodeNeedsPacto25), `delivery needs pactoVersion "2.5"; set pactoVersion: "2.5", which keeps every older field`)
		m = maps.Clone(m)
		delete(m, "delivery")
	}
	m = needsItems(result, m, "2.5", finding.CodeNeedsPacto25, "interfaces", func(map[string]any) []needsKey {
		return []needsKey{{"port", "port on an interface needs ", true, nil}}
	})
	return needsItems(result, m, "2.5", finding.CodeNeedsPacto25, "dependencies", func(im map[string]any) []needsKey {
		// 2.0 requires compatibility, and 2.1 to 2.4 require it beside a ref: a
		// range every older schema accepts stands in, so no generic error joins.
		return []needsKey{{"compatibility", "compatibility: unknown needs ", im["compatibility"] == contract.CompatibilityUnknown, "*"}}
	})
}

// needsKey is one key an item of an array may only hold from a later
// pactoVersion on, the start of its message, whether it counts on that item at
// all and, when the older schema requires the key, the value that stands in
// for it (nil removes the key).
type needsKey struct {
	key, what   string
	applies     bool
	placeholder any
}

// needsItems records code for each key keys names on an item of the array
// m[section] that only pactoVersion version allows, and returns m with those
// keys removed, every item kept at its index. Without the array m is returned
// as is.
func needsItems(result *ValidationResult, m map[string]any, version string, code finding.Code, section string, keys func(map[string]any) []needsKey) map[string]any {
	fix := fmt.Sprintf("pactoVersion %q; set pactoVersion: %q, which keeps every older field", version, version)
	items, ok := m[section].([]any)
	if !ok {
		return m
	}
	kept := slices.Clone(items)
	for i, it := range items {
		im, _ := it.(map[string]any)
		for _, k := range keys(im) {
			if _, has := im[k.key]; !has || !k.applies {
				continue
			}
			result.AddError(fmt.Sprintf("%s.%d.%s", section, i, k.key), string(code), k.what+fix)
			im = maps.Clone(im)
			if k.placeholder != nil {
				im[k.key] = k.placeholder
			} else {
				delete(im, k.key)
			}
			kept[i] = im
		}
	}
	out := maps.Clone(m)
	out[section] = kept
	return out
}

// needs23 records a NEEDS_PACTO_2_3 error for environments, the section only
// pactoVersion 2.3 has, in an older contract in place of the older schema's
// generic "not allowed", and returns data without it for that schema to check
// the rest.
func needs23(result *ValidationResult, data any) any {
	m, _ := data.(map[string]any) // an older pactoVersion was read from it
	if _, ok := m["environments"]; !ok {
		return data
	}
	result.AddError("environments", string(finding.CodeNeedsPacto23), `environments need pactoVersion "2.3"; set pactoVersion: "2.3", which keeps every older field`)
	out := maps.Clone(m)
	delete(out, "environments")
	return out
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
