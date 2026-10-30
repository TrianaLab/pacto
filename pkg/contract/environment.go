package contract

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Environment names one environment the service runs in and the bundle file
// holding the configuration values it uses there (pactoVersion 2.3). The file
// maps a configuration name to that configuration's values, which validation
// checks against the configuration's schema.
type Environment struct {
	Name string `yaml:"name" json:"name"`
	File string `yaml:"file" json:"file"`
}

// ParseEnvironmentValues decodes an environment's file, YAML or JSON, into
// configuration name → values. It decodes scalars as [DecodeYAML] does, so a
// date stays the text it was written as, and every number as JSON decodes it,
// so a JSON Schema reads the values the way it reads a JSON document. Every
// key must be a string. An empty file holds no values.
func ParseEnvironmentValues(data []byte) (map[string]any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if err := stringKeys(&root); err != nil {
		return nil, err
	}
	var doc any
	if err := decodeNode(&root, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return map[string]any{}, nil
	}
	if _, ok := doc.(map[string]any); !ok {
		return nil, fmt.Errorf("the file must map each configuration name to its values")
	}
	b, _ := json.Marshal(doc) // every key is a string, checked above
	var out map[string]any
	_ = json.Unmarshal(b, &out) // b is a JSON object encoded just above
	return out, nil
}

// stringKeys reports the first mapping key under n that is not a string, a
// key JSON cannot hold.
func stringKeys(n *yaml.Node) error {
	for i, child := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 && child.Tag != "!!str" && child.Tag != "!!merge" {
			return fmt.Errorf("line %d: key %s is not a string; quote it, as in %q", child.Line, child.Value, child.Value)
		}
		if err := stringKeys(child); err != nil {
			return err
		}
	}
	return nil
}

// DeepMerge merges src into dst, recursing into objects present on both sides.
// Values in src win.
func DeepMerge(dst, src map[string]any) {
	for k, srcVal := range src {
		dstMap, dstIsMap := dst[k].(map[string]any)
		srcMap, srcIsMap := srcVal.(map[string]any)
		if dstIsMap && srcIsMap {
			DeepMerge(dstMap, srcMap)
		} else {
			dst[k] = srcVal
		}
	}
}
