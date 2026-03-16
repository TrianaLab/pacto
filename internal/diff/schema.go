package diff

import (
	"encoding/json"
	"io/fs"
)

// diffSchema compares two JSON Schema files (configuration schemas)
// and returns changes for added/removed properties.
func diffSchema(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	return diffFileSet(oldPath, newPath, oldFS, newFS, readSchemaProperties, "schema.properties", "configuration property")
}

// readSchemaProperties reads a JSON Schema and extracts property keys,
// recursively flattening nested objects using dot notation (e.g. "postgres.host").
func readSchemaProperties(fsys fs.FS, path string) (map[string]bool, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var schema schemaNode
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}

	props := make(map[string]bool)
	flattenSchemaKeys("", schema.Properties, props)
	return props, nil
}

// schemaNode represents a JSON Schema node with properties and type.
type schemaNode struct {
	Properties map[string]schemaNode `json:"properties"`
	Type       string                `json:"type"`
}

// flattenSchemaKeys recursively collects property keys from a schema,
// using dot notation for nested object properties.
func flattenSchemaKeys(prefix string, properties map[string]schemaNode, out map[string]bool) {
	for name, node := range properties {
		fullName := prefix + name
		if len(node.Properties) > 0 {
			flattenSchemaKeys(fullName+".", node.Properties, out)
		} else {
			out[fullName] = true
		}
	}
}
