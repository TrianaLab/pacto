package validation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// EnvironmentStage checks the values each environment declares against the
// schema of every configuration they name (pactoVersion 2.3). An environment's
// values for a configuration are merged over that configuration's default
// values, and the result must satisfy its schema; a required configuration an
// environment gives no values for must be satisfied by its defaults alone.
//
// Values for a configuration the contract does not declare are an error. A
// configuration with no schema in the bundle (a ref) leaves its values
// unchecked: ENVIRONMENT_VALUES_UNVERIFIED, a warning, never a pass. The stage
// passes only when every value set was checked and none failed, and is skipped
// with a reason naming what it could not verify otherwise.
//
// Errors in a values file carry that file in File, and the line and column of
// the failing value when it is in the file.
func EnvironmentStage(c *contract.Contract, bundleFS fs.FS) Stage {
	if len(c.Environments) == 0 {
		return SkippedStages("no environments declared", "environments")[0]
	}
	var r ValidationResult
	var unverified []string
	for i, env := range c.Environments {
		unverified = append(unverified, checkEnvironment(c, i, env, bundleFS, &r)...)
	}
	st := RanStage("environments", r)
	if st.Status == StagePass && len(unverified) > 0 {
		st.Status, st.Reason = StageSkip, "could not verify: "+strings.Join(unverified, ", ")
	}
	return st
}

// checkEnvironment checks one environment's values file into r and returns
// each environment/configuration pair it could not verify.
func checkEnvironment(c *contract.Contract, i int, env contract.Environment, bundleFS fs.FS, r *ValidationResult) []string {
	path := fmt.Sprintf("environments[%d].values", i)
	if bundleFS == nil {
		r.AddWarning(path, "ENVIRONMENT_VALUES_UNVERIFIED", fmt.Sprintf("environment %s: no bundle files to read %s from", env.Name, env.Values))
		return []string{env.Name}
	}
	fail := func(code, at string, raw []byte, msg string) {
		e := contract.ValidationError{Path: path, Code: code, Message: "environment " + env.Name + ": " + msg, File: env.Values}
		e.Line, e.Column = contract.Locate(raw, at)
		r.Errors = append(r.Errors, e)
	}
	raw, err := fs.ReadFile(bundleFS, env.Values)
	var values map[string]any
	if err == nil {
		values, err = contract.ParseEnvironmentValues(raw)
	}
	if err != nil {
		fail("ENVIRONMENT_VALUES_INVALID", "", nil, fmt.Sprintf("cannot read %s: %v", env.Values, err))
		return nil
	}

	declared := make([]string, len(c.Configurations))
	for j, cfg := range c.Configurations {
		declared[j] = cfg.Name
	}
	for _, name := range slices.Sorted(maps.Keys(values)) {
		if !slices.Contains(declared, name) {
			fail("ENVIRONMENT_UNKNOWN_CONFIGURATION", name, raw,
				fmt.Sprintf("values for configuration %q, which the contract does not declare%s", name, contract.DidYouMean(name, declared)))
		}
	}

	var unverified []string
	for _, cfg := range c.Configurations {
		given, ok := values[cfg.Name]
		if !ok && !cfg.Required {
			continue
		}
		schema, err := loadConfigSchema(cfg, bundleFS)
		if err != nil {
			r.AddWarning(path, "ENVIRONMENT_VALUES_UNVERIFIED", fmt.Sprintf("environment %s: cannot check the values for configuration %s: %v", env.Name, cfg.Name, err))
			unverified = append(unverified, env.Name+"/"+cfg.Name)
			continue
		}
		code := "ENVIRONMENT_VALUES_INVALID"
		if !ok {
			code = "ENVIRONMENT_VALUES_MISSING"
		}
		var violations ValidationResult
		addSchemaError(&violations, schema.Validate(effectiveValues(cfg.Values, given)))
		for _, v := range violations.Errors {
			at := strings.TrimSuffix(cfg.Name+"."+v.Path, ".")
			_, msg, _ := strings.Cut(v.Message, "': ")
			if !ok {
				msg = "required configuration " + cfg.Name + " has no values here and its defaults fail its schema: " + msg
			}
			fail(code, at, raw, fmt.Sprintf("configuration %s at /%s: %s", cfg.Name, strings.ReplaceAll(at, ".", "/"), msg))
		}
	}
	return unverified
}

// loadConfigSchema compiles a configuration's schema from the bundle. A
// configuration that names its schema by ref has none in the bundle.
func loadConfigSchema(cfg contract.Configuration, bundleFS fs.FS) (*jsonschema.Schema, error) {
	if cfg.Schema == "" {
		return nil, fmt.Errorf("its schema is in the contract at %s, and Pacto checks values only against a schema in this bundle", cfg.Ref)
	}
	data, err := fs.ReadFile(bundleFS, cfg.Schema)
	if err != nil {
		return nil, err
	}
	return compileConfigSchema(data)
}

// effectiveValues is a configuration's default values with an environment's
// values merged over them. Values that are not an object replace the
// defaults, so the schema judges them as written.
func effectiveValues(defaults map[string]any, given any) any {
	b, _ := json.Marshal(defaults) // defaults decoded from YAML that validated against the schema
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{}
	}
	if given == nil {
		return out
	}
	m, ok := given.(map[string]any)
	if !ok {
		return given
	}
	contract.DeepMerge(out, m)
	return out
}
