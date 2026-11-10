package contract

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Environment names one environment the service runs in and the bundle file
// holding the configuration values it uses there (pactoVersion 2.3). The file
// maps a configuration name to that configuration's values, which validation
// checks against the configuration's schema. Lifetime, Expires and Owner are
// pactoVersion 2.4: Lifetime is temporary or persistent ("" when not declared,
// which is never read as persistent), Expires the RFC 3339 time a temporary
// environment is meant to end, and Owner the team that owns it when that is
// not the service owner.
type Environment struct {
	Name     string `yaml:"name" json:"name"`
	File     string `yaml:"file" json:"file"`
	Lifetime string `yaml:"lifetime,omitempty" json:"lifetime,omitempty"`
	Expires  string `yaml:"expires,omitempty" json:"expires,omitempty"`
	Owner    *Owner `yaml:"owner,omitempty" json:"owner,omitempty"`
}

// The lifetimes an environment declares.
const (
	EnvironmentTemporary  = "temporary"
	EnvironmentPersistent = "persistent"
)

// ExpiresAt is Expires parsed, and false when it is empty or not RFC 3339.
func (e Environment) ExpiresAt() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, e.Expires)
	return t, err == nil
}

// Summary is the one-line text form: name, file and the declared fields, an
// undeclared lifetime written as such.
func (e Environment) Summary() string {
	parts := []string{e.Name + ": " + e.File, "lifetime " + cmp.Or(e.Lifetime, "not declared")}
	if e.Expires != "" {
		parts = append(parts, "expires "+e.Expires)
	}
	if e.Owner != nil && e.Owner.DisplayString() != "" {
		parts = append(parts, "owner "+e.Owner.DisplayString())
	}
	return strings.Join(parts, ", ")
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
