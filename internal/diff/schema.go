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

// readSchemaProperties reads a JSON Schema and extracts top-level property keys.
func readSchemaProperties(fsys fs.FS, path string) (map[string]bool, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}

	props := make(map[string]bool, len(schema.Properties))
	for p := range schema.Properties {
		props[p] = true
	}
	return props, nil
}
