package app

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/oci"
	"github.com/trianalab/pacto/pkg/override"
	"github.com/trianalab/pacto/pkg/validation"
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
// returns a Bundle with Contract, RawYAML, and FS populated.
func loadLocalBundle(dir string) (*contract.Bundle, error) {
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

	return &contract.Bundle{
		Contract: c,
		RawYAML:  rawYAML,
		FS:       os.DirFS(bundleDir),
	}, nil
}

// resolveBundle loads a contract bundle from either a local directory or an OCI
// reference (prefixed with "oci://"). For local directories it reads pacto.yaml
// from disk and uses the directory as the bundle FS. For OCI references it
// delegates to the configured BundleStore.
func (s *Service) resolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	parsed := graph.ParseDependencyRef(ref)
	if parsed.IsOCI() {
		slog.Debug("resolving OCI bundle", "ref", parsed.Location)
		if err := s.requireBundleStore(); err != nil {
			return nil, err
		}
		location, err := oci.ResolveRef(ctx, s.BundleStore, parsed.Location, "")
		if err != nil {
			return nil, err
		}
		return s.BundleStore.Pull(ctx, location)
	}

	slog.Debug("loading local bundle", "path", parsed.Location)
	return loadLocalBundle(parsed.Location)
}

// resolveBundleWithOverrides loads a bundle and applies overrides to it.
func (s *Service) resolveBundleWithOverrides(ctx context.Context, ref string, overrides override.Overrides) (*contract.Bundle, error) {
	bundle, err := s.resolveBundle(ctx, ref)
	if err != nil {
		return nil, err
	}
	return applyOverrides(bundle, overrides)
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
func loadAndValidateLocal(dir string, overrides override.Overrides) (*contract.Contract, []byte, fs.FS, error) {
	slog.Debug("loading and validating local bundle", "dir", dir)
	bundle, err := loadLocalBundle(dir)
	if err != nil {
		return nil, nil, nil, err
	}

	bundle, err = applyOverrides(bundle, overrides)
	if err != nil {
		return nil, nil, nil, err
	}

	result := validation.Validate(bundle.Contract, bundle.RawYAML, bundle.FS)
	if !result.IsValid() {
		slog.Debug("local validation failed", "errors", len(result.Errors))
		return nil, nil, nil, fmt.Errorf("contract validation failed with %d error(s)", len(result.Errors))
	}

	slog.Debug("local validation passed", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)
	return bundle.Contract, bundle.RawYAML, bundle.FS, nil
}

// loadAndValidateFull reads a local contract directory, parses pacto.yaml,
// validates it with full remote ref resolution (policies and configs), and
// returns the parsed contract and bundle FS. Used by push to enforce remote
// policies before publishing.
func loadAndValidateFull(ctx context.Context, dir string, overrides override.Overrides, store oci.BundleStore) (*contract.Contract, []byte, fs.FS, error) {
	slog.Debug("loading and validating local bundle with remote resolution", "dir", dir)
	bundle, err := loadLocalBundle(dir)
	if err != nil {
		return nil, nil, nil, err
	}

	bundle, err = applyOverrides(bundle, overrides)
	if err != nil {
		return nil, nil, nil, err
	}

	var resolver validation.BundleResolver
	if store != nil {
		resolver = &bundleResolverAdapter{svc: &Service{BundleStore: store}}
	}
	result := validation.ValidateWithResolver(ctx, bundle.Contract, bundle.RawYAML, bundle.FS, resolver)
	if !result.IsValid() {
		slog.Debug("validation failed", "errors", len(result.Errors))
		return nil, nil, nil, fmt.Errorf("contract validation failed with %d error(s)", len(result.Errors))
	}

	slog.Debug("validation passed", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)
	return bundle.Contract, bundle.RawYAML, bundle.FS, nil
}

// isOCIRef reports whether ref uses the oci:// scheme.
func isOCIRef(ref string) bool {
	return graph.ParseDependencyRef(ref).IsOCI()
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
