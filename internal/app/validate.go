package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/readiness"
	"github.com/trianalab/pacto/v4/pkg/validation"
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
	// Closure, when true, also checks every contract in the dependency closure
	// against the policies the ROOT resolves. Off by default: it fetches every
	// dependency, and a dependency's own policies stay its owner's business.
	Closure bool
}

// ValidateResult holds the result of the validate command.
type ValidateResult struct {
	Path     string                       `json:"path"`
	Valid    bool                         `json:"valid"`
	Errors   []contract.ValidationError   `json:"errors"`
	Warnings []contract.ValidationWarning `json:"warnings"`
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
	warnings := result.Warnings
	valid := result.IsValid()

	// Opt-in readiness gate. Time-dependent, so only when explicitly requested.
	if opts.Readiness {
		if eval := readiness.Evaluate(bundle.Contract.Readiness, s.env().now()); eval != nil && !eval.Passing {
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

	if opts.Closure {
		ce, cw := s.closureFindings(ctx, ref, bundle, resolver)
		if len(ce) > 0 {
			errors = append(errors, ce...)
			valid = false
		}
		warnings = append(warnings, cw...)
	}

	return &ValidateResult{
		Path:     ref,
		Valid:    valid,
		Errors:   errors,
		Warnings: warnings,
	}, nil
}

// closureFindings checks every contract in the root's dependency closure against
// the policies the root resolves. It fails closed: a dependency that cannot be
// resolved, or whose contract cannot be read, is an error. External
// dependencies carry no contract and are skipped. It also warns about each
// deprecated interface of a provider the root depends on directly.
//
// A root whose own policies fail to resolve is already invalid, so the closure
// is then checked for resolution failures only.
//
// ponytail: the closure is resolved again after verifyLockIfPresent resolved it
// for the lock check, so a tag re-pushed between the two reads is not caught;
// a lock-pinned fetcher closes that window if it matters.
func (s *Service) closureFindings(ctx context.Context, ref string, bundle *contract.Bundle, resolver validation.BundleResolver) ([]contract.ValidationError, []contract.ValidationWarning) {
	policies, pr := validation.ResolvePoliciesWithResolver(ctx, bundle.Contract, bundle.FS, resolver)
	if !pr.IsValid() {
		policies = nil
	}
	res := graph.ResolveWithOptions(ctx, bundle.Contract, s.newDepFetcher(ref), graph.ResolveOptions{})

	var errs []contract.ValidationError
	// The resolver keys nodes on the declaring base, so one service reached
	// through two directories (a diamond) is two nodes. Check it once, keyed on
	// content too: a local copy with edits and the published bundle can share a
	// name and version.
	checked := map[string]bool{}
	// A shared edge carries no contract, so the root's direct providers are
	// looked up among the nodes the walk resolved.
	// ponytail: keyed on name, version and ref, so two different bundles under one
	// relative ref in different directories collapse; key on the resolver's base if
	// that ever matters.
	contracts := map[string]*contract.Contract{}
	nodeID := func(n *graph.Node) string { return n.Name + "@" + n.Version + "@" + n.Ref }
	var walk func(n *graph.Node)
	walk = func(n *graph.Node) {
		for _, e := range n.Dependencies {
			switch {
			case e.External:
			case e.Error != "":
				path, msg := "dependencies", fmt.Sprintf("dependency %s: %s", e.Ref, e.Error)
				if n != res.Root {
					// The ref is relative to the declaring dependency, so name it.
					path = fmt.Sprintf("dependencies[%s@%s]", n.Name, n.Version)
					msg = fmt.Sprintf("dependency %s of %s@%s (%s): %s", e.Ref, n.Name, n.Version, n.Ref, e.Error)
				}
				errs = append(errs, contract.ValidationError{Path: path, Code: "CLOSURE_DEPENDENCY_UNRESOLVED", Message: msg})
			case e.Node == nil || e.Shared:
				// A shared edge points at a node checked where it was first resolved.
			default:
				err := fs.ErrNotExist
				var data []byte
				if e.Node.FS != nil {
					data, err = fs.ReadFile(e.Node.FS, DefaultContractPath)
				}
				contracts[nodeID(e.Node)] = e.Node.Contract
				sum := sha256.Sum256(data)
				if id := e.Node.Name + "@" + e.Node.Version + "@" + hex.EncodeToString(sum[:]); !checked[id] {
					checked[id] = true
					errs = append(errs, checkClosureNode(e.Node, data, err, policies)...)
				}
				walk(e.Node)
			}
		}
	}
	walk(res.Root)

	var warns []contract.ValidationWarning
	for i, e := range res.Root.Dependencies {
		if e.Node != nil && contracts[nodeID(e.Node)] != nil {
			warns = append(warns, validation.DeprecatedInterfaceWarnings(fmt.Sprintf("dependencies[%d]", i), contracts[nodeID(e.Node)], s.env().now())...)
		}
	}
	return errs, warns
}

// checkClosureNode checks one resolved dependency's contract data, read with
// readErr, against the root's policies, naming the dependency in every finding.
func checkClosureNode(n *graph.Node, data []byte, readErr error, policies []validation.ResolvedPolicy) []contract.ValidationError {
	path := fmt.Sprintf("dependencies[%s@%s]", n.Name, n.Version)
	if readErr != nil {
		return []contract.ValidationError{{
			Path:    path,
			Code:    "CLOSURE_DEPENDENCY_UNRESOLVED",
			Message: fmt.Sprintf("dependency %s: cannot read its contract: %v", n.Ref, readErr),
		}}
	}
	var errs []contract.ValidationError
	for _, v := range validation.EnforcePolicies(data, policies).Errors {
		errs = append(errs, contract.ValidationError{
			Path:    path,
			Code:    "CLOSURE_POLICY_VIOLATION",
			Message: fmt.Sprintf("dependency %s@%s (%s): %s", n.Name, n.Version, n.Ref, v.Message),
		})
	}
	return errs
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

// ValidationReport runs Validate for the bundle at dir and returns the verdict in
// the shape the MCP pacto_check port consumes, so the tool and `pacto validate`
// share one validation path (lockfile verification included).
func (s *Service) ValidationReport(ctx context.Context, dir string) (validation.ValidationResult, error) {
	r, err := s.Validate(ctx, ValidateOptions{Path: dir})
	if err != nil {
		return validation.ValidationResult{}, err
	}
	return validation.ValidationResult{Errors: r.Errors, Warnings: r.Warnings}, nil
}
