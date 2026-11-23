package schemax

import "slices"

// Walk calls fn on node and on every subschema below it: the values of
// properties, patternProperties, $defs and definitions, items (one schema or
// a list), prefixItems, a schema-valued additionalProperties, the members of
// allOf, anyOf and oneOf, and not. A node that is no object is skipped.
func Walk(node any, fn func(map[string]any)) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	fn(m)
	for _, k := range []string{"properties", "patternProperties", "$defs", "definitions"} {
		if sub, ok := m[k].(map[string]any); ok {
			for _, v := range sub {
				Walk(v, fn)
			}
		}
	}
	for _, k := range []string{"items", "prefixItems", "allOf", "anyOf", "oneOf"} {
		if list, ok := m[k].([]any); ok {
			for _, v := range list {
				Walk(v, fn)
			}
		}
	}
	for _, k := range []string{"items", "additionalProperties", "not"} {
		Walk(m[k], fn)
	}
}

// Close sets additionalProperties: false on every object node of doc that
// leaves it unset: a node whose type is, or lists, "object". An explicit
// additionalProperties is kept, and an untyped node stays open, so a caller
// that needs a closed parameter space can still name what is open.
func Close(doc any) {
	Walk(doc, func(m map[string]any) {
		if _, set := m["additionalProperties"]; set || !isObject(m["type"]) {
			return
		}
		m["additionalProperties"] = false
	})
}

// isObject reports whether a schema type is "object" or a list holding it.
func isObject(t any) bool {
	switch v := t.(type) {
	case string:
		return v == "object"
	case []any:
		return slices.Contains(v, any("object"))
	}
	return false
}
