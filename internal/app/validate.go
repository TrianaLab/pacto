package app

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/override"
	"github.com/trianalab/pacto/v3/pkg/readiness"
	"github.com/trianalab/pacto/v3/pkg/validation"
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

	logging.LoggerFromContext(ctx).Debug("resolving contract for validation", "ref", ref)
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

	if lerr := s.verifyLockIfPresent(ctx, ref, bundle); lerr != nil {
		return &ValidateResult{
			Path:  ref,
			Valid: false,
			Errors: []contract.ValidationError{
				{Path: "", Code: lockCode(lerr), Message: lerr.Error()},
			},
		}, nil
	}

	// Raw YAML for structural validation. Unreadable is fatal here: unlike the
	// rendering path, validation cannot report a verdict on a document it never saw.
	rawYAML, err := bundle.Raw()
	if err != nil {
		return nil, err
	}

	logging.LoggerFromContext(ctx).Debug("running validation", "ref", ref)
	var resolver validation.BundleResolver
	if s.BundleStore != nil {
		resolver = s.PolicyResolver(ref)
	}
	result := validation.ValidateWithResolver(ctx, bundle.Contract, rawYAML, bundle.FS, resolver)
	logging.LoggerFromContext(ctx).Debug("validation complete", "valid", result.IsValid(), "errors", len(result.Errors), "warnings", len(result.Warnings))

	errors := result.Errors
	valid := result.IsValid()

	// Opt-in readiness gate. Time-dependent, so only when explicitly requested.
	if opts.Readiness {
		if eval := readiness.Evaluate(bundle.Contract.Readiness, timeNow()); eval != nil && !eval.Passing {
			state := "score below gate"
			if eval.Expired {
				state = "assessment expired"
			}
			errors = append(errors, contract.ValidationError{
				Path: "readiness",
				Code: "READINESS_GATE_UNMET",
				Message: fmt.Sprintf("%s: score %d, minScore %d (%d done, %d partial, %d not-done, %d deferred)",
					state, eval.Score, eval.MinScore, eval.DoneCount, eval.PartialCount, eval.NotDoneCount, eval.DeferredCount),
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

// PolicyResolver returns the resolver layer 3 uses to follow a policies[].ref
// out of a contract loaded from root.
//
// It is built per root rather than once per service because a ref's meaning
// depends on who declared it: "./platform-policy" in a bundle under /a and the
// same text in a bundle under /b name two different directories, and neither
// names one when the contract came out of a registry. root fixes where the ROOT
// contract's own refs resolve from; every deeper hop carries its own base.
func (s *Service) PolicyResolver(root string) validation.BundleResolver {
	return &bundleResolverAdapter{svc: s, base: rootBase(root)}
}

// bundleResolverAdapter adapts *Service to [validation.OriginBundleResolver].
//
// Lock verification is root-level only: verifyLockIfPresent (called above) rebuilds
// and compares the root's full transitive dependency + reference closure against
// pacto.lock. Transitive references resolved here for policy validation are part of
// that already-verified closure, so this adapter deliberately does NOT re-verify a
// lock per resolved reference.
type bundleResolverAdapter struct {
	svc *Service
	// base is where the ROOT contract's own refs resolve from -- its directory,
	// or [graph.OCIBase] when the root itself came from a registry.
	base string
}

// Asserted, because the downgrade is silent: policy resolution accepts the
// narrower [validation.BundleResolver] and falls back to resolving every ref
// from the working directory, so a signature that drifts out of the wider port
// would take the fail-closed rule with it and still compile.
var _ validation.OriginBundleResolver = (*bundleResolverAdapter)(nil)

// RootBase implements [validation.OriginBundleResolver].
func (a *bundleResolverAdapter) RootBase() string { return a.base }

// ResolveBundle implements [validation.BundleResolver] by resolving ref as if
// the ROOT had declared it. Policy resolution never takes this path -- it
// prefers ResolveBundleFrom -- but the narrower port is part of the interface
// this adapter satisfies, so it keeps the one meaning it can express.
func (a *bundleResolverAdapter) ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	b, _, err := a.ResolveBundleFrom(ctx, a.base, ref)
	return b, err
}

// ResolveBundleFrom implements [validation.OriginBundleResolver]. It goes
// through depLocalDir rather than [Service.resolveBundle] for the whole reason
// this port is wider than the other one: resolveBundle reads a local ref from
// the process working directory, which both loses the declarer's directory and
// lets a contract fetched from a registry pick a local directory for Pacto to
// read a policy schema out of.
func (a *bundleResolverAdapter) ResolveBundleFrom(ctx context.Context, base, ref string) (*contract.Bundle, string, error) {
	parsed := graph.ParseDependencyRef(ref)
	if parsed.IsLocal() {
		dir, err := depLocalDir(parsed.Location, base)
		if err != nil {
			return nil, "", err
		}
		logging.LoggerFromContext(ctx).Debug("resolving local policy reference", "ref", ref, "base", base, "dir", dir)
		b, err := loadLocalBundle(dir)
		if err != nil {
			return nil, "", err
		}
		return b, dir, nil
	}
	b, err := a.svc.resolveOCIBundle(ctx, parsed.Location)
	if err != nil {
		return nil, "", err
	}
	return b, graph.OCIBase, nil
}
