package contract

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Document is a contract document decoded into the shape the JSON Schema
// layers check: objects, arrays, float64, string, bool and nil, exactly what a
// round trip through encoding/json would give. Decoding YAML is the costly part
// of loading a contract, so a loader that already parsed the bytes
// ([ParseDocument]) hands the Document on and validation does not read the YAML
// a second time.
type Document struct {
	value any
	err   error
}

// JSON returns the decoded document, or the error decoding it hit.
func (d *Document) JSON() (any, error) { return d.value, d.err }

// DecodeDocument decodes raw into a [Document]. A failure is kept in the
// Document rather than returned, so every reader reports it the same way.
func DecodeDocument(raw []byte) *Document {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return &Document{err: err}
	}
	return documentFromNode(&root)
}

// ParseDocument is [Parse] over bytes already in memory that also returns the
// [Document], reading the YAML once for both.
func ParseDocument(raw []byte) (*Contract, *Document, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil || root.Kind == 0 {
		// Let the strict decoder phrase the error (and the empty document).
		c, err := parseStrict(raw)
		return c, nil, err
	}
	c, err := parseNode(&root, raw)
	if err != nil {
		return nil, nil, err
	}
	return c, documentFromNode(&root), nil
}

// parseNode decodes an already parsed document into a Contract. A Node decode
// has no unknown-field check, so knownFields stands in for it; anything it is
// unsure of, and any decode error, goes back to the strict decoder over raw,
// which is then the one answer, word for word what Parse says.
func parseNode(root *yaml.Node, raw []byte) (*Contract, error) {
	var c Contract
	if !knownFields(root, reflect.TypeOf(c)) || root.Decode(&c) != nil {
		return parseStrict(raw)
	}
	if err := checkRequired(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// documentFromNode decodes root into the Document. It untags timestamps first
// (see DecodeYAML), so it must run after the Contract decode, which keeps a
// date in Metadata a time.Time as Parse always has.
func documentFromNode(root *yaml.Node) *Document {
	if root.Kind == 0 {
		return &Document{} // empty document: nil, as yaml.Unmarshal leaves it
	}
	untagTimestamps(root)
	var v any
	if err := root.Decode(&v); err != nil {
		return &Document{err: err}
	}
	v, err := toJSON(v)
	return &Document{value: v, err: err}
}

// toJSON converts a generic YAML value to what json.Marshal then json.Unmarshal
// would make of it, without the round trip: numbers become float64, map keys
// strings, strings valid UTF-8. Like json.Marshal it refuses NaN and infinities.
func toJSON(v any) (any, error) {
	var err error
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if out[k], err = toJSON(val); err != nil {
				return nil, err
			}
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if out[fmt.Sprintf("%v", k)], err = toJSON(val); err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			if out[i], err = toJSON(val); err != nil {
				return nil, err
			}
		}
		return out, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			_, err := json.Marshal(v) // json's own wording
			return nil, err
		}
		return v, nil
	case string:
		if !utf8.ValidString(v) {
			// json.Marshal replaces each invalid byte (a !!binary value) with U+FFFD.
			var b strings.Builder
			for _, r := range v {
				b.WriteRune(r) // ranging a string yields RuneError per invalid byte
			}
			return b.String(), nil
		}
		return v, nil
	default: // bool, nil
		return v, nil
	}
}

// knownFields reports whether every mapping key in n names a field of t, the
// check yaml.v3 runs with KnownFields(true). It answers false when unsure
// (aliases, merge keys, inline fields), which only costs a strict re-decode.
func knownFields(n *yaml.Node, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if pt := reflect.PointerTo(t); pt.Implements(unmarshalerType) || pt.Implements(obsoleteUnmarshalerType) {
		return false // decodes itself: nothing to vouch for
	}
	switch n.Kind {
	case yaml.AliasNode:
		return false
	case yaml.DocumentNode:
		return len(n.Content) == 1 && knownFields(n.Content[0], t)
	case yaml.SequenceNode:
		if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
			return true // a type mismatch: the Node decode reports it
		}
		for _, item := range n.Content {
			if !knownFields(item, t.Elem()) {
				return false
			}
		}
	case yaml.MappingNode:
		switch t.Kind() {
		case reflect.Map:
			for i := 1; i < len(n.Content); i += 2 {
				if n.Content[i-1].Value == "<<" || !knownFields(n.Content[i], t.Elem()) {
					return false
				}
			}
		case reflect.Struct:
			fields, ok := structFields(t)
			if !ok {
				return false
			}
			for i := 1; i < len(n.Content); i += 2 {
				ft, known := fields[n.Content[i-1].Value]
				if !known || !knownFields(n.Content[i], ft) {
					return false
				}
			}
		}
	}
	return true
}

var (
	unmarshalerType         = reflect.TypeFor[yaml.Unmarshaler]()
	obsoleteUnmarshalerType = reflect.TypeFor[interface{ UnmarshalYAML(func(any) error) error }]()
)

var structFieldsCache sync.Map // reflect.Type -> map[string]reflect.Type (nil: unsure)

// structFields maps a struct's YAML keys to their field types the way yaml.v3
// names them; ok is false for a struct it cannot vouch for (an inline field).
func structFields(t reflect.Type) (map[string]reflect.Type, bool) {
	if v, ok := structFieldsCache.Load(t); ok {
		m := v.(map[string]reflect.Type)
		return m, m != nil
	}
	m := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue
		}
		// ponytail: yaml.v3 also reads a bare keyless tag as the yaml tag; go vet
		// rejects those, so they are not mirrored.
		name, flags, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if strings.Contains(flags, "inline") || f.Anonymous {
			m = nil
			break
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		m[name] = f.Type
	}
	structFieldsCache.Store(t, m)
	return m, m != nil
}
