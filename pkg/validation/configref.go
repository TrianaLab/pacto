package validation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ResolvedConfigRef is what a configuration that names its schema by ref
// resolved to. Provider is the contract the ref names and Selected the
// configuration of it the entry takes values for; Schema is that
// configuration's compiled schema, read from ProviderFS at SchemaPath (from the
// last contract of the chain when the selected configuration is itself a ref).
// Err says why the ref yields no schema; Schema is nil then.
type ResolvedConfigRef struct {
	Name                          string
	Ref                           string
	Selected                      string
	ProviderName, ProviderVersion string
	Provider                      *contract.Contract
	Schema                        *jsonschema.Schema
	SchemaPath                    string
	ProviderFS                    fs.FS
	Err                           error
}

// ConfigRefs holds each resolved configuration ref, keyed by the consumer's
// configuration name.
type ConfigRefs map[string]ResolvedConfigRef

// ConfigRefOptions says how configuration refs resolve. Strict callers
// (validate, pacto_check, push) fail closed with CONFIG_REF_UNRESOLVED on a ref
// something depends on; the others report CONFIG_REF_UNVERIFIED. Now dates the
// provider's lifecycle warnings.
type ConfigRefOptions struct {
	Resolver BundleResolver
	Strict   bool
	Now      time.Time
}

// configRefProblem is why a configuration ref yields no schema. An empty code
// is a ref that could not be resolved, whose code depends on the mode.
type configRefProblem struct {
	code, field, msg string
	err              bool
}

// ResolveConfigRefs resolves each configuration of c that names its schema by
// ref to the referenced contract and the schema of the configuration it
// selects, following a selected configuration that is itself a ref. bundleFS
// holds c's environment files, which say whether a value depends on the ref.
func ResolveConfigRefs(ctx context.Context, c *contract.Contract, bundleFS fs.FS, opts ConfigRefOptions) (ConfigRefs, ValidationResult) {
	refs := ConfigRefs{}
	var res ValidationResult
	resolve, base := resolvePort(opts.Resolver)
	given := EnvironmentValueNames(c, bundleFS)
	for i, cfg := range c.Configurations {
		if cfg.Ref == "" {
			continue
		}
		r, p := resolveConfigRef(ctx, c.PactoVersion, cfg, resolve, base)
		refs[cfg.Name] = r
		if p == nil {
			continue
		}
		path := fmt.Sprintf("configurations[%d].%s", i, p.field)
		switch {
		case p.code != "" && p.err:
			res.AddError(path, p.code, p.msg)
		case p.code != "":
			res.AddWarning(path, p.code, p.msg)
		case opts.Strict && refNeeded(c, cfg, given):
			res.AddError(path, "CONFIG_REF_UNRESOLVED", p.msg)
		default:
			res.AddWarning(path, "CONFIG_REF_UNVERIFIED", p.msg)
		}
	}
	return refs, res
}

// MaxConfigRefHops bounds how many refs a configuration ref chain follows, so a
// registry that answers every hop with a new ref cannot hold a caller forever.
const MaxConfigRefHops = 8

// resolveConfigRef follows cfg's ref, declared by a contract of version
// version, hop by hop until a selected configuration has a schema. path is the
// chain of links from the root, so a link that reappears in it is a cycle.
func resolveConfigRef(ctx context.Context, version string, cfg contract.Configuration, resolve resolveFrom, base string) (ResolvedConfigRef, *configRefProblem) {
	r := ResolvedConfigRef{Name: cfg.Name, Ref: cfg.Ref}
	fail := func(code, field string, hard bool, format string, a ...any) (ResolvedConfigRef, *configRefProblem) {
		msg := fmt.Sprintf("configuration %s: "+format, append([]any{cfg.Name}, a...)...)
		r.Err = errors.New(msg)
		return r, &configRefProblem{code: code, field: field, msg: msg, err: hard}
	}
	if resolve == nil {
		return fail("", "ref", false, "ref %q cannot be resolved (no resolver configured)", cfg.Ref)
	}
	entry := cfg
	var path []declaredRef
	for hop := 0; hop < MaxConfigRefHops; hop++ {
		link := declaredRef{Base: base, Ref: entry.Ref}
		if slices.Contains(path, link) {
			return fail("CONFIG_REF_CYCLE", "ref", true, "cycle detected resolving ref %q (chain: %v)", entry.Ref, refChain(path, entry.Ref))
		}
		path = append(path, link)
		bundle, next, err := resolve(ctx, base, entry.Ref)
		switch {
		case err != nil:
			return fail("", "ref", false, "failed to resolve ref %q: %v", entry.Ref, err)
		case bundle == nil || bundle.FS == nil || bundle.Contract == nil:
			return fail("", "ref", false, "ref %q resolved to an empty bundle", entry.Ref)
		}
		p := bundle.Contract
		if hop == 0 {
			r.Provider, r.ProviderName, r.ProviderVersion = p, p.Service.Name, p.Service.Version
		}
		sel, code, why := SelectConfiguration(entry, p)
		if code != "" {
			field := "ref"
			if hop == 0 && entry.ProviderConfiguration != "" {
				field = "configuration"
			}
			return fail(code, field, !olderThan(version, "2.6"), "%s", why)
		}
		if hop == 0 {
			r.Selected = sel.Name
		}
		if sel.Ref != "" {
			entry, version, base = sel, p.PactoVersion, next
			continue
		}
		schema, err := readConfigSchema(bundle.FS, sel.Schema)
		if err != nil {
			return fail("", "ref", false, "%s@%s configuration %s: %v", p.Service.Name, p.Service.Version, sel.Name, err)
		}
		r.Schema, r.SchemaPath, r.ProviderFS = schema, sel.Schema, bundle.FS
		return r, nil
	}
	return fail("", "ref", false, "ref %q chains through more than %d configuration refs", cfg.Ref, MaxConfigRefHops)
}

// readConfigSchema reads and compiles the configuration schema at path.
func readConfigSchema(fsys fs.FS, path string) (*jsonschema.Schema, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("cannot read schema %q: %w", path, err)
	}
	schema, err := compileConfigSchema(data)
	if err != nil {
		return nil, fmt.Errorf("schema %q failed to compile: %w", path, err)
	}
	return schema, nil
}

// SelectConfiguration picks the configuration of provider that entry takes
// values for: the one its selector names, else the only one. It reports a
// finding code and message when there is none to pick.
func SelectConfiguration(entry contract.Configuration, provider *contract.Contract) (contract.Configuration, string, string) {
	who := provider.Service.Name + "@" + provider.Service.Version
	want := entry.ProviderConfiguration
	names := make([]string, len(provider.Configurations))
	for i, pc := range provider.Configurations {
		names[i] = pc.Name
		if want != "" && pc.Name == want {
			return pc, "", ""
		}
	}
	switch {
	case want != "":
		return contract.Configuration{}, "CONFIG_REF_UNKNOWN_CONFIGURATION", fmt.Sprintf("%s declares no configuration %q%s", who, want, contract.DidYouMean(want, names))
	case len(names) == 1:
		return provider.Configurations[0], "", ""
	case len(names) == 0:
		return contract.Configuration{}, "CONFIG_REF_UNKNOWN_CONFIGURATION", who + " declares no configuration"
	}
	return contract.Configuration{}, "CONFIG_REF_AMBIGUOUS", fmt.Sprintf("%s declares %d configurations (%s); set configuration: <name> to pick one", who, len(names), strings.Join(names, ", "))
}

// refNeeded reports whether a value depends on resolving cfg's ref: in 2.6 its
// selector and inline values do, as do environment values for it and a
// dependency on the same repository with a compatibility to check against.
func refNeeded(c *contract.Contract, cfg contract.Configuration, given map[string]bool) bool {
	if !olderThan(c.PactoVersion, "2.6") || given[cfg.Name] {
		return true
	}
	repo := refRepository(cfg.Ref)
	return slices.ContainsFunc(c.Dependencies, func(d contract.Dependency) bool {
		return d.Compatibility != "" && refRepository(d.Ref) == repo
	})
}

// refRepository is the repository a ref names, without scheme, tag or digest.
func refRepository(ref string) string {
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	return repo
}

// EnvironmentValueNames is each configuration some environment of c gives
// values for. A file that does not read gives none; the environments stage
// reports it.
func EnvironmentValueNames(c *contract.Contract, bundleFS fs.FS) map[string]bool {
	out := map[string]bool{}
	if bundleFS == nil {
		return out
	}
	for _, env := range c.Environments {
		raw, err := fs.ReadFile(bundleFS, env.File)
		if err != nil {
			continue
		}
		values, _ := contract.ParseEnvironmentValues(raw)
		for name := range values {
			out[name] = true
		}
	}
	return out
}

// configRefsNotEnforced is local-only resolution: it resolves no ref and warns
// CONFIG_REF_NOT_ENFORCED for each ref configuration with values to check. With
// no bundle at all it says nothing, like [ResolvePoliciesFromBundle].
func configRefsNotEnforced(c *contract.Contract, bundleFS fs.FS) (ConfigRefs, ValidationResult) {
	var res ValidationResult
	if bundleFS == nil {
		return nil, res
	}
	given := EnvironmentValueNames(c, bundleFS)
	for i, cfg := range c.Configurations {
		if cfg.Ref != "" && (len(cfg.Values) > 0 || given[cfg.Name]) {
			res.AddWarning(fmt.Sprintf("configurations[%d].ref", i), "CONFIG_REF_NOT_ENFORCED",
				fmt.Sprintf("values for configuration %s are not checked by local validation: its schema comes from %s; run validate with a resolver to check them", cfg.Name, cfg.Ref))
		}
	}
	return nil, res
}

// ConfigRefStage is the configuration-refs stage: res, the findings of
// resolving refs, plus each ref configuration's inline values checked against
// the resolved schema, a resolved revision outside the compatibility of a
// dependency on the same repository, and the provider's lifecycle warnings. It
// is skipped when c references no configuration, and when it would pass with a
// ref left unresolved.
func ConfigRefStage(c *contract.Contract, refs ConfigRefs, res ValidationResult, opts ConfigRefOptions) Stage {
	var declared, unverified []string
	for i, cfg := range c.Configurations {
		if cfg.Ref == "" {
			continue
		}
		declared = append(declared, cfg.Name)
		at := fmt.Sprintf("configurations[%d]", i)
		r := refs[cfg.Name]
		if r.Schema == nil {
			unverified = append(unverified, cfg.Name)
		} else {
			checkRefValues(&res, at+".values", cfg, r.Schema)
		}
		if r.Provider != nil {
			checkRefRange(&res, c, at+".ref", cfg, r.Provider)
			res.Warnings = append(res.Warnings, DependencyServiceWarnings(at, r.Provider, opts.Now)...)
		}
	}
	if len(declared) == 0 {
		return SkippedStages("no configuration references", "configuration-refs")[0]
	}
	st := RanStage("configuration-refs", res)
	if st.Status == StagePass && len(unverified) > 0 {
		st.Status, st.Reason = StageSkip, "could not verify: "+strings.Join(unverified, ", ")
	}
	return st
}

// checkRefValues reports each violation of schema by cfg's inline values.
func checkRefValues(res *ValidationResult, path string, cfg contract.Configuration, schema *jsonschema.Schema) {
	if len(cfg.Values) == 0 {
		return
	}
	for _, v := range schemaViolations(schema.Validate(EffectiveValues(cfg.Values, nil))) {
		_, msg, _ := strings.Cut(v.Error()+enumSuggestion(v.ErrorKind), "': ")
		res.AddError(path, "CONFIG_VALUES_VALIDATION_FAILED", fmt.Sprintf("configuration %s at %s: %s", cfg.Name, "/"+strings.TrimPrefix(jsonPointer(v.InstanceLocation), "/"), msg))
	}
}

// checkRefRange reports a dependency of c on cfg's repository whose
// compatibility the resolved provider's version is outside of. A range or
// version that does not read is unknown and reports nothing.
func checkRefRange(res *ValidationResult, c *contract.Contract, path string, cfg contract.Configuration, provider *contract.Contract) {
	repo := refRepository(cfg.Ref)
	for _, d := range c.Dependencies {
		if d.Compatibility == "" || refRepository(d.Ref) != repo {
			continue
		}
		rng, err := contract.ParseRange(d.Compatibility)
		if err != nil {
			continue
		}
		if ok, err := rng.Check(provider.Service.Version); err == nil && !ok {
			res.AddError(path, "CONFIG_REF_VERSION_OUTSIDE_RANGE", fmt.Sprintf("configuration %s: ref %q resolves %s@%s, outside the compatibility %s of dependency %s",
				cfg.Name, cfg.Ref, provider.Service.Name, provider.Service.Version, d.Compatibility, d.Name))
		}
	}
}

// DependencyServiceWarnings returns the warning for a deprecated provider, the
// contract a consumer's dependency or configuration ref at path resolved to,
// naming its sunset and successor. From the sunset date on the code is
// DEPENDENCY_SERVICE_SUNSET instead of DEPENDENCY_SERVICE_DEPRECATED. Both stay
// warnings, for the reasons [DeprecatedInterfaceWarnings] gives.
func DependencyServiceWarnings(path string, provider *contract.Contract, now time.Time) []contract.ValidationWarning {
	l := provider.Service.Lifecycle
	if l == nil {
		return nil
	}
	code, msg := "DEPENDENCY_SERVICE_DEPRECATED", fmt.Sprintf("%s@%s is deprecated since %s", provider.Service.Name, provider.Service.Version, l.Deprecated)
	switch {
	case l.SunsetReached(now):
		code, msg = "DEPENDENCY_SERVICE_SUNSET", msg+fmt.Sprintf(" and reached its sunset %s: it may be retired in any release", l.Sunset)
	case l.Sunset != "":
		msg += fmt.Sprintf(", sunset %s", l.Sunset)
	}
	if l.Successor != "" {
		msg += fmt.Sprintf("; successor %s", l.Successor)
	}
	return []contract.ValidationWarning{{Path: path, Code: code, Message: msg}}
}
