package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/pkg/override"
	"github.com/trianalab/pacto/pkg/plugin"
)

// PluginRunner abstracts plugin execution so the app layer does not depend
// on the concrete subprocess implementation.
type PluginRunner interface {
	Run(ctx context.Context, name string, req plugin.GenerateRequest) (*plugin.GenerateResponse, error)
}

// GenerateOptions holds options for the generate command.
type GenerateOptions struct {
	Path      string
	OutputDir string
	Plugin    string
	Options   map[string]any
	Overrides override.Overrides
}

// GenerateResult holds the result of the generate command.
type GenerateResult struct {
	Plugin     string `json:"plugin"`
	OutputDir  string `json:"outputDir"`
	FilesCount int    `json:"filesCount"`
	Message    string `json:"message,omitempty"`
}

// Generate invokes a plugin to produce artifacts from a contract.
func (s *Service) Generate(ctx context.Context, opts GenerateOptions) (*GenerateResult, error) {
	if s.PluginRunner == nil {
		return nil, fmt.Errorf("plugin runner not configured")
	}

	ref := defaultPath(opts.Path)

	slog.Debug("resolving contract for generation", "ref", ref, "plugin", opts.Plugin)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return nil, err
	}

	slog.Debug("preparing bundle directory")
	bundleDir, cleanup, err := prepareBundleDir(ref, bundle.FS)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = opts.Plugin + "-output"
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	slog.Debug("invoking plugin", "plugin", opts.Plugin, "bundleDir", bundleDir, "outputDir", outputDir)
	resp, err := s.PluginRunner.Run(ctx, opts.Plugin, plugin.GenerateRequest{
		ProtocolVersion: plugin.ProtocolVersion,
		Contract:        bundle.Contract,
		BundleDir:       bundleDir,
		OutputDir:       outputDir,
		Options:         opts.Options,
	})
	if err != nil {
		return nil, err
	}

	absOutput, err := absPathFn(outputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output directory: %w", err)
	}
	for _, f := range resp.Files {
		outPath := filepath.Join(absOutput, f.Path)
		if rel, relErr := filepath.Rel(absOutput, outPath); relErr != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("plugin file path %q escapes output directory", f.Path)
		}
		if strings.Contains(f.Path, "..") {
			return nil, fmt.Errorf("plugin file path %q contains path traversal", f.Path)
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory for %s: %w", f.Path, err)
		}
		if err := writeFileFn(outPath, []byte(f.Content), 0644); err != nil {
			return nil, fmt.Errorf("failed to write %s: %w", f.Path, err)
		}
	}

	slog.Debug("plugin execution complete", "plugin", opts.Plugin, "files", len(resp.Files))
	return &GenerateResult{
		Plugin:     opts.Plugin,
		OutputDir:  outputDir,
		FilesCount: len(resp.Files),
		Message:    resp.Message,
	}, nil
}

// prepareBundleDir returns a directory path containing the bundle files.
// For local directories it returns ref directly. For OCI bundles it writes
// the in-memory FS to a temp directory.
func prepareBundleDir(ref string, bundleFS fs.FS) (dir string, cleanup func(), err error) {
	if !isOCIRef(ref) {
		return ref, nil, nil
	}

	tmpDir, err := mkdirTempFn("", "pacto-bundle-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	if err := extractBundleFS(bundleFS, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("failed to extract bundle: %w", err)
	}

	return tmpDir, func() { _ = os.RemoveAll(tmpDir) }, nil
}
