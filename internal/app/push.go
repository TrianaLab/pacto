package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/override"
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
	Ref     string `json:"ref"`
	Digest  string `json:"digest"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Unchanged is true when the registry already held exactly this bundle.
	Unchanged bool `json:"unchanged,omitempty"`
}

// Push validates a contract bundle, builds an OCI artifact, and pushes it to a registry.
func (s *Service) Push(ctx context.Context, opts PushOptions) (*PushResult, error) {
	if err := s.requireBundleStore(); err != nil {
		return nil, err
	}

	parsed := graph.ParseDependencyRef(opts.Ref)
	if !parsed.IsOCI() {
		return nil, fmt.Errorf("push requires an OCI reference (oci://...): got %q", opts.Ref)
	}

	path := defaultPath(opts.Path)

	c, _, bundleFS, err := loadAndValidateFull(ctx, path, opts.Overrides, s.BundleStore)
	if err != nil {
		return nil, err
	}

	if err := rejectLocalDeps(c); err != nil {
		return nil, err
	}

	if err := rejectLocalRefs(c); err != nil {
		return nil, err
	}

	if err := s.verifyLockIfPresent(ctx, path, &contract.Bundle{Contract: c, FS: bundleFS}); err != nil {
		return nil, err
	}

	ref := parsed.Location
	if !hasTagOrDigest(ref) {
		ref = ref + ":" + c.Service.Version
	}

	bundle := &contract.Bundle{Contract: c, FS: bundleFS}
	result := &PushResult{Ref: ref, Name: c.Service.Name, Version: c.Service.Version}

	// The same content again is a successful no-op; different content under
	// an existing reference is refused unless --force.
	if !opts.Force {
		remote, err := s.BundleStore.Resolve(ctx, ref)
		switch {
		case err == nil:
			// A bundle that cannot be packed has no digest and reads as a
			// conflict below: refused, never overwritten.
			local, _ := oci.BundleDigest(bundle)
			if remote == local {
				logging.LoggerFromContext(ctx).Debug("identical artifact already published", "ref", ref, "digest", remote)
				result.Digest, result.Unchanged = remote, true
				return result, nil
			}
			return nil, fmt.Errorf("%w: %s holds %s, not this bundle's %s (use --force to overwrite)", ErrArtifactAlreadyExists, ref, remote, local)
		case !isNotFound(err):
			return nil, err
		}
		logging.LoggerFromContext(ctx).Debug("artifact not found, proceeding with push", "ref", ref)
	} else {
		logging.LoggerFromContext(ctx).Debug("force flag set, skipping existence check", "ref", ref)
	}

	logging.LoggerFromContext(ctx).Debug("pushing artifact", "ref", ref)
	digest, err := s.BundleStore.Push(ctx, ref, bundle)
	if err != nil {
		return nil, err
	}

	result.Digest = digest
	return result, nil
}

// isNotFound reports whether err indicates the artifact was not found.
func isNotFound(err error) bool {
	var notFound *oci.ArtifactNotFoundError
	return errors.As(err, &notFound)
}

// rejectLocalDeps returns an error if any dependency uses a local reference.
func rejectLocalDeps(c *contract.Contract) error {
	for _, dep := range c.Dependencies {
		if dep.Ref != "" && graph.ParseDependencyRef(dep.Ref).IsLocal() {
			return fmt.Errorf("local dependency detected: %s\nLocal dependencies are not allowed when publishing. All dependencies must use OCI references (oci://...)", dep.Ref)
		}
	}
	return nil
}

// rejectLocalRefs returns an error if any config or policy ref uses a local reference.
func rejectLocalRefs(c *contract.Contract) error {
	for _, cfg := range c.Configurations {
		if cfg.Ref != "" && graph.ParseDependencyRef(cfg.Ref).IsLocal() {
			return fmt.Errorf("local config ref detected: %s (configurations[%q])\nLocal references are not allowed when publishing. Use an OCI reference (oci://...)", cfg.Ref, cfg.Name)
		}
	}
	for i, pol := range c.Policies {
		if pol.Ref != "" && graph.ParseDependencyRef(pol.Ref).IsLocal() {
			return fmt.Errorf("local policy ref detected: %s (policies[%d])\nLocal references are not allowed when publishing. Use an OCI reference (oci://...)", pol.Ref, i)
		}
	}
	return nil
}
