package contract

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Environment names one environment the service runs in and the bundle file
// holding the configuration values it uses there (pactoVersion 2.3). The file
// maps a configuration name to that configuration's values, which validation
// checks against the configuration's schema. From pactoVersion 2.6 the file
// is optional: an environment with none adds no values of its own, so each
// configuration runs on its defaults. Lifetime, Expires and Owner are
// pactoVersion 2.4: Lifetime is temporary or persistent ("" when not declared,
// which is never read as persistent), Expires the RFC 3339 time a temporary
// environment is meant to end, and Owner the team that owns it when that is
// not the service owner.
type Environment struct {
	Name     string `yaml:"name" json:"name"`
	File     string `yaml:"file,omitempty" json:"file,omitempty"`
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

// ExpiredAt reports whether Expires is at or before asOf. An unparsed Expires
// or a zero asOf (no time known) is never expired.
func (e Environment) ExpiredAt(asOf time.Time) bool {
	at, ok := e.ExpiresAt()
	return ok && !asOf.IsZero() && !at.After(asOf)
}

// Summary is the one-line text form: name, file and the declared fields, an
// undeclared lifetime written as such and a missing file as no values file.
func (e Environment) Summary() string {
	parts := []string{e.Name + ": " + cmp.Or(e.File, "no values file"), "lifetime " + cmp.Or(e.Lifetime, "not declared")}
	if e.Expires != "" {
		parts = append(parts, "expires "+e.Expires)
	}
	if e.Owner != nil && e.Owner.DisplayString() != "" {
		parts = append(parts, "owner "+e.Owner.DisplayString())
	}
	return strings.Join(parts, ", ")
}

// ReadEnvironmentValues reads e's values file from fsys and returns its bytes
// and its values (see [ParseEnvironmentValues]). An environment with no file
// (pactoVersion 2.6) has no values of its own: it reads as empty values and
// fsys is never touched, so it may be nil.
func ReadEnvironmentValues(fsys fs.FS, e Environment) ([]byte, map[string]any, error) {
	if e.File == "" {
		return nil, map[string]any{}, nil
	}
	raw, err := fs.ReadFile(fsys, e.File)
	if err != nil {
		return nil, nil, err
	}
	values, err := ParseEnvironmentValues(raw)
	return raw, values, err
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
