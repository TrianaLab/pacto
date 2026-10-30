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
	Name   string `yaml:"name" json:"name"`
	Values string `yaml:"values" json:"values"`
}

// ParseEnvironmentValues decodes an environment values file, YAML or JSON, into
// configuration name → values, with every number decoded as JSON decodes it so
// a JSON Schema reads the values the way it reads a JSON document. An empty
// file holds no values.
func ParseEnvironmentValues(data []byte) (map[string]any, error) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return map[string]any{}, nil
	}
	if _, ok := doc.(map[string]any); !ok {
		return nil, fmt.Errorf("the file must map each configuration name to its values")
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out) // b is a JSON object encoded just above
	return out, nil
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
