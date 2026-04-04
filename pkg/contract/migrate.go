package contract

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// UnmarshalYAML implements yaml.Unmarshaler to handle backward-compatible
// migration from the v1 contract format (singular "configuration" field,
// dependencies/policies without "name") to the current plural format.
func (c *Contract) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		migrateContractNode(node)
	}
	// Type alias prevents infinite recursion.
	type contractAlias Contract
	var alias contractAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*c = Contract(alias)
	return nil
}

// migrateContractNode transforms legacy v1 YAML nodes to the current format.
func migrateContractNode(node *yaml.Node) {
	for i := 0; i < len(node.Content)-1; i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]

		switch key.Value {
		case "configuration":
			key.Value = "configurations"
			migrateConfigurationValue(node, i)
		case "dependencies":
			if val.Kind == yaml.SequenceNode {
				for _, dep := range val.Content {
					ensureNameField(dep, "ref")
				}
			}
		case "policies":
			if val.Kind == yaml.SequenceNode {
				for _, pol := range val.Content {
					ensureNameField(pol, "")
				}
			}
		}
	}
}

// migrateConfigurationValue converts the old singular configuration value
// to the new configurations array format.
func migrateConfigurationValue(parent *yaml.Node, idx int) {
	val := parent.Content[idx+1]
	if val.Kind != yaml.MappingNode {
		return
	}

	// Check if the old Configuration has a "configs" sub-array.
	for j := 0; j < len(val.Content)-1; j += 2 {
		if val.Content[j].Value == "configs" {
			configsVal := val.Content[j+1]
			if configsVal.Kind == yaml.SequenceNode {
				parent.Content[idx+1] = configsVal
				return
			}
		}
	}

	// Legacy single configuration object: remove any "configs" key (if present
	// but empty), add name="default", and wrap in an array.
	addNameToMapping(val, "default")
	parent.Content[idx+1] = &yaml.Node{
		Kind:    yaml.SequenceNode,
		Content: []*yaml.Node{val},
	}
}

// ensureNameField adds a "name" key to a mapping node if not already present.
// If deriveFrom is set and the mapping has that key, the name is derived from
// its value (e.g. the last path segment of an OCI reference).
func ensureNameField(node *yaml.Node, deriveFrom string) {
	if node.Kind != yaml.MappingNode {
		return
	}

	for j := 0; j < len(node.Content)-1; j += 2 {
		if node.Content[j].Value == "name" {
			return // already has a name
		}
	}

	name := "default"
	if deriveFrom != "" {
		for j := 0; j < len(node.Content)-1; j += 2 {
			if node.Content[j].Value == deriveFrom {
				name = deriveNameFromRef(node.Content[j+1].Value)
				break
			}
		}
	}
	addNameToMapping(node, name)
}

// addNameToMapping prepends a name field to a mapping node.
func addNameToMapping(node *yaml.Node, name string) {
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: "name", Tag: "!!str"}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: name, Tag: "!!str"}
	node.Content = append([]*yaml.Node{keyNode, valNode}, node.Content...)
}

// deriveNameFromRef extracts a short name from an OCI reference.
// "ghcr.io/org/repo/svc-name:1.0.0" → "svc-name"
func deriveNameFromRef(ref string) string {
	name := ref
	if idx := strings.LastIndex(name, "@"); idx != -1 {
		name = name[:idx]
	}
	if idx := strings.LastIndex(name, ":"); idx != -1 {
		name = name[:idx]
	}
	if idx := strings.LastIndex(name, "/"); idx != -1 {
		name = name[idx+1:]
	}
	if name == "" {
		return "default"
	}
	return name
}
