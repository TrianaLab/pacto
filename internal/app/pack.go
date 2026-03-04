package app

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/internal/oci"
)

// PackOptions holds options for the pack command.
type PackOptions struct {
	Path   string
	Output string
}

// PackResult holds the result of the pack command.
type PackResult struct {
	Output  string
	Name    string
	Version string
}

// Pack validates a contract bundle and produces a tar.gz archive.
func (s *Service) Pack(_ context.Context, opts PackOptions) (*PackResult, error) {
	path := defaultPath(opts.Path)

	c, _, bundleFS, err := loadAndValidateLocal(path)
	if err != nil {
		return nil, err
	}

	data, err := oci.BundleToTarGz(bundleFS)
	if err != nil {
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}

	output := opts.Output
	if output == "" {
		output = fmt.Sprintf("%s-%s.tar.gz", c.Service.Name, c.Service.Version)
	}

	if err := writeFileFn(output, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", output, err)
	}

	return &PackResult{
		Output:  output,
		Name:    c.Service.Name,
		Version: c.Service.Version,
	}, nil
}
