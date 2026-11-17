// Package schemax extracts human-readable property summaries from JSON Schema
// documents and configuration value maps.
//
// It is the single source of truth shared by the dashboard (which renders
// contract bundles) and the operator (which mirrors a contract's config/policy
// content into CR status). Sharing this code guarantees a service renders
// identically whether its data is read from an OCI bundle or from the
// operator's status. The package is intentionally dependency-light so the
// operator can import it without pulling in graph/validation.
package schemax

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Property is a flattened entry from a JSON Schema or a configuration values
// map, suitable for display. A writeOnly schema property, and every property
// under a writeOnly object, has the Value (sensitive) and never its default.
type Property struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// unmarshalSpec parses a schema document as JSON (for .json paths) or YAML.
func unmarshalSpec(data []byte, path string) (map[string]any, error) {
	var spec map[string]any
	if strings.HasSuffix(path, ".json") {
		return spec, json.Unmarshal(data, &spec)
	}
	return spec, yaml.Unmarshal(data, &spec)
}

// Doc is a parsed schema document.
type Doc map[string]any

// Parse parses data as a schema document (JSON for .json paths, YAML
// otherwise), once, for a caller that needs its properties and its meta. It
// tells an unparseable document apart from one that declares no properties,
// which [Properties] cannot.
func Parse(data []byte, path string) (Doc, error) {
	spec, err := unmarshalSpec(data, path)
	return Doc(spec), err
}

// Properties parses a JSON Schema document and returns its top-level properties
// flattened with dot-notation keys (nested objects are recursed). It returns
// nil when the document is unparseable or declares no properties.
func Properties(data []byte, path string) []Property {
	spec, err := Parse(data, path)
	if err != nil {
		return nil
	}
	return spec.Properties()
}

// Properties is [Properties] of a parsed document.
func (spec Doc) Properties() []Property {
	propsRaw, ok := spec["properties"]
	if !ok {
		return nil
	}
	props, ok := propsRaw.(map[string]any)
	if !ok {
		return nil
	}
	var out []Property
	flattenProps("", props, false, &out)
	return out
}

// flattenProps recursively walks JSON Schema properties, producing Property
// entries. Nested objects use dot-notation (e.g. "cors.enabled"). masked is
// true under a writeOnly object, whose children inherit the mask.
func flattenProps(prefix string, props map[string]any, masked bool, out *[]Property) {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		propRaw, ok := props[k].(map[string]any)
		if !ok {
			continue
		}
		m := masked || propRaw["writeOnly"] == true
		// If this property is an object with sub-properties, recurse.
		if subType, _ := propRaw["type"].(string); subType == "object" {
			if subProps, ok := propRaw["properties"].(map[string]any); ok {
				flattenProps(fullKey, subProps, m, out)
				continue
			}
		}
		*out = append(*out, leaf(fullKey, propRaw, m))
	}
}

// Sensitive is the value every summary and diff shows for a writeOnly or
// sensitive key, in place of its default or value.
const Sensitive = "(sensitive)"

// leaf is the Property of one schema property: its default, (any) without
// one, or (sensitive) when masked.
func leaf(key string, propRaw map[string]any, masked bool) Property {
	p := Property{Key: key, Value: "(any)"}
	p.Type, _ = propRaw["type"].(string)
	if def, ok := propRaw["default"]; masked {
		p.Value = Sensitive
	} else if ok {
		p.Value = fmt.Sprintf("%v", def)
	}
	return p
}

// Values flattens a configuration values map into sorted Property entries,
// inferring a type for each entry.
func Values(m map[string]any) []Property {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Property, 0, len(m))
	for _, k := range keys {
		p := Property{Key: k}
		switch val := m[k].(type) {
		case string:
			p.Value, p.Type = val, "string"
		case float64:
			p.Value, p.Type = fmt.Sprintf("%g", val), "number"
		case int:
			p.Value, p.Type = fmt.Sprintf("%d", val), "number"
		case bool:
			p.Value, p.Type = fmt.Sprintf("%t", val), "boolean"
		case nil:
			p.Value, p.Type = "(any)", "any"
		default:
			p.Value, p.Type = fmt.Sprintf("%v", val), "object"
		}
		out = append(out, p)
	}
	return out
}

// Meta returns the title and description declared at the root of a JSON Schema.
func Meta(data []byte, path string) (title, description string) {
	spec, err := Parse(data, path)
	if err != nil {
		return "", ""
	}
	return spec.Meta()
}

// Meta is [Meta] of a parsed document.
func (spec Doc) Meta() (title, description string) {
	if t, ok := spec["title"].(string); ok {
		title = t
	}
	if d, ok := spec["description"].(string); ok {
		description = d
	}
	return title, description
}

// sensitivePatterns are the parts of a key name that mark its value as
// sensitive.
// ponytail: substring match; KEYCLOAK_URL and MONKEY read as sensitive, which masks them in a summary or a diff and, in a pacto init --cluster draft, moves their literal into a Secret the owner creates. A word-boundary rule when a false positive hides or moves something a reader needs.
var sensitivePatterns = []string{"password", "passwd", "secret", "token", "key", "credential", "private", "dsn"}

// SensitiveKey reports whether a configuration key's name, in any case,
// contains one of the sensitive patterns: password, passwd, secret, token, key,
// credential, private or dsn.
func SensitiveKey(name string) bool {
	name = strings.ToLower(name)
	for _, p := range sensitivePatterns {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// credRE is a URL, or a scheme-less user:password@host, whose userinfo holds
// a password, read from the raw text when url.Parse rejects the value: a
// password with an unescaped %, /, ?, # or space.
// ponytail: mailto:user@example.com matches the scheme-less form, which only masks it.
var credRE = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]*://[^@/]*:[^@]+|[^@/:\s]+:[^@/\s]+)@`)

// SensitiveValue reports whether a value is a URL that carries a credential:
// a non-empty password in its userinfo, or a query parameter with a sensitive
// name. A value url.Parse rejects, or reads as an opaque scheme:rest, is read
// with credRE instead, so a malformed password still counts. It never returns
// the value.
func SensitiveValue(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.Opaque != "" {
		return credRE.MatchString(v)
	}
	if u.Scheme == "" {
		return false
	}
	if p, ok := u.User.Password(); ok && p != "" {
		return true
	}
	for k := range u.Query() {
		if SensitiveKey(k) {
			return true
		}
	}
	return false
}
