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
