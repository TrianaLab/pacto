package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/override"
	"github.com/trianalab/pacto/pkg/validation"
)

// ValidateOptions holds options for the validate command.
type ValidateOptions struct {
	Path      string
	Overrides override.Overrides
}

// ValidateResult holds the result of the validate command.
type ValidateResult struct {
	Path     string
	Valid    bool
	Errors   []contract.ValidationError
	Warnings []contract.ValidationWarning
}

// Validate loads a contract, runs validation, and returns the result.
func (s *Service) Validate(ctx context.Context, opts ValidateOptions) (*ValidateResult, error) {
	ref := defaultPath(opts.Path)

	slog.Debug("resolving contract for validation", "ref", ref)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return &ValidateResult{
			Path:  ref,
			Valid: false,
			Errors: []contract.ValidationError{
				{Path: "", Code: "PARSE_ERROR", Message: err.Error()},
			},
		}, nil
	}

	// Determine raw YAML for structural validation.
	var rawYAML []byte
	if bundle.RawYAML != nil {
		rawYAML = bundle.RawYAML
	} else if bundle.FS != nil {
		var readErr error
		rawYAML, readErr = fs.ReadFile(bundle.FS, DefaultContractPath)
		if readErr != nil {
			return nil, readErr
		}
	} else {
		return nil, fmt.Errorf("bundle has no raw YAML or filesystem")
	}

	slog.Debug("running validation", "ref", ref)
	result := validation.Validate(bundle.Contract, rawYAML, bundle.FS)
	slog.Debug("validation complete", "valid", result.IsValid(), "errors", len(result.Errors), "warnings", len(result.Warnings))

	return &ValidateResult{
		Path:     ref,
		Valid:    result.IsValid(),
		Errors:   result.Errors,
		Warnings: result.Warnings,
	}, nil
}
