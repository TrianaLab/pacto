// Package schemax extracts human-readable property summaries from JSON Schema
// documents and configuration value maps.
//
// It is the single source of truth shared by the dashboard (which renders
// contract bundles) and the operator (which mirrors a contract's config/policy
// content into CR status). Sharing this code guarantees a service renders
// identically whether its data is read from an OCI bundle or from the
// operator's status. The package is intentionally dependency-light so the
// operator can import it without pulling in graph/validation.
package schemax

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Property is a flattened entry from a JSON Schema or a configuration values
// map, suitable for display.
type Property struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// unmarshalSpec parses a schema document as JSON (for .json paths) or YAML.
func unmarshalSpec(data []byte, path string) (map[string]any, error) {
	var spec map[string]any
	if strings.HasSuffix(path, ".json") {
		return spec, json.Unmarshal(data, &spec)
	}
	return spec, yaml.Unmarshal(data, &spec)
}

// Properties parses a JSON Schema document and returns its top-level properties
// flattened with dot-notation keys (nested objects are recursed). It returns
// nil when the document is unparseable or declares no properties.
func Properties(data []byte, path string) []Property {
	spec, err := unmarshalSpec(data, path)
	if err != nil {
		return nil
	}
	propsRaw, ok := spec["properties"]
	if !ok {
		return nil
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil
	}
	var out []Property
	flattenProps("", props, &out)
	return out
}

// flattenProps recursively walks JSON Schema properties, producing Property
// entries. Nested objects use dot-notation (e.g. "cors.enabled").
func flattenProps(prefix string, props map[string]any, out *[]Property) {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		propRaw, ok := props[k].(map[string]any)
		if !ok {
			continue
		}
		// If this property is an object with sub-properties, recurse.
		if subType, _ := propRaw["type"].(string); subType == "object" {
			if subPropsRaw, ok := propRaw["properties"]; ok {
				if subProps, ok := subPropsRaw.(map[string]any); ok {
					flattenProps(fullKey, subProps, out)
					continue
				}
			}
		}
		p := Property{Key: fullKey}
		if t, ok := propRaw["type"].(string); ok {
			p.Type = t
		}
		if def, ok := propRaw["default"]; ok {
			p.Value = fmt.Sprintf("%v", def)
		} else {
			p.Value = "(any)"
		}
		*out = append(*out, p)
	}
}

// Values flattens a configuration values map into sorted Property entries,
// inferring a type for each entry.
func Values(m map[string]any) []Property {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Property, 0, len(m))
	for _, k := range keys {
		p := Property{Key: k}
		switch val := m[k].(type) {
		case string:
			p.Value, p.Type = val, "string"
		case float64:
			p.Value, p.Type = fmt.Sprintf("%g", val), "number"
		case int:
			p.Value, p.Type = fmt.Sprintf("%d", val), "number"
		case bool:
			p.Value, p.Type = fmt.Sprintf("%t", val), "boolean"
		case nil:
			p.Value, p.Type = "(any)", "any"
		default:
			p.Value, p.Type = fmt.Sprintf("%v", val), "object"
		}
		out = append(out, p)
	}
	return out
}

// Meta returns the title and description declared at the root of a JSON Schema.
func Meta(data []byte, path string) (title, description string) {
	spec, err := unmarshalSpec(data, path)
	if err != nil {
		return "", ""
	}
	if t, ok := spec["title"].(string); ok {
		title = t
	}
	if d, ok := spec["description"].(string); ok {
		description = d
	}
	return title, description
}
