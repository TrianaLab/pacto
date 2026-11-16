package contract

import "gopkg.in/yaml.v3"

// applyDependencyDefaults fills the parse-time defaults of each dependency from
// the document it was decoded from: required is true when the mapping has no
// required key, and an absent name is derived from ref (marked NameFromRef).
// It reads the node rather than implementing yaml.Unmarshaler on Dependency,
// which would take every contract with dependencies off knownFields' fast path
// and drop the unknown-field check inside a dependency.
func applyDependencyDefaults(root *yaml.Node, c *Contract) {
	deps := mappingValue(root, "dependencies")
	if deps == nil || deps.Kind != yaml.SequenceNode {
		return
	}
	for i, item := range deps.Content { // one decoded dependency per item
		d := &c.Dependencies[i]
		if mappingValue(item, "required") == nil {
			d.Required = true
		}
		if d.Name == "" {
			d.Name = DependencyName(d.Ref)
			d.NameFromRef = d.Name != ""
		}
	}
}

// mappingValue returns the value of key in the mapping n (through a document
// or an alias), following merge keys; nil when n holds no such key.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	switch n = resolveNode(n); n.Kind {
	case yaml.SequenceNode: // the value of a merge key: a list of mappings
		for _, m := range n.Content {
			if v := mappingValue(m, key); v != nil {
				return v
			}
		}
	case yaml.MappingNode:
		var merged *yaml.Node
		for i := 1; i < len(n.Content); i += 2 {
			switch n.Content[i-1].Value {
			case key:
				return resolveNode(n.Content[i])
			case "<<":
				merged = n.Content[i]
			}
		}
		if merged != nil {
			return mappingValue(merged, key)
		}
	}
	return nil
}

// resolveNode follows documents and aliases to the node they stand for. An
// empty document is a zero Node, never a DocumentNode, so a document always
// holds exactly one node.
func resolveNode(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.DocumentNode || n.Kind == yaml.AliasNode {
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		} else {
			n = n.Content[0]
		}
	}
	return n
}
