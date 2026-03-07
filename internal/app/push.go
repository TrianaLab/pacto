package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/trianalab/pacto/internal/graph"
	"github.com/trianalab/pacto/pkg/contract"
)

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
	Ref  string
	Path string
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

	path := defaultPath(opts.Path)

	c, _, bundleFS, err := loadAndValidateLocal(path)
	if err != nil {
		return nil, err
	}

	if err := rejectLocalDeps(c); err != nil {
		return nil, err
	}

	ref := opts.Ref
	if !hasTagOrDigest(ref) {
		ref = ref + ":" + c.Service.Version
	}

	bundle := &contract.Bundle{Contract: c, FS: bundleFS}

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

// rejectLocalDeps returns an error if any dependency uses a local reference.
func rejectLocalDeps(c *contract.Contract) error {
	for _, dep := range c.Dependencies {
		if graph.ParseDependencyRef(dep.Ref).IsLocal() {
			return fmt.Errorf("local dependency detected: %s\nLocal dependencies are not allowed when publishing. All dependencies must use OCI references (oci://...)", dep.Ref)
		}
	}
	return nil
}
