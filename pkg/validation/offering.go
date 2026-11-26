package validation

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// tieredOffering reports whether r's chain reaches an accepted offering
// whose configuration schema declares tiers, and returns that schema decoded.
// unverified says why the values stay unchecked: the offering is inferred
// (its parameters are not accepted) or its schema does not read. An
// unknown never reads as tunable.
func tieredOffering(r ResolvedConfigRef) (doc any, ok bool, unverified string) {
	p := r.Offering
	if p == nil {
		return nil, false, ""
	}
	for _, fsys := range []fs.FS{r.OfferingFS, r.ProviderFS} { // the offering, and the contract holding its schema
		if rec, err := contract.ReadInferredRecord(fsys); rec != nil || err != nil {
			return nil, false, "the offering " + p.Service.Name + " is inferred, so its parameters are not accepted"
		}
	}
	data, err := fs.ReadFile(r.ProviderFS, r.SchemaPath)
	if err == nil {
		err = json.Unmarshal(data, &doc)
	}
	if err != nil {
		return nil, false, fmt.Sprintf("cannot read the schema %s of offering %s: %v", r.SchemaPath, p.Service.Name, err)
	}
	return doc, schemax.Tiered(doc), ""
}

// paramFinding is one value outside a tiered offering's parameter space.
type paramFinding struct {
	code string
	path []string // inside the configuration's values
	msg  string   // "<subject> <what>. Next: <step>"
}

// offeringParams judges values against a tiered offering's schema: each
// violation mapped through parameterCode, and the rest, the violations the
// parameter codes do not cover, through legacy with the configuration's
// usual code. subject names a path in messages.
type offeringParams struct {
	r       ResolvedConfigRef
	doc     any
	cfgIdx  int
	subject func(path []string) string
}

// violations is each violation of schema by values as a parameter finding;
// one that maps to no parameter code is passed to legacy.
func (o offeringParams) violations(schema *jsonschema.Schema, values any, legacy func(*jsonschema.ValidationError)) []paramFinding {
	var out []paramFinding
	eachParameter(schema, values, legacy, func(code string, at []string, k jsonschema.ErrorKind) {
		suggest := ""
		switch {
		case code == string(finding.CodeParameterNotDeclared):
			suggest, k = contract.DidYouMean(at[len(at)-1], schemaProperties(o.doc, at[:len(at)-1])), nil
		case !writeOnlyAt(o.doc, at): // a suggestion would hint at a secret value
			suggest = enumSuggestion(k)
		}
		out = append(out, o.finding(code, at, suggest, k))
	})
	return out
}

// eachParameter calls fn with the parameter code, the instance path and the
// error kind of each violation of schema by values that parameterCode
// covers, once per key for an undeclared field; legacy takes the others.
func eachParameter(schema *jsonschema.Schema, values any, legacy func(*jsonschema.ValidationError), fn func(code string, at []string, k jsonschema.ErrorKind)) {
	for _, v := range schemaViolations(schema.Validate(values)) {
		switch code := parameterCode(v); code {
		case "":
			legacy(v)
		case string(finding.CodeParameterNotDeclared):
			for _, key := range v.ErrorKind.(*kind.AdditionalProperties).Properties {
				fn(code, append(slices.Clone(v.InstanceLocation), key), v.ErrorKind)
			}
		default:
			fn(code, v.InstanceLocation, v.ErrorKind)
		}
	}
}

// finding is the paramFinding for code at path.
func (o offeringParams) finding(code string, path []string, suggest string, k jsonschema.ErrorKind) paramFinding {
	p := o.r.Offering.Service
	what := map[string]string{
		string(finding.CodeTierNotTunable):       tierWhat[1:] + "%s %s, so it cannot change per environment",
		string(finding.CodeParameterOutOfRange):  "is outside the parameters of offering %s %s",
		string(finding.CodeParameterNotDeclared): "is not a parameter of offering %s %s",
		string(finding.CodeClassNotOffered):      "names a class offering %s %s does not offer",
	}[code]
	note := ""
	if j := junctionAt(o.doc, path); code == string(finding.CodeTierNotTunable) && j != "" {
		note = "; the tier sits behind a " + j + " Pacto does not follow"
	}
	msg := o.subject(path) + " " + fmt.Sprintf(what, p.Name, p.Version) + note + ". Next: " + nextStep(code, o.r, o.cfgIdx, o.doc, path, suggest, k)
	return paramFinding{code: code, path: path, msg: msg}
}

// tierWhat follows the subject of a TierNotTunable message.
const tierWhat = " is contract-tier in offering "

// TierNotTunableField is the subject a TierNotTunable message names, such as
// "environment prod: db.storageGB", or "" when msg is no such message. It
// leaves out the offering version, which differs between revisions.
func TierNotTunableField(msg string) string {
	field, _, _ := strings.Cut(msg, tierWhat)
	if field == msg {
		return ""
	}
	return field
}

// junctionAt is the first of $ref, oneOf, anyOf and allOf a node on the way
// to path uses, where [schemax.TierAt] stops reading tiers; "" when none does.
func junctionAt(doc any, path []string) string {
	for i := 0; i <= len(path); i++ {
		n := schemaNode(doc, path[:i])
		for _, k := range []string{"$ref", "oneOf", "anyOf", "allOf"} {
			if _, ok := n[k]; ok {
				return k
			}
		}
	}
	return ""
}

// tierFindings is a TierNotTunable finding for each path given sets that the
// offering does not mark tunable (see untunablePaths). reported holds the
// paths already reported with a parameter code, which are skipped with
// everything under them.
func (o offeringParams) tierFindings(given any, reported map[string]bool) []paramFinding {
	var out []paramFinding
	for _, at := range untunablePaths(o.doc, given, reported) {
		out = append(out, o.finding(string(finding.CodeTierNotTunable), at, "", nil))
	}
	return out
}

// untunablePaths is each path given sets that the configuration schema doc
// does not mark tunable (see [schemax.TierAt]): a non-null leaf, a null (it
// removes the value, on a leaf or an object, and with an object every field
// the schema declares under it) and values that are not an object (they
// replace the configuration root). Paths in skip, keyed by paramKey, are
// left out with everything under them.
func untunablePaths(doc, given any, skip map[string]bool) [][]string {
	var out [][]string
	var walk func(at []string, v any, root bool)
	walk = func(at []string, v any, root bool) {
		if skip[paramKey(at)] {
			return
		}
		if m, isMap := v.(map[string]any); isMap {
			for _, k := range slices.Sorted(maps.Keys(m)) {
				walk(append(slices.Clone(at), k), m[k], false)
			}
			return
		}
		if root && v == nil {
			return // a configuration given no values sets nothing
		}
		tier, _ := schemax.TierAt(doc, at)
		switch {
		case tier != schemax.TierTunable:
			out = append(out, at)
		case v == nil:
			out = append(out, untunableBelow(doc, at)...)
		}
	}
	walk(nil, given, true)
	return out
}

// untunableBelow is each field the schema doc declares under the tunable
// path at that is not tunable itself, the nearest one on each branch: a null
// at removes them all.
func untunableBelow(doc any, at []string) [][]string {
	var out [][]string
	for _, k := range schemaProperties(doc, at) {
		p := append(slices.Clone(at), k)
		if tier, _ := schemax.TierAt(doc, p); tier != schemax.TierTunable {
			out = append(out, p)
			continue
		}
		out = append(out, untunableBelow(doc, p)...)
	}
	return out
}

// paramKey is path as a map key.
func paramKey(path []string) string { return strings.Join(path, "\x00") }

// offeringFindings judges the values an environment gives (given) a
// configuration backed by a tiered offering, merged over its defaults:
// each violation of schema through parameterCode, then each path given sets
// that is not tunable (see tierFindings). Values that replace a contract-tier
// root report only that. legacy takes the violations no parameter code
// covers. The defaults alone are never tier-checked: they apply in every
// environment, which is where a contract-tier field is set.
func offeringFindings(o offeringParams, schema *jsonschema.Schema, defaults map[string]any, given any, legacy func(*jsonschema.ValidationError)) []paramFinding {
	if _, isMap := given.(map[string]any); !isMap && given != nil {
		if root := o.tierFindings(given, nil); len(root) > 0 {
			return root
		}
	}
	out := o.violations(schema, EffectiveValues(defaults, given), legacy)
	reported := map[string]bool{}
	for _, f := range out {
		reported[paramKey(f.path)] = true
	}
	return append(out, o.tierFindings(given, reported)...)
}

// parameterCode is the parameter code for a violation of a tiered offering's
// schema, or "" for one the offering's parameters do not cover (a missing
// required value keeps the configuration's usual code).
func parameterCode(v *jsonschema.ValidationError) string {
	switch v.ErrorKind.(type) {
	case *kind.Enum:
		if slices.Equal(v.InstanceLocation, []string{"class"}) { // classes are the root class enum
			return string(finding.CodeClassNotOffered)
		}
		return string(finding.CodeParameterOutOfRange)
	case *kind.AdditionalProperties:
		return string(finding.CodeParameterNotDeclared)
	case *kind.Minimum, *kind.Maximum, *kind.ExclusiveMinimum, *kind.ExclusiveMaximum, *kind.MinLength,
		*kind.MaxLength, *kind.Pattern, *kind.Type, *kind.MultipleOf, *kind.Const, *kind.Format,
		*kind.MinItems, *kind.MaxItems, *kind.UniqueItems, *kind.AdditionalItems, *kind.Contains, *kind.MinContains,
		*kind.MaxContains, *kind.MinProperties, *kind.MaxProperties, *kind.PropertyNames, *kind.DependentRequired,
		*kind.FalseSchema, *kind.Not, *kind.ContentEncoding, *kind.ContentMediaType:
		// Every bound the offering owner declares on a value is theirs.
		return string(finding.CodeParameterOutOfRange)
	}
	return ""
}

// nextStep is the step that fixes a parameter finding. It is built from the
// schema only, never from the value, which may be a secret.
func nextStep(code string, r ResolvedConfigRef, cfgIdx int, doc any, path []string, suggest string, k jsonschema.ErrorKind) string {
	p := r.Offering.Service
	lists := "; pacto fleet get " + p.Name + " lists the parameters"
	switch code {
	case string(finding.CodeTierNotTunable):
		owner := cmp.Or(p.Owner.DisplayString(), "the offering owner")
		return fmt.Sprintf("set it once in configurations[%d].values, or ask %s to mark it %s: %s", cfgIdx, owner, schemax.TierKeyword, schemax.TierTunable)
	case string(finding.CodeParameterNotDeclared):
		return "remove it" + suggest + lists
	case string(finding.CodeClassNotOffered):
		return "use one of " + joinValues(k.(*kind.Enum).Want) + suggest
	}
	return "use a value " + cmp.Or(schemaBound(doc, path), "the schema allows") + suggest + lists
}

// schemaNode is the node of the schema doc at path, following
// properties.<segment>; nil when the schema does not name it.
func schemaNode(doc any, path []string) map[string]any {
	node, _ := doc.(map[string]any)
	for _, seg := range path {
		props, _ := node["properties"].(map[string]any)
		node, _ = props[seg].(map[string]any)
	}
	return node
}

// schemaProperties is the property names the schema doc declares at path.
func schemaProperties(doc any, path []string) []string {
	props, _ := schemaNode(doc, path)["properties"].(map[string]any)
	return slices.Sorted(maps.Keys(props))
}

// schemaBound describes the values the schema doc allows at path, such as
// "of type integer, >= 1, <= 5"; empty when the node states no bound.
func schemaBound(doc any, path []string) string {
	n := schemaNode(doc, path)
	var parts []string
	for _, b := range []struct{ key, text string }{
		{"type", "of type %v"}, {"enum", "in %v"}, {"const", "equal to %v"},
		{"minimum", ">= %v"}, {"exclusiveMinimum", "> %v"}, {"maximum", "<= %v"}, {"exclusiveMaximum", "< %v"},
		{"multipleOf", "a multiple of %v"}, {"minLength", "at least %v characters long"},
		{"maxLength", "at most %v characters long"}, {"pattern", "matching %v"}, {"format", "in format %v"},
		{"minItems", "with at least %v items"}, {"maxItems", "with at most %v items"},
		{"minProperties", "with at least %v keys"}, {"maxProperties", "with at most %v keys"},
	} {
		v, ok := n[b.key]
		if !ok {
			continue
		}
		if list, isList := v.([]any); isList {
			v = joinValues(list)
		}
		parts = append(parts, fmt.Sprintf(b.text, v))
	}
	return strings.Join(parts, ", ")
}

// writeOnlyAt reports whether the schema doc marks the node at path, or an
// ancestor, writeOnly: its value is a secret no message may carry.
func writeOnlyAt(doc any, path []string) bool {
	for i := 0; i <= len(path); i++ {
		if schemaNode(doc, path[:i])["writeOnly"] == true {
			return true
		}
	}
	return false
}

// joinValues is vals as "a, b".
func joinValues(vals []any) string {
	s := make([]string, len(vals))
	for i, v := range vals {
		s[i] = fmt.Sprint(v)
	}
	return strings.Join(s, ", ")
}
