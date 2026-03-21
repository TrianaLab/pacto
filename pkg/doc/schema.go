package doc

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Property represents a configuration property extracted from a JSON Schema.
type Property struct {
	Name        string
	Type        string
	Description string
	Default     string
	Required    bool
}

// readSchemaProperties compiles a JSON Schema file using the jsonschema library
// and extracts its top-level properties. All $ref pointers ($ref, $defs,
// definitions, nested chains, etc.) are resolved automatically by the compiler.
func readSchemaProperties(fsys fs.FS, path string) ([]Property, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading schema %s: %w", path, err)
	}

	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing schema %s: %w", path, err)
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource(path, doc); err != nil {
		return nil, fmt.Errorf("compiling schema %s: %w", path, err)
	}
	sch, err := c.Compile(path)
	if err != nil {
		return nil, fmt.Errorf("compiling schema %s: %w", path, err)
	}

	return flattenProperties("", resolveRef(sch)), nil
}

// resolveRef follows $ref pointers to reach the underlying schema.
func resolveRef(s *jsonschema.Schema) *jsonschema.Schema {
	for s.Ref != nil {
		s = s.Ref
	}
	return s
}

// flattenProperties recursively collects properties from a schema, prefixing
// nested property names with their parent path using dot notation
// (e.g. "postgres.host").
func flattenProperties(prefix string, sch *jsonschema.Schema) []Property {
	if len(sch.Properties) == 0 {
		return nil
	}

	requiredSet := make(map[string]bool, len(sch.Required))
	for _, r := range sch.Required {
		requiredSet[r] = true
	}

	names := make([]string, 0, len(sch.Properties))
	for name := range sch.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	var props []Property
	for _, name := range names {
		p := resolveRef(sch.Properties[name])
		fullName := prefix + name

		// If the property is an object with its own properties, recurse.
		if len(p.Properties) > 0 {
			props = append(props, flattenProperties(fullName+".", p)...)
			continue
		}

		prop := Property{
			Name:        fullName,
			Required:    requiredSet[name],
			Description: p.Description,
		}
		if p.Types != nil {
			prop.Type = strings.Join(p.Types.ToStrings(), ", ")
		}
		if p.Default != nil {
			prop.Default = fmt.Sprintf("%v", *p.Default)
		}
		props = append(props, prop)
	}

	return props
}
