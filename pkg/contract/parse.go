package contract

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SupportedVersions are the pactoVersion values Parse accepts, oldest first.
var SupportedVersions = []string{"2.0", "2.1", "2.2"}

// Parse deserializes a pacto.yaml from the given reader into a Contract.
// It enforces syntactic correctness (field types, unknown-field rejection) and a
// few required top-level fields (pactoVersion, service.name, service.version).
// pactoVersion "2.0", "2.1" and "2.2" are supported; other versions are
// rejected. 2.1 adds the publish and checks sections, external dependencies and
// the mcp interface type, which validation refuses in a 2.0 contract; 2.2 adds
// fields validation refuses in a 2.0 or 2.1 contract.
// Deeper semantic validation is a separate concern handled by the validation engine.
func Parse(r io.Reader) (*Contract, error) {
	var c Contract
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return nil, &ParseError{
			Message: suggestFields(fmt.Sprintf("failed to parse YAML: %v", err)),
			Err:     err,
		}
	}
	if err := checkRequired(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func parseStrict(raw []byte) (*Contract, error) { return Parse(bytes.NewReader(raw)) }

// checkRequired enforces the top-level fields Parse requires.
func checkRequired(c *Contract) error {
	if c.PactoVersion == "" {
		return &ParseError{
			Path:    "pactoVersion",
			Message: "pactoVersion is required",
		}
	}

	if !slices.Contains(SupportedVersions, c.PactoVersion) {
		return &ParseError{
			Path:    "pactoVersion",
			Message: fmt.Sprintf("unsupported pactoVersion %q; supported: %s", c.PactoVersion, strings.Join(SupportedVersions, ", ")),
		}
	}

	if c.Service.Name == "" {
		return &ParseError{
			Path:    "service.name",
			Message: "service.name is required",
		}
	}

	if c.Service.Version == "" {
		return &ParseError{
			Path:    "service.version",
			Message: "service.version is required",
		}
	}
	return nil
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
