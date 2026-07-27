package app

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/override"
)

// PackOptions holds options for the pack command.
type PackOptions struct {
	Path      string
	Output    string
	Overrides override.Overrides
}

// PackResult holds the result of the pack command.
type PackResult struct {
	Output  string
	Name    string
	Version string
}

// Pack validates a contract bundle and produces a tar.gz archive.
func (s *Service) Pack(ctx context.Context, opts PackOptions) (*PackResult, error) {
	path := defaultPath(opts.Path)

	logging.LoggerFromContext(ctx).Debug("loading and validating local contract", "path", path)
	c, _, bundleFS, err := loadAndValidateLocal(ctx, path, opts.Overrides)
	if err != nil {
		return nil, err
	}

	logging.LoggerFromContext(ctx).Debug("creating tar.gz archive", "name", c.Service.Name, "version", c.Service.Version)
	data, err := oci.BundleToTarGz(bundleFS)
	if err != nil {
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}

	output := opts.Output
	if output == "" {
		output = fmt.Sprintf("%s-%s.tar.gz", c.Service.Name, c.Service.Version)
	}

	logging.LoggerFromContext(ctx).Debug("writing archive", "output", output)
	if err := writeFileFn(output, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", output, err)
	}

	return &PackResult{
		Output:  output,
		Name:    c.Service.Name,
		Version: c.Service.Version,
	}, nil
}
