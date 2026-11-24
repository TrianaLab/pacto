package schemax

import (
	"slices"
	"strings"
)

// Parameter is one field a consumer sets in an offering's configuration: its
// dot-notation path, type, tier (see [TierAt]), whether its parent object
// requires it and the bounds its schema declares. A sensitive field (one that
// is writeOnly, holds a writeOnly node or sits under a writeOnly object) has
// no Default and no Enum, since either can hold its value.
type Parameter struct {
	Path             string
	Type             string
	Tier             string
	Required         bool
	Enum             []any
	Minimum          *float64
	Maximum          *float64
	ExclusiveMinimum *float64
	ExclusiveMaximum *float64
	MinLength        *int
	MaxLength        *int
	Pattern          string
	Default          any
	Sensitive        bool
}

// Parameters lists the leaf fields of the configuration schema doc (a parsed
// document or a [Doc]), sorted by path: it follows properties from the root
// and recurses into each object that declares properties, as [Properties]
// flattens them. It is nil when doc is no schema object.
func Parameters(doc any) []Parameter {
	if d, ok := doc.(Doc); ok {
		doc = map[string]any(d)
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	var out []Parameter
	paramWalk(root, root, nil, false, &out)
	return out
}

// paramWalk appends the parameters under node, at path, to out. masked is
// true under a writeOnly object.
func paramWalk(root, node map[string]any, path []string, masked bool, out *[]Parameter) {
	props, _ := node["properties"].(map[string]any)
	required, _ := node["required"].([]any)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		sub, ok := props[k].(map[string]any)
		if !ok {
			continue // a boolean schema has nothing to set
		}
		p := append(slices.Clip(path), k)
		m := masked || sub["writeOnly"] == true
		if _, nested := sub["properties"].(map[string]any); nested && isObject(sub["type"]) {
			paramWalk(root, sub, p, m, out)
			continue
		}
		*out = append(*out, parameter(root, sub, p, slices.Contains(required, any(k)), m || HoldsWriteOnly(sub)))
	}
}

// parameter is the Parameter of the field node at path in root.
func parameter(root, node map[string]any, path []string, required, sensitive bool) Parameter {
	p := Parameter{
		Path: strings.Join(path, "."), Required: required, Sensitive: sensitive,
		Minimum: number(node["minimum"]), Maximum: number(node["maximum"]),
		ExclusiveMinimum: number(node["exclusiveMinimum"]), ExclusiveMaximum: number(node["exclusiveMaximum"]),
		MinLength: length(node["minLength"]), MaxLength: length(node["maxLength"]),
	}
	p.Tier, _ = TierAt(root, path)
	p.Type, _ = node["type"].(string)
	if list, ok := node["type"].([]any); ok && len(list) > 0 {
		p.Type, _ = list[0].(string)
	}
	p.Pattern, _ = node["pattern"].(string)
	if sensitive {
		return p
	}
	p.Default = node["default"]
	p.Enum, _ = node["enum"].([]any)
	if c, ok := node["const"]; ok && p.Enum == nil {
		p.Enum = []any{c} // ponytail: a const is a one-value enum to a consumer
	}
	return p
}

// number is v as a JSON Schema number, nil when v is none; JSON decodes
// float64 and YAML decodes integers.
func number(v any) *float64 {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint64:
		f = float64(n)
	default:
		return nil
	}
	return &f
}

// length is v as a JSON Schema length, nil when v is no number.
func length(v any) *int {
	f := number(v)
	if f == nil {
		return nil
	}
	n := int(*f)
	return &n
}

// HoldsWriteOnly reports whether node, or any schema node under it, is
// writeOnly.
// ponytail: a $ref is not followed, so a writeOnly definition it names stays unseen; resolve local refs when a consume schema uses them.
func HoldsWriteOnly(node any) bool {
	switch t := node.(type) {
	case map[string]any:
		if t["writeOnly"] == true {
			return true
		}
		for _, v := range t {
			if HoldsWriteOnly(v) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(t, HoldsWriteOnly)
	}
	return false
}
