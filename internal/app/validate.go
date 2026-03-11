package app

import (
	"context"
	"io/fs"
	"log/slog"

	"github.com/trianalab/pacto/internal/validation"
	"github.com/trianalab/pacto/pkg/contract"
)

// ValidateOptions holds options for the validate command.
type ValidateOptions struct {
	Path string
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
	bundle, err := s.resolveBundle(ctx, ref)
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
	} else {
		var readErr error
		rawYAML, readErr = fs.ReadFile(bundle.FS, DefaultContractPath)
		if readErr != nil {
			return nil, readErr
		}
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
