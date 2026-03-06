package doc

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
)

// Property represents a configuration property extracted from a JSON Schema.
type Property struct {
	Name        string
	Type        string
	Description string
	Default     string
	Required    bool
}

// readSchemaProperties parses a JSON Schema file and returns its top-level properties.
func readSchemaProperties(fsys fs.FS, path string) ([]Property, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading schema %s: %w", path, err)
	}

	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
			Default     any    `json:"default"`
		} `json:"properties"`
		Required []string `json:"required"`
	}

	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parsing schema %s: %w", path, err)
	}

	if len(schema.Properties) == 0 {
		return nil, nil
	}

	requiredSet := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		requiredSet[r] = true
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	props := make([]Property, 0, len(names))
	for _, name := range names {
		p := schema.Properties[name]
		prop := Property{
			Name:        name,
			Type:        p.Type,
			Description: p.Description,
			Required:    requiredSet[name],
		}
		if p.Default != nil {
			prop.Default = fmt.Sprintf("%v", p.Default)
		}
		props = append(props, prop)
	}

	return props, nil
}
