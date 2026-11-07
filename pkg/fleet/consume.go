package fleet

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/schemax"
	"github.com/trianalab/pacto/v4/pkg/validation"
	"gopkg.in/yaml.v3"
)

// consumeSnippet builds how to consume provider from newest, its newest
// readable revision (the offering page's current revision), as Entries and as
// the YAML rendered from them:
//   - a dependency when it declares a workload or an interface, with the range
//     majorRange gives for its version, or a line asking for one when none does;
//   - a configuration reference, pinned to newest's version, per required
//     configuration, with a selector when the provider declares more than one
//     and a values skeleton of the schema's required properties;
//   - a policy reference when it declares a policy, or ships the fixed-path
//     policy/schema.json, and neither applies.
//
// A deprecated newest adds a comment saying so. It is nil when no OCI
// repository is known for provider (see [consumeRepo]) or there is nothing to
// consume.
func consumeSnippet(snap *FleetSnapshot, provider *ServiceRecord, newest *ContractRevision) *ConsumeSnippet {
	if newest == nil {
		return nil
	}
	repo := consumeRepo(snap, provider, newest)
	if repo == "" {
		return nil
	}
	c := newest.Contract
	sn := &ConsumeSnippet{
		Ref:           contract.OCIScheme + repo,
		Dependency:    c.Workload != "" || len(c.Interfaces) > 0,
		Configuration: slices.ContainsFunc(c.Configurations, func(cfg contract.Configuration) bool { return cfg.Required }),
	}
	sn.Policy = !sn.Dependency && !sn.Configuration && (len(c.Policies) > 0 || newest.fixedPolicy)
	if !sn.Dependency && !sn.Configuration && !sn.Policy {
		return nil
	}
	var b strings.Builder
	writeDeprecation(&b, provider.Name, newest.Version, c.Service.Lifecycle)
	if sn.Dependency {
		sn.Compatibility = majorRange(newest.Version)
		sn.Entries.Dependency = &contract.Dependency{Name: provider.Name, Ref: sn.Ref, Required: true, Compatibility: sn.Compatibility}
		writeDependency(&b, sn.Entries.Dependency)
	}
	if sn.Configuration {
		var skeletons []string
		sn.Entries.Configurations, skeletons = configurationRefs(snap, provider, newest, sn.Ref)
		writeConfigurationRefs(&b, sn.Entries.Configurations, skeletons)
	}
	if sn.Policy {
		sn.Entries.Policy = &contract.Policy{Name: provider.Name, Ref: sn.Ref}
		fmt.Fprintf(&b, "policies:\n  - name: %s\n    ref: %s\n", sn.Entries.Policy.Name, sn.Entries.Policy.Ref)
	}
	sn.YAML = b.String()
	return sn
}

// consumeRepo is the OCI repository, without scheme or tag, consumers reference
// provider by: the one newest was read from, else the one newest publishes its
// bundle to, else the first, in order, that a consumer's resolved reference to
// provider names (registry references only). A revision read from a local
// directory has only the last two. It is "" when none is an OCI repository.
func consumeRepo(snap *FleetSnapshot, provider *ServiceRecord, newest *ContractRevision) string {
	var declared []string
	for _, rel := range snap.Relationships {
		if rel.Resolved && rel.ToService == provider.Key && strings.HasPrefix(rel.RequestedRef, contract.OCIScheme) {
			declared = append(declared, rel.RequestedRef)
		}
	}
	slices.Sort(declared)
	refs := []string{cmp.Or(newest.ResolvedRef, newest.RequestedRef)}
	if p := newest.Contract.Publish; p != nil && strings.HasPrefix(p.Bundle, contract.OCIScheme) {
		refs = append(refs, p.Bundle)
	}
	for _, ref := range append(refs, declared...) {
		if repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref)); repo != "" && !strings.Contains(repo, "://") {
			return repo
		}
	}
	return ""
}

// writeDeprecation writes a comment saying version of name is deprecated,
// nothing when it is not.
func writeDeprecation(b *strings.Builder, name, version string, l *contract.InterfaceLifecycle) {
	if l == nil || l.Deprecated == "" {
		return
	}
	fmt.Fprintf(b, "# %s %s is deprecated since %s", name, version, l.Deprecated)
	if l.Successor != "" {
		fmt.Fprintf(b, "; successor %s", l.Successor)
	}
	b.WriteString("\n")
}

// writeDependency writes the dependencies block of a consume snippet.
func writeDependency(b *strings.Builder, d *contract.Dependency) {
	fmt.Fprintf(b, "dependencies:\n  - name: %s\n    ref: %s\n    required: true\n", d.Name, d.Ref)
	if d.Compatibility != "" {
		fmt.Fprintf(b, "    compatibility: %q\n", d.Compatibility)
		return
	}
	// A dependency with a ref needs a range; say so rather than omit it.
	b.WriteString("    compatibility: \"\" # set a semver range: the newest version is not semver\n")
}

// configurationRefs is one configuration entry per required configuration of
// rev, pinned to rev's version, named after the provider (and the
// configuration when there are several entries), with a selector when rev
// declares more than one configuration, plus the values block YAML of each.
func configurationRefs(snap *FleetSnapshot, provider *ServiceRecord, rev *ContractRevision, ref string) ([]ConsumeConfiguration, []string) {
	if rev.Version != "" {
		// ponytail: a version with build metadata ("+") is no valid tag; pacto lock pins it by digest.
		ref += ":" + rev.Version
	}
	var required []contract.Configuration
	for _, cfg := range rev.Contract.Configurations {
		if cfg.Required {
			required = append(required, cfg)
		}
	}
	entries := make([]ConsumeConfiguration, len(required))
	skeletons := make([]string, len(required))
	for i, cfg := range required {
		e := &entries[i]
		e.Name, e.Ref, e.Required = provider.Name, ref, true
		if len(required) > 1 {
			e.Name += "-" + cfg.Name
		}
		if len(rev.Contract.Configurations) > 1 {
			e.ProviderConfiguration = cfg.Name
		}
		from, path := rev, cfg.Schema
		if cfg.Ref != "" {
			from, path = refSchemaFile(snap, rev, cfg.Name)
		}
		if from != nil {
			if data, reason := readBundleFile(from, path, maxSchemaBytes); reason == "" {
				e.Values, e.MissingValues, skeletons[i] = valuesSkeleton(data, path)
			}
		}
	}
	return entries, skeletons
}

// writeConfigurationRefs writes the configurations block of a consume snippet
// from its entries and the values block YAML of each.
func writeConfigurationRefs(b *strings.Builder, entries []ConsumeConfiguration, skeletons []string) {
	b.WriteString("# values and configuration need pactoVersion \"2.4\"; run pacto lock to pin the reference\nconfigurations:\n")
	for i, e := range entries {
		fmt.Fprintf(b, "  - name: %s\n    ref: %s\n", e.Name, e.Ref)
		if e.ProviderConfiguration != "" {
			fmt.Fprintf(b, "    configuration: %s\n", e.ProviderConfiguration)
		}
		b.WriteString("    required: true\n")
		b.WriteString(skeletons[i])
	}
}

// valuesSkeleton is the values of the schema's required top-level properties
// and their values block, indented for a configuration entry. Each property
// takes the first candidate its schema accepts: its default, its first enum
// value, its minimum (numbers) or the empty value of its type; a line comments
// the type, the allowed values and the default. A property no candidate fits
// is left out of values, listed in missing and written as a comment asking for
// a value. It is all empty when the schema cannot be parsed or requires
// nothing.
func valuesSkeleton(schema []byte, path string) (values map[string]any, missing []string, block string) {
	doc, err := schemax.Parse(schema, path)
	required, _ := doc["required"].([]any)
	if err != nil || len(required) == 0 {
		return nil, nil, ""
	}
	props, _ := doc["properties"].(map[string]any)
	values = map[string]any{}
	var lines strings.Builder
	for _, k := range required {
		key := fmt.Sprint(k)
		prop, _ := props[key].(map[string]any)
		if prop == nil {
			prop = map[string]any{}
		}
		typ := schemaType(prop["type"])
		enum, _ := prop["enum"].([]any)
		def, hasDef := prop["default"]
		value, ok := skeletonValue(prop, typ, enum, def, hasDef)
		if !ok {
			missing = append(missing, key)
			fmt.Fprintf(&lines, "      # %s: %s, set a value\n", yamlKey(key), strings.TrimSpace("required "+typ))
			continue
		}
		values[key] = value
		comment := strings.TrimSpace("required " + typ)
		if len(enum) > 0 {
			opts := make([]string, len(enum))
			for i, e := range enum {
				opts[i] = plainValue(e)
			}
			comment += ", one of " + strings.Join(opts, "|")
		}
		if hasDef {
			comment += ", default " + plainValue(def)
		}
		fmt.Fprintf(&lines, "      %s: %s # %s\n", yamlKey(key), jsonValue(value), comment)
	}
	head := "    values:\n"
	if len(values) == 0 {
		// Every line is a comment: an empty block would read as null.
		head = "    values: {}\n"
	}
	return values, missing, head + lines.String()
}

// skeletonValue is the first candidate value prop accepts: def, the first enum
// value, the minimum of a number, then the empty value of typ.
func skeletonValue(prop map[string]any, typ string, enum []any, def any, hasDef bool) (any, bool) {
	var candidates []any
	if hasDef {
		candidates = append(candidates, def)
	}
	if len(enum) > 0 {
		candidates = append(candidates, enum[0])
	}
	if minimum, ok := prop["minimum"]; ok && (typ == "integer" || typ == "number") {
		candidates = append(candidates, minimum)
	}
	zero := map[string]any{"string": "", "integer": 0, "number": 0, "boolean": false, "object": map[string]any{}, "array": []any{}}[typ]
	if zero == nil {
		zero = ""
	}
	candidates = append(candidates, zero)
	for _, v := range candidates {
		if validation.ValueAccepted(prop, v) {
			return v, true
		}
	}
	return nil, false
}

// schemaType is a JSON Schema type keyword: the type, or the first of a list.
func schemaType(t any) string {
	if list, ok := t.([]any); ok && len(list) > 0 {
		t = list[0]
	}
	s, _ := t.(string)
	return s
}

// jsonValue renders v as JSON, which YAML reads as the same flow value.
func jsonValue(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// plainValue renders v for a comment: a string as is, anything else as JSON.
func plainValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jsonValue(v)
}

// yamlKey renders key as a YAML mapping key, quoted when YAML would read it as
// something other than that string.
func yamlKey(key string) string {
	data, _ := yaml.Marshal(key)
	return strings.TrimSuffix(string(data), "\n")
}

// majorRange is the widest caret range that admits version: ^MAJOR.0.0, or
// ^0.MINOR.0 for a 0.x version, or ^version for a prerelease, which a range
// admits only when it names it. It is empty when none does, so a version that is
// not semver never gets a wrong range.
func majorRange(version string) string {
	major, rest, _ := strings.Cut(version, ".")
	minor, _, _ := strings.Cut(rest, ".")
	candidates := []string{"^" + major + ".0.0"}
	if major == "0" {
		candidates = append(candidates, "^0."+minor+".0")
	}
	for _, r := range append(candidates, "^"+version) {
		if CheckRange(r, version) == RangeSatisfied {
			return r
		}
	}
	return ""
}
