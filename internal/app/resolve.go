package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// DefaultContractPath is the default filename looked up when no path is given.
const DefaultContractPath = "pacto.yaml"

// defaultPath returns the given path if non-empty, otherwise "." (current directory).
func defaultPath(path string) string {
	if path == "" {
		return "."
	}
	return path
}

// resolveLocalPath validates that dir is a directory containing pacto.yaml
// and returns the full file path and the bundle directory.
func resolveLocalPath(dir string) (filePath, bundleDir string, err error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", "", fmt.Errorf("failed to access %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%s is not a directory", dir)
	}
	filePath = filepath.Join(dir, DefaultContractPath)
	if _, err := os.Stat(filePath); err != nil {
		return "", "", fmt.Errorf("no pacto.yaml found in %s", dir)
	}
	return filePath, dir, nil
}

// loadLocalBundle reads a local contract directory, parses pacto.yaml, and
// returns a completed Bundle with Contract, RawYAML, FS (serving the inferred
// record) and Inferred populated. The record names no commit: its callers hash the bundle without
// it or read its fields.
func loadLocalBundle(dir string) (*contract.Bundle, error) {
	return loadLocalBundleWithOverrides(dir, override.Overrides{})
}

// loadLocalBundleWithOverrides reads a local contract directory, applies the
// overrides and then completes the final contract, so what it records as
// inferred always describes the bytes the bundle holds.
// A folder Pacto cannot complete is read as declared: these reads load local
// dependencies, whose inferred record nothing here reports.
func loadLocalBundleWithOverrides(dir string, overrides override.Overrides) (*contract.Bundle, error) {
	b, err := loadLocalBundleNamed(dir, dirBase(dir), overrides, contract.InferredInputs{}, nil)
	if errors.Is(err, errIncomplete) {
		err = nil
	}
	return b, err
}

// loadLocalBundleNamed is loadLocalBundleWithOverrides completing with
// dirName, the folder dir is a staged copy of, recording in as the inferred
// record's inputs and classifying external systems with cat (nil is the
// default catalog).
func loadLocalBundleNamed(dir, dirName string, overrides override.Overrides, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.Bundle, error) {
	bundle, err := readLocalBundle(dir)
	if err != nil {
		return nil, err
	}
	if bundle, err = applyOverrides(bundle, overrides); err != nil {
		return nil, err
	}
	return bundle, completeLocal(bundle, dir, dirName, "", in, cat)
}

// completeLocal completes a bundle read from the folder dir: what it implies
// (pactoVersion, service.name from dirName) and the inferred record. When
// discovery fails the bundle stays as declared, with no record, and the error
// wraps errIncomplete: pack, push and publish refuse it, a read keeps the
// bundle (readIncomplete). repoDir is the repository root ("" finds it). cat
// classifies external systems; nil is the default catalog.
func completeLocal(b *contract.Bundle, dir, dirName, repoDir string, in contract.InferredInputs, cat *externalcatalog.Catalog) error {
	b.Complete(dirName)
	if err := discover.CompleteBundleIn(b, dir, repoDir, in, cat); err != nil {
		return fmt.Errorf("%w %s: %w", errIncomplete, dir, err)
	}
	return nil
}

// errIncomplete marks a local bundle Pacto read but could not complete.
var errIncomplete = errors.New("completing")

// readIncomplete lets a read keep a bundle Pacto could not complete, since
// inference only adds: it warns that the inferred values are unknown.
func readIncomplete(ctx context.Context, err error) error {
	if errors.Is(err, errIncomplete) {
		logging.LoggerFromContext(ctx).Warn(err.Error() + "; its inferred values are unknown, not absent")
		return nil
	}
	return err
}

// localInputs are the inferred record inputs of a local read of dir: this
// CLI and revision, else the commit of dir's git checkout.
func (s *Service) localInputs(dir, revision string) contract.InferredInputs {
	if revision == "" {
		if abs, err := s.env().absPath(dir); err == nil {
			revision = gitRevision(abs)
		}
	}
	return contract.InferredInputs{Commit: revision, Pacto: s.buildVersion()}
}

// dirBase is the name of dir's folder, "." resolved.
func dirBase(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Base(dir) // ponytail: Abs fails only without a working directory
}

// readLocalBundle reads and parses a local contract directory, uncompleted.
func readLocalBundle(dir string) (*contract.Bundle, error) {
	filePath, bundleDir, err := resolveLocalPath(dir)
	if err != nil {
		return nil, err
	}

	rawYAML, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", filePath, err)
	}

	c, err := contract.Parse(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", filePath, err)
	}

	dirFS := os.DirFS(bundleDir)
	matcher, err := ignore.Load(dirFS)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", ignore.IgnoreFileName, err)
	}

	return &contract.Bundle{
		Contract: c,
		RawYAML:  rawYAML,
		FS:       ignore.FS(dirFS, matcher),
	}, nil
}

// resolveBundle loads a contract bundle from either a local directory or an OCI
// reference (prefixed with "oci://"). For local directories it reads pacto.yaml
// from disk and uses the directory as the bundle FS. For OCI references it
// delegates to the configured BundleStore.
func (s *Service) resolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	return s.resolveBundleWithOverrides(ctx, ref, override.Overrides{})
}

// resolveOCIBundle pulls the bundle for an OCI location (a reference with the
// "oci://" scheme already stripped), never touching the local filesystem
// branch.
//
// It is separate from [Service.resolveBundle] so a caller that must NOT be able
// to reach a local directory can say so structurally rather than by guarding
// the dispatching function — see the evidence ingest resolver.
func (s *Service) resolveOCIBundle(ctx context.Context, location string) (*contract.Bundle, error) {
	logging.LoggerFromContext(ctx).Debug("resolving OCI bundle", "ref", location)
	if err := s.requireBundleStore(); err != nil {
		return nil, err
	}
	resolved, err := oci.ResolveRef(ctx, s.BundleStore, location, "")
	if err != nil {
		return nil, err
	}
	return s.BundleStore.Pull(ctx, resolved)
}

// ResolveBundle resolves a bundle reference (local directory or oci:// ref) to
// its parsed contract and file system. Local refs do not require a BundleStore.
func (s *Service) ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	return s.resolveBundle(ctx, ref)
}

// resolveBundleWithOverrides loads a bundle and applies overrides to it.
// A local bundle is completed after the overrides apply.
func (s *Service) resolveBundleWithOverrides(ctx context.Context, ref string, overrides override.Overrides) (*contract.Bundle, error) {
	b, _, err := s.resolveBundleAsWritten(ctx, ref, overrides)
	return b, err
}

// resolveBundleAsWritten is resolveBundleWithOverrides that also returns the
// bundle's files before any override, from the same single read.
func (s *Service) resolveBundleAsWritten(ctx context.Context, ref string, overrides override.Overrides) (*contract.Bundle, fs.FS, error) {
	parsed := graph.ParseDependencyRef(ref)
	var read *contract.Bundle
	var err error
	if parsed.IsOCI() {
		read, err = s.resolveOCIBundle(ctx, parsed.Location)
	} else {
		logging.LoggerFromContext(ctx).Debug("loading local bundle", "path", parsed.Location)
		read, err = readLocalBundle(parsed.Location)
	}
	if err != nil {
		return nil, nil, err
	}
	asWritten := read.FS
	bundle, err := applyOverrides(read, overrides)
	if err != nil {
		return nil, nil, err
	}
	if !parsed.IsOCI() {
		cat, err := s.completionCatalog(ctx)
		if err != nil {
			return nil, nil, err
		}
		// completeLocal fails only with errIncomplete, which a read keeps.
		_ = readIncomplete(ctx, completeLocal(bundle, parsed.Location, dirBase(parsed.Location), "", s.localInputs(parsed.Location, ""), cat))
	}
	return bundle, asWritten, nil
}

// applyOverrides applies value file and --set overrides to a bundle.
// It re-parses the contract from the merged YAML.
func applyOverrides(bundle *contract.Bundle, overrides override.Overrides) (*contract.Bundle, error) {
	if overrides.IsEmpty() {
		return bundle, nil
	}

	rawYAML := bundle.RawYAML
	if rawYAML == nil && bundle.FS != nil {
		var err error
		rawYAML, err = fs.ReadFile(bundle.FS, DefaultContractPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read contract for overrides: %w", err)
		}
	}
	if rawYAML == nil {
		return nil, fmt.Errorf("no raw YAML available to apply overrides")
	}

	merged, err := override.Apply(rawYAML, overrides)
	if err != nil {
		return nil, fmt.Errorf("failed to apply overrides: %w", err)
	}

	c, err := contract.Parse(bytes.NewReader(merged))
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract after overrides: %w", err)
	}

	// Validate overridden YAML against JSON Schema to catch invalid enum values,
	// unknown fields, and type mismatches that Go struct unmarshalling silently accepts.
	if result := validation.ValidateStructuralRaw(merged); !result.IsValid() {
		return nil, fmt.Errorf("overrides produce an invalid contract: %s", result.Errors[0].Message)
	}

	mergedFS, err := copyFSWithReplace(bundle.FS, DefaultContractPath, merged)
	if err != nil {
		return nil, fmt.Errorf("failed to build overridden bundle FS: %w", err)
	}

	return &contract.Bundle{
		Contract: c,
		RawYAML:  merged,
		FS:       mergedFS,
	}, nil
}

// copyFSWithReplace copies all files from src into a fstest.MapFS, replacing
// the file at replaceName with replaceData.
func copyFSWithReplace(src fs.FS, replaceName string, replaceData []byte) (fstest.MapFS, error) {
	m := fstest.MapFS{}
	err := fs.WalkDir(src, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		if d.IsDir() {
			m[path] = &fstest.MapFile{Mode: fs.ModeDir | 0755}
			return nil
		}
		if path == replaceName {
			m[path] = &fstest.MapFile{Data: replaceData, Mode: 0644}
			return nil
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		m[path] = &fstest.MapFile{Data: data, Mode: 0644}
		return nil
	})
	return m, err
}

// loadAndValidateLocal reads a local contract directory, parses pacto.yaml,
// validates it, and returns the parsed contract and bundle FS. This is the
// shared helper for pack and push commands that must validate before proceeding.
func loadAndValidateLocal(ctx context.Context, dir string, overrides override.Overrides, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.Contract, []byte, fs.FS, error) {
	logging.LoggerFromContext(ctx).Debug("loading and validating local bundle", "dir", dir)
	bundle, err := loadLocalBundleNamed(dir, dirBase(dir), overrides, in, cat)
	if err != nil {
		return nil, nil, nil, err
	}

	result := validation.Validate(bundle.Contract, bundle.RawYAML, bundle.FS)
	if !result.IsValid() {
		logging.LoggerFromContext(ctx).Debug("local validation failed", "errors", len(result.Errors))
		return nil, nil, nil, fmt.Errorf("contract validation failed with %d error(s)", len(result.Errors))
	}

	logging.LoggerFromContext(ctx).Debug("local validation passed", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)
	if bundle, err = publishable(bundle, false); err != nil {
		return nil, nil, nil, err
	}
	return bundle.Contract, bundle.RawYAML, bundle.FS, nil
}

// loadAndValidateFull reads a local contract directory, parses pacto.yaml,
// validates it with full remote ref resolution (policies and configs), and
// returns the parsed contract and bundle FS. Used by push to enforce remote
// policies before publishing. in are the inputs the inferred record names.
func loadAndValidateFull(ctx context.Context, dir string, overrides override.Overrides, store oci.BundleStore, in contract.InferredInputs, cat *externalcatalog.Catalog, deriveVersion bool) (*contract.Contract, []byte, fs.FS, error) {
	logging.LoggerFromContext(ctx).Debug("loading and validating local bundle with remote resolution", "dir", dir)
	bundle, err := loadLocalBundleNamed(dir, dirBase(dir), overrides, in, cat)
	if err != nil {
		return nil, nil, nil, err
	}

	var resolver validation.BundleResolver
	if store != nil {
		resolver = (&Service{BundleStore: store}).PolicyResolver(dir)
	}
	result := validation.ValidateWithResolver(ctx, bundle.Contract, bundle.RawYAML, bundle.FS, resolver)
	if !result.IsValid() {
		logging.LoggerFromContext(ctx).Debug("validation failed", "errors", len(result.Errors))
		return nil, nil, nil, fmt.Errorf("contract validation failed with %d error(s)", len(result.Errors))
	}

	logging.LoggerFromContext(ctx).Debug("validation passed", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)
	if bundle, err = publishable(bundle, deriveVersion); err != nil {
		return nil, nil, nil, err
	}
	return bundle.Contract, bundle.RawYAML, bundle.FS, nil
}

// The errors pack, push and publish return for a contract that leaves its
// identity undeclared, so a published bundle never carries an implied value.
var (
	ErrPublishNeedsServiceName    = errors.New(string(finding.CodePublishNeedsServiceName))
	ErrPublishNeedsServiceVersion = errors.New(string(finding.CodePublishNeedsServiceVersion) +
		": service.version is not declared; declare it to publish: `service: {version: 1.0.0}`")
)

// publishable is the bundle pack, push and publish write: it refuses an
// undeclared service.name or service.version and pins an absent pactoVersion
// to contract.Current() in the bundled pacto.yaml, then re-parses the pinned
// bytes to prove the root declares it. deriveVersion lets an undeclared
// service.version through: publish derives it and pins it next.
func publishable(b *contract.Bundle, deriveVersion bool) (*contract.Bundle, error) {
	if b.Contract.Service.Version == "" && !deriveVersion {
		return nil, ErrPublishNeedsServiceVersion
	}
	if b.Contract.Service.Name == "" {
		return nil, fmt.Errorf("%w: service.name is not declared; declare it to publish: `service: {name: <name>}`", ErrPublishNeedsServiceName)
	}
	if inf, ok := b.NameInferred(); ok {
		return nil, fmt.Errorf("%w: service.name is inferred from %s; declare it to publish: `service: {name: %s}`", ErrPublishNeedsServiceName, inf.Source, inf.Value)
	}
	raw, err := contract.PinVersion(b.RawYAML)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(raw, b.RawYAML) {
		return b, nil // pactoVersion already declared
	}
	c, _, err := contract.ParseDocument(raw)
	if err == nil {
		err = contract.RequireDeclared(declaredVersion(c, b.Contract.Service.Version), raw)
	}
	if err != nil || c.PactoVersion != contract.Current() {
		return nil, fmt.Errorf("pinning pactoVersion %s into pacto.yaml: %v", contract.Current(), err)
	}
	fsys, err := copyFSWithReplace(b.FS, DefaultContractPath, raw)
	if err != nil {
		return nil, fmt.Errorf("failed to build the pinned bundle FS: %w", err)
	}
	return &contract.Bundle{Contract: c, RawYAML: raw, FS: fsys}, nil
}

// declaredVersion is c as RequireDeclared reads it when publish will derive
// service.version: an undeclared version stands in as declared, since publish
// pins it and checks the pinned bytes again.
func declaredVersion(c *contract.Contract, version string) *contract.Contract {
	if version != "" {
		return c
	}
	d := *c
	d.Service.Version = "derived"
	return &d
}

// isOCIRef reports whether ref uses the oci:// scheme.
func isOCIRef(ref string) bool {
	return graph.ParseDependencyRef(ref).IsOCI()
}

// rootBase is where the ROOT contract loaded from ref resolves its own relative
// references from: its absolute directory, or [graph.OCIBase] when it came from
// a registry.
//
// One function because one answer. The dependency fetcher, the lock builder's
// reference closure and policy resolution each walk a different closure out of
// the same root, and a root that is "a registry bundle" to one of them and "the
// working directory" to another is how a local reference declared inside a
// remote contract gets honoured by whichever walk spelled the sentinel
// differently. [graph.OCIBase] is never a valid absolute path, so the two cases
// can never be confused.
func rootBase(ref string) string {
	if isOCIRef(ref) {
		return graph.OCIBase
	}
	// Only reachable if the process has no working directory; the relative ref is
	// still the right base to join onto in that case.
	base := ref
	if abs, err := filepath.Abs(ref); err == nil {
		base = abs
	}
	return base
}

// extractBundleFS writes all files from a bundle FS to the given directory.
func extractBundleFS(fsys fs.FS, dir string) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		target := filepath.Join(dir, path)

		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, 0644)
	})
}
