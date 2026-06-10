package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/override"
	"github.com/trianalab/pacto/pkg/readiness"
	"github.com/trianalab/pacto/pkg/validation"
)

// ValidateOptions holds options for the validate command.
type ValidateOptions struct {
	Path      string
	Overrides override.Overrides
	// Readiness, when true, also enforces the readiness gate: validation fails if
	// the derived readiness score is below the declared (or default 100) minScore.
	// Off by default because the gate is time-dependent (it reads expiry dates),
	// which would otherwise make plain validation non-deterministic.
	Readiness bool
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
	var resolver validation.BundleResolver
	if s.BundleStore != nil {
		resolver = &bundleResolverAdapter{svc: s}
	}
	result := validation.ValidateWithResolver(ctx, bundle.Contract, rawYAML, bundle.FS, resolver)
	slog.Debug("validation complete", "valid", result.IsValid(), "errors", len(result.Errors), "warnings", len(result.Warnings))

	errors := result.Errors
	valid := result.IsValid()

	// Opt-in readiness gate. Time-dependent, so only when explicitly requested.
	if opts.Readiness {
		if eval := readiness.Evaluate(bundle.Contract.Readiness, timeNow()); eval != nil && !eval.Passing {
			errors = append(errors, contract.ValidationError{
				Path: "readiness",
				Code: "READINESS_GATE_UNMET",
				Message: fmt.Sprintf("readiness score %d is below minScore %d (%d current, %d expired, %d invalid of %d checks)",
					eval.Score, eval.MinScore, eval.CurrentCount, eval.ExpiredCount, eval.InvalidCount, len(eval.Checks)),
			})
			valid = false
		}
	}

	return &ValidateResult{
		Path:     ref,
		Valid:    valid,
		Errors:   errors,
		Warnings: result.Warnings,
	}, nil
}

// bundleResolverAdapter adapts *Service to the validation.BundleResolver interface.
type bundleResolverAdapter struct {
	svc *Service
}

func (a *bundleResolverAdapter) ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	return a.svc.resolveBundle(ctx, ref)
}
