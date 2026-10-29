package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/lock"
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

// ValidateResult holds the result of the validate command. Stages lists every
// stage in order; Errors and Warnings hold all of theirs, in the same order.
// File is the contract file the errors' Line and Column point into, and Next
// is the one command to run now.
type ValidateResult struct {
	Path     string                       `json:"path"`
	File     string                       `json:"file"`
	Valid    bool                         `json:"valid"`
	Errors   []contract.ValidationError   `json:"errors"`
	Warnings []contract.ValidationWarning `json:"warnings"`
	Stages   []validation.Stage           `json:"stages"`
	Next     string                       `json:"next"`
}

// stageOrder is every stage of a validate run, in the order it runs them.
var stageOrder = []string{"load", "lock", "structural", "cross-field", "policy", "guarantees", "readiness", "closure"}

// Validate loads a contract, runs validation, and returns the result.
func (s *Service) Validate(ctx context.Context, opts ValidateOptions) (*ValidateResult, error) {
	ref := defaultPath(opts.Path)

	logging.LoggerFromContext(ctx).Debug("resolving contract for validation", "ref", ref)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return newValidateResult(ref, opts, nil, failedStage("load", "PARSE_ERROR", err)), nil
	}
	stages := []validation.Stage{validation.RanStage("load", validation.ValidationResult{})}

	lockPath, local := lockPathFor(ref)
	if lerr := s.verifyLockIfPresent(ctx, ref, bundle); lerr != nil {
		return newValidateResult(ref, opts, nil, append(stages, failedStage("lock", lockCode(lerr), lerr))...), nil
	}
	if _, err := os.Stat(lockPath); local && err == nil {
		stages = append(stages, validation.RanStage("lock", validation.ValidationResult{}))
	} else {
		stages = append(stages, validation.SkippedStages("no "+lock.FileName, "lock")...)
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
	stages = append(stages, validation.ValidateStagesWithResolver(ctx, bundle.Contract, rawYAML, bundle.FS, resolver)...)

	// Opt-in readiness gate. Time-dependent, so only when explicitly requested.
	if opts.Readiness {
		var r validation.ValidationResult
		if eval := readiness.Evaluate(bundle.Contract.Readiness, s.env().now()); eval != nil && !eval.Passing {
			state := "score below gate"
			if eval.Expired {
				state = "assessment expired"
			}
			r.AddError("readiness", "READINESS_GATE_UNMET", fmt.Sprintf("%s: score %d, minScore %d (%d done, %d partial, %d not-done, %d deferred)",
				state, eval.Score, eval.MinScore, eval.DoneCount, eval.PartialCount, eval.NotDoneCount, eval.DeferredCount))
		}
		stages = append(stages, validation.RanStage("readiness", r))
	}

	if opts.Closure {
		ce, cw := s.closureFindings(ctx, ref, bundle, resolver)
		stages = append(stages, validation.RanStage("closure", validation.ValidationResult{Errors: ce, Warnings: cw}))
	}

	stages = append(stages, s.guaranteeStage(ctx, ref, bundle.Contract))

	return newValidateResult(ref, opts, rawYAML, stages...), nil
}

// failedStage is the stage name, failed with err under code.
func failedStage(name, code string, err error) validation.Stage {
	return validation.RanStage(name, validation.ValidationResult{Errors: []contract.ValidationError{{Code: code, Message: err.Error()}}})
}

var (
	// parseLineRe finds the line a YAML decode error names.
	parseLineRe = regexp.MustCompile(`line (\d+):`)
	// fixCommandRe finds the command a lock error tells the reader to run.
	fixCommandRe = regexp.MustCompile("run `(pacto [^`]+)`")
)

// newValidateResult folds stages into a result. It locates each error in raw,
// lists every stage that did not run as skipped, and names in Next the
// narrowest command to run now.
func newValidateResult(ref string, opts ValidateOptions, raw []byte, stages ...validation.Stage) *ValidateResult {
	file := DefaultContractPath
	if !isOCIRef(ref) {
		file = filepath.Join(ref, DefaultContractPath)
	}
	ran := map[string]validation.Stage{}
	failed := ""
	for i := range stages {
		if failed == "" && stages[i].Status == validation.StageFail {
			failed = stages[i].Name
		}
		for j := range stages[i].Errors {
			e := &stages[i].Errors[j]
			e.Line, e.Column = contract.Locate(raw, e.Path)
			if m := parseLineRe.FindStringSubmatch(e.Message); e.Code == "PARSE_ERROR" && m != nil {
				e.Line, _ = strconv.Atoi(m[1])
			}
		}
		// In file order, so the first error, the one Next names, is the same on
		// every run: the schema layer reports its errors in no fixed order.
		slices.SortStableFunc(stages[i].Errors, func(a, b contract.ValidationError) int {
			unlocated := func(e contract.ValidationError) int {
				if e.Line == 0 {
					return 1
				}
				return 0
			}
			return cmp.Or(cmp.Compare(unlocated(a), unlocated(b)), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column),
				cmp.Compare(a.Path, b.Path), cmp.Compare(a.Message, b.Message))
		})
		ran[stages[i].Name] = stages[i]
	}
	asked := map[string]bool{"readiness": opts.Readiness, "closure": opts.Closure}
	ordered := make([]validation.Stage, len(stageOrder))
	for i, name := range stageOrder {
		st, ok := ran[name]
		if ask, optIn := asked[name]; !ok && optIn && !ask {
			st = validation.SkippedStages("not requested (--"+name+")", name)[0]
		} else if !ok {
			st = validation.SkippedStages(failed+" failed", name)[0]
		}
		ordered[i] = st
	}

	r := validation.Merged(ordered)
	res := &ValidateResult{Path: ref, File: file, Valid: r.IsValid(), Errors: r.Errors, Warnings: r.Warnings, Stages: ordered}
	res.Next = "pacto impact PUBLISHED_REF " + ref
	if !res.Valid {
		res.Next = nextFix(res, rerunCommand(ref, opts))
	}
	return res
}

// nextFix is what to do about the first error: the command a lock error names,
// or where to fix the contract and the command that checks it again.
func nextFix(r *ValidateResult, rerun string) string {
	e := r.Errors[0]
	if m := fixCommandRe.FindStringSubmatch(e.Message); strings.HasPrefix(e.Code, "LOCK_") && m != nil {
		return m[1] + " " + r.Path
	}
	return fmt.Sprintf("fix %s [%s], then run %s", r.Where(e), e.Code, rerun)
}

// Where is the place an error points at: its contract path, and its file
// position when known.
func (r *ValidateResult) Where(e contract.ValidationError) string {
	at := r.File
	if e.Line > 0 {
		at += ":" + strconv.Itoa(e.Line)
	}
	if e.Column > 0 {
		at += ":" + strconv.Itoa(e.Column)
	}
	if e.Path == "" {
		return at
	}
	return e.Path + " at " + at
}

// rerunCommand is the pacto validate command line opts stand for.
//
// ponytail: values are not shell-quoted; a --set value with a space needs
// quoting by hand.
func rerunCommand(ref string, opts ValidateOptions) string {
	args := []string{"pacto", "validate"}
	if opts.Readiness {
		args = append(args, "--readiness")
	}
	if opts.Closure {
		args = append(args, "--closure")
	}
	for _, f := range opts.Overrides.ValueFiles {
		args = append(args, "-f", f)
	}
	for _, kv := range opts.Overrides.SetValues {
		args = append(args, "--set", kv)
	}
	return strings.Join(append(args, ref), " ")
}

// guaranteeStage checks what each direct dependency requires against its
// provider's contract, reporting every unmet guarantee at once. It is skipped
// when no dependency declares requires. An unmet
// guarantee is a confirmed contradiction between two declared contracts:
// GUARANTEE_UNMET, an error on a required dependency and a warning on an
// optional one, which by definition the service runs without. A provider
// Pacto cannot fetch leaves its requirements unknown: GUARANTEE_UNVERIFIED, a
// warning, never a pass. Only dependencies that declare requires are fetched,
// so a contract without them validates offline as before; what a dependency's
// own dependencies require stays its owner's business.
func (s *Service) guaranteeStage(ctx context.Context, ref string, c *contract.Contract) validation.Stage {
	var errs []contract.ValidationError
	var warns []contract.ValidationWarning
	fetcher := s.newDepFetcher(ref)
	checked := false
	for i, dep := range c.Dependencies {
		if len(dep.Requires) == 0 {
			continue
		}
		checked = true
		path := fmt.Sprintf("dependencies[%d].requires", i)
		var b *contract.Bundle
		err := fmt.Errorf("it has no ref, so there is no contract to read")
		if dep.Ref != "" {
			b, err = fetcher.Fetch(ctx, dep)
		}
		if err != nil {
			warns = append(warns, contract.ValidationWarning{Path: path, Code: "GUARANTEE_UNVERIFIED",
				Message: fmt.Sprintf("cannot check what %s requires from %s: %v", c.Service.Name, dep.Name, err)})
			continue
		}
		for _, m := range contract.Unmet(c.Service.Name, dep, b.Contract) {
			if dep.Required {
				errs = append(errs, contract.ValidationError{Path: path, Code: "GUARANTEE_UNMET", Message: m.String()})
			} else {
				warns = append(warns, contract.ValidationWarning{Path: path, Code: "GUARANTEE_UNMET", Message: m.String()})
			}
		}
	}
	if !checked {
		return validation.SkippedStages("no dependency declares requires", "guarantees")[0]
	}
	return validation.RanStage("guarantees", validation.ValidationResult{Errors: errs, Warnings: warns})
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
func (s *Service) ValidationReport(ctx context.Context, dir string) (validation.Report, error) {
	r, err := s.Validate(ctx, ValidateOptions{Path: dir})
	if err != nil {
		return validation.Report{}, err
	}
	return validation.Report{ValidationResult: validation.ValidationResult{Errors: r.Errors, Warnings: r.Warnings}, Stages: r.Stages, Next: r.Next}, nil
}
