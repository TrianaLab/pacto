package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/override"
)

// ErrArtifactAlreadyExists is returned when a push is attempted for a
// reference that already exists in the registry and --force was not specified.
var ErrArtifactAlreadyExists = errors.New("artifact already exists")

// hasTagOrDigest reports whether an OCI reference includes a tag or digest.
func hasTagOrDigest(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	afterSlash := ref
	if i := strings.LastIndex(ref, "/"); i != -1 {
		afterSlash = ref[i+1:]
	}
	return strings.Contains(afterSlash, ":")
}

// PushOptions holds options for the push command.
type PushOptions struct {
	Ref       string
	Path      string
	Force     bool
	Overrides override.Overrides
}

// PushResult holds the result of the push command.
type PushResult struct {
	Ref     string
	Digest  string
	Name    string
	Version string
}

// Push validates a contract bundle, builds an OCI image, and pushes it to a registry.
func (s *Service) Push(ctx context.Context, opts PushOptions) (*PushResult, error) {
	if err := s.requireBundleStore(); err != nil {
		return nil, err
	}

	parsed := graph.ParseDependencyRef(opts.Ref)
	if !parsed.IsOCI() {
		return nil, fmt.Errorf("push requires an OCI reference (oci://...): got %q", opts.Ref)
	}

	path := defaultPath(opts.Path)

	c, _, bundleFS, err := loadAndValidateLocal(path, opts.Overrides)
	if err != nil {
		return nil, err
	}

	if err := rejectLocalDeps(c); err != nil {
		return nil, err
	}

	if err := rejectLocalChart(c); err != nil {
		return nil, err
	}

	if err := rejectLocalRefs(c); err != nil {
		return nil, err
	}

	ref := parsed.Location
	if !hasTagOrDigest(ref) {
		ref = ref + ":" + c.Service.Version
	}

	if !opts.Force {
		if _, err := s.BundleStore.Resolve(ctx, ref); err == nil {
			slog.Debug("artifact already exists, skipping push", "ref", ref)
			return nil, fmt.Errorf("%w: %s (use --force to overwrite)", ErrArtifactAlreadyExists, ref)
		} else if !isNotFound(err) {
			return nil, err
		}
		slog.Debug("artifact not found, proceeding with push", "ref", ref)
	} else {
		slog.Debug("force flag set, skipping existence check", "ref", ref)
	}

	bundle := &contract.Bundle{Contract: c, FS: bundleFS}

	slog.Debug("pushing artifact", "ref", ref)
	digest, err := s.BundleStore.Push(ctx, ref, bundle)
	if err != nil {
		return nil, err
	}

	return &PushResult{
		Ref:     ref,
		Digest:  digest,
		Name:    c.Service.Name,
		Version: c.Service.Version,
	}, nil
}

// isNotFound reports whether err indicates the artifact was not found.
func isNotFound(err error) bool {
	var notFound *oci.ArtifactNotFoundError
	return errors.As(err, &notFound)
}

// rejectLocalChart returns an error if the chart uses a local reference.
func rejectLocalChart(c *contract.Contract) error {
	if c.Service.Chart == nil {
		return nil
	}
	parsed := graph.ParseDependencyRef(c.Service.Chart.Ref)
	if parsed.IsLocal() {
		return fmt.Errorf("local chart reference detected: %s\nLocal chart references are not allowed when publishing. Use an OCI reference (oci://...)", c.Service.Chart.Ref)
	}
	return nil
}

// rejectLocalDeps returns an error if any dependency uses a local reference.
func rejectLocalDeps(c *contract.Contract) error {
	for _, dep := range c.Dependencies {
		if graph.ParseDependencyRef(dep.Ref).IsLocal() {
			return fmt.Errorf("local dependency detected: %s\nLocal dependencies are not allowed when publishing. All dependencies must use OCI references (oci://...)", dep.Ref)
		}
	}
	return nil
}

// rejectLocalRefs returns an error if configuration.ref or policy.ref uses a local reference.
func rejectLocalRefs(c *contract.Contract) error {
	if c.Configuration != nil && c.Configuration.Ref != "" {
		if graph.ParseDependencyRef(c.Configuration.Ref).IsLocal() {
			return fmt.Errorf("local configuration ref detected: %s\nLocal references are not allowed when publishing. Use an OCI reference (oci://...)", c.Configuration.Ref)
		}
	}
	if c.Policy != nil && c.Policy.Ref != "" {
		if graph.ParseDependencyRef(c.Policy.Ref).IsLocal() {
			return fmt.Errorf("local policy ref detected: %s\nLocal references are not allowed when publishing. Use an OCI reference (oci://...)", c.Policy.Ref)
		}
	}
	return nil
}
