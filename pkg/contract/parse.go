package contract

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Parse deserializes a pacto.yaml from the given reader into a Contract.
// It handles syntactic correctness only (field types, required top-level structure).
// Semantic validation is a separate concern handled by the validation engine.
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

	// Normalize: replicas shorthand sets min = max = replicas.
	if c.Scaling != nil && c.Scaling.Replicas != nil {
		c.Scaling.Min = *c.Scaling.Replicas
		c.Scaling.Max = *c.Scaling.Replicas
	}

	return &c, nil
}
