package validation

import (
	"cmp"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// codeOfferingSchemaOpen is the declare refusal for an offering whose
// configuration schema is not a closed parameter space. It names the same
// refusal the declaration engine gives; no validate finding carries it.
const codeOfferingSchemaOpen finding.Code = "OfferingSchemaOpen"

// OfferingValueRefusal is one value an offering's closed configuration
// schema refuses. Field is relative to the offering: values.<key> for a
// value set once, environments[<env>].values.<key> for one environment,
// with nested keys dotted. Closest is the nearest value the schema accepts
// (a clamped bound, the first allowed value or the nearest declared name),
// nil when there is none to suggest; Allowed lists an enum's values. Neither
// ever carries the refused value.
type OfferingValueRefusal struct {
	Code    finding.Code
	Field   string
	Message string
	Closest any
	Allowed []any
}

// CheckOfferingValues judges the values a consumer sets on an offering
// against its configuration schema: values once for every environment,
// perEnv per environment name. It returns every refusal, sorted by field:
// a field the schema does not declare (ParameterNotDeclared), a value
// outside its bounds, enum, type or pattern (ParameterOutOfRange), a class
// the offering does not offer (ClassNotOffered), a per-environment value on
// a field that is not x-pacto-tier: tunable (TierNotTunable; no tier reads
// contract) and any literal for a writeOnly field, one under a writeOnly
// object or one whose name reads sensitive (ParameterSensitive). A schema
// that is not closed refuses every value with one OfferingSchemaOpen
// refusal. nil means every value is inside the parameter space.
func CheckOfferingValues(schema, values map[string]any, perEnv map[string]map[string]any) []OfferingValueRefusal {
	compiled, open := closedSchema(schema)
	if open != "" {
		return []OfferingValueRefusal{{Code: codeOfferingSchemaOpen, Field: "values",
			Message: "the offering's configuration schema is " + open + ": its owner closes it before anyone consumes it"}}
	}
	c := valueCheck{doc: schema, schema: compiled}
	c.check("values", values, false)
	for _, env := range slices.Sorted(maps.Keys(perEnv)) {
		c.check("environments["+env+"].values", perEnv[env], true)
	}
	slices.SortStableFunc(c.out, func(a, b OfferingValueRefusal) int { return strings.Compare(a.Field, b.Field) })
	return c.out
}

// closedSchema compiles schema, or says why it is no closed parameter space:
// open at some paths, or not a schema that compiles.
func closedSchema(schema map[string]any) (*jsonschema.Schema, string) {
	if paths := schemax.OpenPaths(schema); len(paths) > 0 {
		return nil, "open at " + strings.Join(paths, ", ")
	}
	data, _ := json.Marshal(schema) // a decoded JSON document always marshals
	compiled, err := compileConfigSchema(data)
	if err != nil {
		return nil, "unreadable (" + err.Error() + ")"
	}
	return compiled, ""
}

// valueCheck collects the refusals of one offering's values.
type valueCheck struct {
	doc    map[string]any
	schema *jsonschema.Schema
	out    []OfferingValueRefusal
}

// check judges one set of values, under prefix; perEnv adds the tier rule.
func (c *valueCheck) check(prefix string, set map[string]any, perEnv bool) {
	if len(set) == 0 {
		return
	}
	given := EffectiveValues(set, nil) // a JSON copy, the types the validator reads
	refused := c.sensitive(prefix, set)
	eachParameter(c.schema, given, func(v *jsonschema.ValidationError) {
		// ponytail: a missing required value is no value this check refuses,
		// the contract's own validation names it; every other keyword
		// parameterCode leaves out (format, items, not) still refuses.
		if _, missing := v.ErrorKind.(*kind.Required); !missing {
			c.refuse(prefix, refused, finding.CodeParameterOutOfRange, v.InstanceLocation, v.ErrorKind)
		}
	}, func(code string, at []string, k jsonschema.ErrorKind) {
		c.refuse(prefix, refused, finding.Code(code), at, k)
	})
	if perEnv {
		for _, at := range untunablePaths(c.doc, given, refused) {
			c.add(refused, at, OfferingValueRefusal{Code: finding.CodeTierNotTunable, Field: fieldAt(prefix, at),
				Message: fieldAt(prefix, at) + " is contract-tier in this offering, so it cannot change per environment: set it once in values, or ask the offering owner to mark it " + schemax.TierKeyword + ": " + schemax.TierTunable})
		}
	}
}

// sensitive refuses every sensitive literal leaf of set and returns the
// paths it refused, keyed by paramKey.
func (c *valueCheck) sensitive(prefix string, set map[string]any) map[string]bool {
	refused := map[string]bool{}
	var walk func(at []string, v any)
	walk = func(at []string, v any) {
		if m, isMap := v.(map[string]any); isMap {
			for _, k := range slices.Sorted(maps.Keys(m)) {
				walk(append(slices.Clone(at), k), m[k])
			}
			return
		}
		if v == nil || !writeOnlyAt(c.doc, at) && !schemax.SensitiveKey(at[len(at)-1]) {
			return // a null removes a value; it sets no literal
		}
		refused[paramKey(at)] = true
		c.out = append(c.out, OfferingValueRefusal{Code: finding.CodeParameterSensitive, Field: fieldAt(prefix, at),
			Message: fieldAt(prefix, at) + " is sensitive: set it through a Secret reference, never a literal"})
	}
	walk(nil, set)
	return refused
}

// refuse adds the refusal for a parameter violation at path.
func (c *valueCheck) refuse(prefix string, refused map[string]bool, code finding.Code, at []string, k jsonschema.ErrorKind) {
	r := OfferingValueRefusal{Code: code, Field: fieldAt(prefix, at)}
	switch code {
	case finding.CodeParameterNotDeclared:
		r.Closest = nearestName(at[len(at)-1], schemaProperties(c.doc, at[:len(at)-1]))
		r.Message = r.Field + " is not a parameter of this offering"
	case finding.CodeClassNotOffered:
		r.Allowed = k.(*kind.Enum).Want
		r.Closest = r.Allowed[0]
		r.Message = r.Field + " names a class this offering does not offer: use one of " + joinValues(r.Allowed)
	default:
		r.Closest, r.Allowed = closest(schemaNode(c.doc, at), k)
		r.Message = r.Field + " is outside this offering's parameters: use a value " + cmp.Or(schemaBound(c.doc, at), "the schema allows")
	}
	c.add(refused, at, r)
}

// add appends r, the refusal at path, unless that path already holds one,
// and marks it refused: one refusal per field, a sensitive one first.
func (c *valueCheck) add(refused map[string]bool, at []string, r OfferingValueRefusal) {
	if refused[paramKey(at)] {
		return
	}
	refused[paramKey(at)] = true
	c.out = append(c.out, r)
}

// closest is the value nearest a refused one the schema node accepts: the
// bound a number crossed, the first allowed value of an enum (with the list)
// or the const. nil when the violation names no such value.
func closest(node map[string]any, k jsonschema.ErrorKind) (any, []any) {
	switch e := k.(type) {
	case *kind.Minimum:
		return node["minimum"], nil
	case *kind.Maximum:
		return node["maximum"], nil
	case *kind.Enum:
		return e.Want[0], e.Want
	case *kind.Const:
		return node["const"], nil
	}
	return nil, nil
}

// nearestName is the declared name closest to got (see
// [contract.DidYouMean]), nil when none is close enough.
func nearestName(got string, names []string) any {
	hint := contract.DidYouMean(got, names) // ` (did you mean "x"?)`
	if hint == "" {
		return nil
	}
	name, _ := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(hint, " (did you mean "), "?)"))
	return name
}

// fieldAt is the field of path under prefix, its keys dotted.
func fieldAt(prefix string, path []string) string {
	if len(path) == 0 {
		return prefix
	}
	return prefix + "." + strings.Join(path, ".")
}
