package fleet

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/schemax"
	"gopkg.in/yaml.v3"
)

// consumeSnippet renders how to consume provider from newest, its newest
// readable revision (the offering page's current revision):
//   - a dependency when it declares a workload or an interface, with the range
//     majorRange gives for its version, or a line asking for one when none does;
//   - a configuration reference, pinned to newest's version, per required
//     configuration, with a selector when the provider declares more than one
//     and a values skeleton of the schema's required properties;
//   - a policy reference when it declares a policy, or ships the fixed-path
//     policy/schema.json, and neither applies.
//
// A deprecated newest adds a comment saying so. It is nil when newest has no
// OCI reference or nothing to consume.
func consumeSnippet(snap *FleetSnapshot, provider *ServiceRecord, newest *ContractRevision) *ConsumeSnippet {
	if newest == nil {
		return nil
	}
	ref := cmp.Or(newest.ResolvedRef, newest.RequestedRef)
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	if repo == "" || strings.Contains(repo, "://") {
		return nil
	}
	c := newest.Contract
	sn := &ConsumeSnippet{
		Ref:           contract.OCIScheme + repo,
		Dependency:    c.Workload != "" || len(c.Interfaces) > 0,
		Configuration: slices.ContainsFunc(c.Configurations, func(cfg contract.Configuration) bool { return cfg.Required }),
	}
	sn.Policy = !sn.Dependency && !sn.Configuration && (len(c.Policies) > 0 || newest.fixedPolicy)
	var b strings.Builder
	writeDeprecation(&b, provider.Name, newest.Version, c.Service.Lifecycle)
	if sn.Dependency {
		sn.Compatibility = majorRange(newest.Version)
		writeDependency(&b, provider.Name, sn.Ref, sn.Compatibility)
	}
	if sn.Configuration {
		writeConfigurationRefs(&b, snap, provider, newest, sn.Ref)
	}
	if sn.Policy {
		fmt.Fprintf(&b, "policies:\n  - name: %s\n    ref: %s\n", provider.Name, sn.Ref)
	}
	if !sn.Dependency && !sn.Configuration && !sn.Policy {
		return nil
	}
	sn.YAML = b.String()
	return sn
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
func writeDependency(b *strings.Builder, name, ref, compatibility string) {
	fmt.Fprintf(b, "dependencies:\n  - name: %s\n    ref: %s\n    required: true\n", name, ref)
	if compatibility != "" {
		fmt.Fprintf(b, "    compatibility: %q\n", compatibility)
		return
	}
	// A dependency with a ref needs a range; say so rather than omit it.
	b.WriteString("    compatibility: \"\" # set a semver range: the newest version is not semver\n")
}

// writeConfigurationRefs writes the configurations block of a consume snippet:
// one entry per required configuration of rev, pinned to rev's version, named
// after the provider (and the configuration when there are several entries),
// with a selector when rev declares more than one configuration.
func writeConfigurationRefs(b *strings.Builder, snap *FleetSnapshot, provider *ServiceRecord, rev *ContractRevision, ref string) {
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
	b.WriteString("# values and configuration need pactoVersion \"2.4\"; run pacto lock to pin the reference\nconfigurations:\n")
	for _, cfg := range required {
		name := provider.Name
		if len(required) > 1 {
			name += "-" + cfg.Name
		}
		fmt.Fprintf(b, "  - name: %s\n    ref: %s\n", name, ref)
		if len(rev.Contract.Configurations) > 1 {
			fmt.Fprintf(b, "    configuration: %s\n", cfg.Name)
		}
		b.WriteString("    required: true\n")
		from, path := rev, cfg.Schema
		if cfg.Ref != "" {
			from, path = refSchemaFile(snap, rev, cfg.Name)
		}
		if from != nil {
			if data, reason := readBundleFile(from, path, maxSchemaBytes); reason == "" {
				b.WriteString(valuesSkeleton(data, path))
			}
		}
	}
}

// valuesSkeleton is a values block, indented for a configuration entry, with
// one line per required top-level property of the schema: its default, else
// its first enum value, else the empty value of its type, and a comment naming
// the type, the allowed values and the default. It is empty when the schema
// cannot be parsed or requires nothing.
func valuesSkeleton(schema []byte, path string) string {
	doc, err := schemax.Parse(schema, path)
	required, _ := doc["required"].([]any)
	if err != nil || len(required) == 0 {
		return ""
	}
	props, _ := doc["properties"].(map[string]any)
	var b strings.Builder
	b.WriteString("    values:\n")
	for _, k := range required {
		key := fmt.Sprint(k)
		prop, _ := props[key].(map[string]any)
		typ := schemaType(prop["type"])
		enum, _ := prop["enum"].([]any)
		def, hasDef := prop["default"]
		value := map[string]any{"string": "", "integer": 0, "number": 0, "boolean": false, "object": map[string]any{}, "array": []any{}}[typ]
		switch {
		case hasDef:
			value = def
		case len(enum) > 0:
			value = enum[0]
		case value == nil:
			value = ""
		}
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
		fmt.Fprintf(&b, "      %s: %s # %s\n", yamlKey(key), jsonValue(value), comment)
	}
	return b.String()
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
