package schemax

import (
	"slices"
	"strconv"
)

// OpenPaths names every path of doc whose values are not a closed parameter
// space, sorted: an object node whose additionalProperties is not false or
// that declares patternProperties, a node that keeps unknown fields
// (x-kubernetes-preserve-unknown-fields: true), a node with no type, enum,
// const, allOf, anyOf or oneOf (a true schema included), and a class
// property with no enum. Paths start at "schema" and follow the keywords,
// such as schema.properties.backups. It walks
// properties, items, prefixItems and the members of allOf, anyOf and oneOf
// of a node that has no type, enum or const of its own.
// ponytail: $ref is not resolved, so a node that only refers reads open; a
// resolver if refs ever appear in offering schemas.
func OpenPaths(doc any) []string {
	var out []string
	openWalk(doc, "schema", &out)
	slices.Sort(out)
	return out
}

// openWalk appends the open paths of node, at path, and of its subschemas.
func openWalk(node any, path string, out *[]string) {
	m, ok := node.(map[string]any)
	switch {
	case node == false:
		return // a false schema admits no value
	case !ok:
		*out = append(*out, path)
		return
	}
	if open(m) {
		*out = append(*out, path)
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, v := range props {
			p := path + ".properties." + name
			if sub, ok := v.(map[string]any); ok && name == "class" && sub["enum"] == nil {
				*out = append(*out, p)
				continue
			}
			openWalk(v, p, out)
		}
	}
	if items, ok := m["items"].(map[string]any); ok {
		openWalk(items, path+".items", out)
	}
	keys := []string{"prefixItems", "allOf", "anyOf", "oneOf"}
	if constrained(m) {
		// A junctor beside a type, enum or const only narrows values the node
		// already bounds, and a Kubernetes structural schema forbids a type
		// inside one: its untyped members (oneOf: [{required: [a]}]) are no
		// open space.
		keys = keys[:1]
	}
	for _, k := range keys {
		list, _ := m[k].([]any)
		for i, v := range list {
			openWalk(v, path+"."+k+"["+strconv.Itoa(i)+"]", out)
		}
	}
}

// open reports whether the node m itself leaves its values open.
func open(m map[string]any) bool {
	switch {
	case m["x-kubernetes-preserve-unknown-fields"] == true, m["patternProperties"] != nil:
		return true
	case isObject(m["type"]):
		return m["additionalProperties"] != false
	}
	if constrained(m) {
		return false
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		if _, set := m[k]; set {
			return false
		}
	}
	return true
}

// constrained reports whether m bounds its values itself, with a type, an
// enum or a const.
func constrained(m map[string]any) bool {
	for _, k := range []string{"type", "enum", "const"} {
		if _, set := m[k]; set {
			return true
		}
	}
	return false
}
