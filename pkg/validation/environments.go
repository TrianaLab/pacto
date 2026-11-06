package validation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strconv"
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
// configuration that names its schema by ref is checked against the schema it
// resolved to in refs; one whose ref did not resolve, or with no refs at all,
// leaves its values unchecked and reports ENVIRONMENT_VALUES_UNVERIFIED, a
// warning. Before pactoVersion 2.6 a ref configuration has no defaults, so only
// the values an environment gives for it are checked. The stage passes only
// when every value set was checked and none failed, and is skipped with a
// reason naming what it could not verify otherwise.
//
// Errors in a values file carry that file in File, and the line and column of
// the failing value when it is in the file.
//
// Deprecated: it resolves no configuration ref, so every value for one is
// unverified; use [EnvironmentStageWith] with the refs [ResolveConfigRefs]
// resolved.
func EnvironmentStage(c *contract.Contract, bundleFS fs.FS) Stage {
	return EnvironmentStageWith(c, bundleFS, nil)
}

// EnvironmentStageWith is [EnvironmentStage] with the configuration refs that
// were resolved.
func EnvironmentStageWith(c *contract.Contract, bundleFS fs.FS, refs ConfigRefs) Stage {
	if len(c.Environments) == 0 {
		return SkippedStages("no environments declared", "environments")[0]
	}
	var r ValidationResult
	var unverified []string
	for i, env := range c.Environments {
		unverified = append(unverified, checkEnvironment(c, i, env, bundleFS, refs, &r)...)
	}
	st := RanStage("environments", r)
	if st.Status == StagePass && len(unverified) > 0 {
		st.Status, st.Reason = StageSkip, "could not verify: "+strings.Join(unverified, ", ")
	}
	return st
}

// checkEnvironment checks one environment's values file into r and returns
// each environment/configuration pair it could not verify.
func checkEnvironment(c *contract.Contract, i int, env contract.Environment, bundleFS fs.FS, refs ConfigRefs, r *ValidationResult) []string {
	path := fmt.Sprintf("environments[%d].file", i)
	if bundleFS == nil {
		r.AddWarning(path, "ENVIRONMENT_VALUES_UNVERIFIED", fmt.Sprintf("environment %s: no bundle files to read %s from", env.Name, env.File))
		return []string{env.Name}
	}
	fail := func(code string, at []string, raw []byte, msg string) {
		e := contract.ValidationError{Path: path, Code: code, Message: "environment " + env.Name + ": " + msg, File: env.File}
		e.Line, e.Column = contract.LocateSegments(raw, at)
		r.Errors = append(r.Errors, e)
	}
	raw, err := fs.ReadFile(bundleFS, env.File)
	var values map[string]any
	if err == nil {
		values, err = contract.ParseEnvironmentValues(raw)
	}
	if err != nil {
		e := contract.ValidationError{Path: path, Code: "ENVIRONMENT_VALUES_INVALID", File: env.File,
			Message: fmt.Sprintf("environment %s: cannot read %s: %v", env.Name, env.File, err)}
		if m := lineRe.FindStringSubmatch(err.Error()); m != nil {
			e.Line, _ = strconv.Atoi(m[1])
		}
		r.Errors = append(r.Errors, e)
		return nil
	}

	declared := make([]string, len(c.Configurations))
	for j, cfg := range c.Configurations {
		declared[j] = cfg.Name
	}
	for _, name := range slices.Sorted(maps.Keys(values)) {
		if !slices.Contains(declared, name) {
			fail("ENVIRONMENT_UNKNOWN_CONFIGURATION", []string{name}, raw,
				fmt.Sprintf("values for configuration %q, which the contract does not declare%s", name, contract.DidYouMean(name, declared)))
		}
	}

	var unverified []string
	for _, cfg := range c.Configurations {
		given, ok := values[cfg.Name]
		if !valuesChecked(c, cfg, ok) {
			continue
		}
		schema, err := loadConfigSchema(cfg, bundleFS, refs)
		if err != nil {
			r.AddWarning(path, "ENVIRONMENT_VALUES_UNVERIFIED", fmt.Sprintf("environment %s: cannot check the values for configuration %s: %v", env.Name, cfg.Name, err))
			unverified = append(unverified, env.Name+"/"+cfg.Name)
			continue
		}
		code := "ENVIRONMENT_VALUES_INVALID"
		if !ok {
			code = "ENVIRONMENT_VALUES_MISSING"
		}
		for _, v := range schemaViolations(schema.Validate(EffectiveValues(cfg.Values, given))) {
			at := append([]string{cfg.Name}, v.InstanceLocation...)
			_, msg, _ := strings.Cut(v.Error()+enumSuggestion(v.ErrorKind), "': ")
			if !ok {
				msg = "required configuration " + cfg.Name + " has no values here and its defaults fail its schema: " + msg
			}
			fail(code, at, raw, fmt.Sprintf("configuration %s at %s: %s", cfg.Name, jsonPointer(at), msg))
		}
	}
	return unverified
}

// valuesChecked reports whether an environment that gives values for cfg, or
// leaves them out, has them checked: given values always are, and a required
// configuration's defaults must pass alone. Before pactoVersion 2.6 a ref
// configuration has no defaults, so only given values are checked.
func valuesChecked(c *contract.Contract, cfg contract.Configuration, given bool) bool {
	return given || cfg.Required && (cfg.Ref == "" || !olderThan(c.PactoVersion, "2.6"))
}

// loadConfigSchema compiles a configuration's schema from the bundle, or
// takes the schema a configuration that names it by ref resolved to in refs.
func loadConfigSchema(cfg contract.Configuration, bundleFS fs.FS, refs ConfigRefs) (*jsonschema.Schema, error) {
	if cfg.Schema == "" {
		r, ok := refs[cfg.Name]
		switch {
		case r.Schema != nil:
			return r.Schema, nil
		case ok:
			return nil, fmt.Errorf("its schema comes from the contract %s, which could not be resolved: %v", cfg.Ref, r.Err)
		}
		return nil, fmt.Errorf("its schema comes from the contract %s, which this validation does not resolve", cfg.Ref)
	}
	data, err := fs.ReadFile(bundleFS, cfg.Schema)
	if err != nil {
		return nil, err
	}
	return compileConfigSchema(data)
}

// lineRe finds the line a YAML decode error names.
var lineRe = regexp.MustCompile(`line (\d+):`)

// schemaViolations is each leaf violation in err, the result of validating
// JSON-decoded values, so every error it holds is a *jsonschema.ValidationError.
func schemaViolations(err error) []*jsonschema.ValidationError {
	ve, _ := err.(*jsonschema.ValidationError)
	if ve == nil {
		return nil
	}
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}
	var out []*jsonschema.ValidationError
	for _, cause := range ve.Causes {
		out = append(out, schemaViolations(cause)...)
	}
	return out
}

// jsonPointer renders segments as a JSON Pointer (RFC 6901).
func jsonPointer(segments []string) string {
	var b strings.Builder
	for _, s := range segments {
		b.WriteString("/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(s))
	}
	return b.String()
}

// EffectiveValues is a configuration's default values with an environment's
// values merged over them (see [MergeValues]): the values a consumer requests
// in that environment. Values that are not an object replace the defaults, so
// the schema judges them as written. defaults is not modified.
func EffectiveValues(defaults map[string]any, given any) any {
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
	MergeValues(out, m)
	return out
}

// MergeValues merges an environment's values src over dst. Objects merge key
// by key and src wins. A null in src removes its key from dst, at any depth.
func MergeValues(dst, src map[string]any) {
	for k, v := range src {
		switch v := v.(type) {
		case nil:
			delete(dst, k)
		case map[string]any:
			d, ok := dst[k].(map[string]any)
			if !ok {
				d = map[string]any{}
				dst[k] = d
			}
			MergeValues(d, v)
		default:
			dst[k] = v
		}
	}
}
