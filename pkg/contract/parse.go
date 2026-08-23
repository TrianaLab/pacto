package contract

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Parse deserializes a pacto.yaml from the given reader into a Contract.
// It enforces syntactic correctness (field types, unknown-field rejection) and a
// few required top-level fields (pactoVersion, service.name, service.version).
// Only pactoVersion "2.0" is supported; other versions are rejected.
// Deeper semantic validation is a separate concern handled by the validation engine.
func Parse(r io.Reader) (*Contract, error) {
	var c Contract
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return nil, &ParseError{
			Message: fmt.Sprintf("failed to parse YAML: %v", err),
			Err:     err,
		}
	}

	if c.PactoVersion == "" {
		return nil, &ParseError{
			Path:    "pactoVersion",
			Message: "pactoVersion is required",
		}
	}

	if c.PactoVersion != "2.0" {
		return nil, &ParseError{
			Path:    "pactoVersion",
			Message: fmt.Sprintf("unsupported pactoVersion %q; only \"2.0\" is supported", c.PactoVersion),
		}
	}

	if c.Service.Name == "" {
		return nil, &ParseError{
			Path:    "service.name",
			Message: "service.name is required",
		}
	}

	if c.Service.Version == "" {
		return nil, &ParseError{
			Path:    "service.version",
			Message: "service.version is required",
		}
	}

	return &c, nil
}

// DecodeYAML unmarshals YAML into out the way Parse sees it: a scalar that
// yaml.v3 resolves to !!timestamp keeps the verbatim text written in the
// document instead of becoming a time.Time.
//
// Parse decodes dates into string fields, so it always sees what the author
// wrote. A generic decode does not: `expires: 2099-12-31` resolves to a
// time.Time, so a read-modify-write re-emits it as 2099-12-31T00:00:00Z and the
// JSON Schema layer checks a value that is not in the file. Retagging the node
// before decoding preserves the scalar exactly — nothing is reformatted, so a
// non-canonical `2099-1-1` stays rejectable and an explicit
// `2024-01-15T00:00:00Z` keeps its time — and it is independent of the map kind
// yaml.v3 picks for the enclosing mapping.
func DecodeYAML(data []byte, out any) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if root.Kind == 0 {
		return nil // empty document: leave out at its zero value, as yaml.Unmarshal does
	}
	untagTimestamps(&root)
	return root.Decode(out)
}

// untagTimestamps rewrites every !!timestamp scalar in the tree to !!str so it
// decodes as its literal text.
func untagTimestamps(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!timestamp" {
		n.Tag = "!!str"
	}
	for _, child := range n.Content {
		untagTimestamps(child)
	}
}
