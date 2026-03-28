package oci

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/trianalab/pacto/pkg/contract"
)

// ResolveMode controls whether remote fetching is allowed.
type ResolveMode int

const (
	// LocalOnly restricts resolution to the local disk cache.
	LocalOnly ResolveMode = iota
	// RemoteAllowed permits fetching from the OCI registry on cache miss.
	RemoteAllowed
)

// InvalidRefError indicates the OCI reference could not be parsed.
type InvalidRefError struct {
	Ref string
	Err error
}

func (e *InvalidRefError) Error() string {
	return fmt.Sprintf("invalid OCI reference %q: %v", e.Ref, e.Err)
}

func (e *InvalidRefError) Unwrap() error { return e.Err }

// InvalidBundleError indicates the pulled artifact is not a valid Pacto bundle.
type InvalidBundleError struct {
	Ref string
	Err error
}

func (e *InvalidBundleError) Error() string {
	return fmt.Sprintf("artifact at %s is not a valid Pacto bundle: %v", e.Ref, e.Err)
}

func (e *InvalidBundleError) Unwrap() error { return e.Err }

// NoMatchingVersionError indicates no tags satisfy the compatibility constraint.
type NoMatchingVersionError struct {
	Ref        string
	Constraint string
	Err        error
}

func (e *NoMatchingVersionError) Error() string {
	return fmt.Sprintf("no versions of %s match constraint %q: %v", e.Ref, e.Constraint, e.Err)
}

func (e *NoMatchingVersionError) Unwrap() error { return e.Err }

// Resolver provides lazy, on-demand resolution of Pacto bundles from OCI
// references. It checks the local disk cache first and optionally falls back
// to pulling from the remote registry.
type Resolver struct {
	store BundleStore
}

// NewResolver creates a Resolver backed by the given BundleStore.
// The store should be a CachedStore so that successful pulls persist to disk.
func NewResolver(store BundleStore) *Resolver {
	return &Resolver{store: store}
}

// Resolve fetches a Pacto bundle for the given OCI reference.
//
// In LocalOnly mode, only the disk cache is checked.
// In RemoteAllowed mode, a cache miss triggers a pull from the registry.
//
// Errors are typed:
//   - *InvalidRefError: ref cannot be parsed
//   - *ArtifactNotFoundError: not in registry (404)
//   - *AuthenticationError: credentials rejected (401/403)
//   - *RegistryUnreachableError: network/DNS failure
//   - *InvalidBundleError: pulled artifact is not a valid Pacto bundle
func (r *Resolver) Resolve(ctx context.Context, ref string, mode ResolveMode) (*contract.Bundle, error) {
	return r.ResolveConstrained(ctx, ref, "", mode)
}

// ResolveConstrained fetches a Pacto bundle, resolving untagged refs using
// the compatibility constraint to select the best matching version from the
// OCI registry's available tags.
//
// If the ref already has an explicit tag or digest, the constraint is ignored.
// If the ref is untagged and constraint is empty, the highest semver tag is used.
//
// Additional error type:
//   - *NoMatchingVersionError: no tags satisfy the constraint
func (r *Resolver) ResolveConstrained(ctx context.Context, ref, constraint string, mode ResolveMode) (*contract.Bundle, error) {
	ref = strings.TrimPrefix(ref, "oci://")

	if mode == LocalOnly {
		return r.resolveLocal(ctx, ref)
	}

	// For untagged refs in remote mode, resolve the best tag first.
	if !HasExplicitTag(ref) {
		resolved, err := ResolveRef(ctx, r.store, ref, constraint)
		if err != nil {
			if constraint != "" {
				return nil, &NoMatchingVersionError{Ref: ref, Constraint: constraint, Err: err}
			}
			return nil, &ArtifactNotFoundError{Ref: ref, Err: err}
		}
		ref = resolved
	}

	return r.resolveWithFetch(ctx, ref)
}

// ListVersions returns all semver tags available for the given OCI repo reference.
// The ref should be untagged (e.g. "ghcr.io/org/svc-pacto"). Non-semver tags are
// excluded. Results are sorted descending (latest first).
func (r *Resolver) ListVersions(ctx context.Context, ref string) ([]string, error) {
	ref = strings.TrimPrefix(ref, "oci://")
	tags, err := r.store.ListTags(ctx, ref)
	if err != nil {
		return nil, err
	}
	return FilterSemverTags(tags), nil
}

// FetchAllVersions lists all semver tags for the given OCI repo reference and
// pulls each one, ensuring they are cached by the underlying BundleStore.
// Returns the version list sorted descending (latest first).
func (r *Resolver) FetchAllVersions(ctx context.Context, ref string) ([]string, error) {
	ref = strings.TrimPrefix(ref, "oci://")
	tags, err := r.store.ListTags(ctx, ref)
	if err != nil {
		return nil, err
	}
	versions := FilterSemverTags(tags)
	for _, v := range versions {
		// Pull triggers caching in CachedStore. Errors are non-fatal —
		// we still return versions that were successfully listed.
		if _, pullErr := r.store.Pull(ctx, ref+":"+v); pullErr != nil {
			slog.Warn("failed to cache version", "ref", ref+":"+v, "error", pullErr)
		}
	}
	return versions, nil
}

func (r *Resolver) resolveLocal(ctx context.Context, ref string) (*contract.Bundle, error) {
	bundle, err := r.store.Pull(ctx, ref)
	if err != nil {
		return nil, &ArtifactNotFoundError{Ref: ref, Err: fmt.Errorf("not found in local cache")}
	}
	if bundle.Contract == nil {
		return nil, &InvalidBundleError{Ref: ref, Err: fmt.Errorf("bundle has no contract")}
	}
	return bundle, nil
}

func (r *Resolver) resolveWithFetch(ctx context.Context, ref string) (*contract.Bundle, error) {
	bundle, err := r.store.Pull(ctx, ref)
	if err != nil {
		// Classify the error.
		var authErr *AuthenticationError
		var notFoundErr *ArtifactNotFoundError
		var unreachableErr *RegistryUnreachableError
		switch {
		case errors.As(err, &authErr):
			return nil, err
		case errors.As(err, &notFoundErr):
			return nil, err
		case errors.As(err, &unreachableErr):
			return nil, err
		default:
			// Could be a parse error from an invalid ref or an extraction error.
			if strings.Contains(err.Error(), "invalid reference") {
				return nil, &InvalidRefError{Ref: ref, Err: err}
			}
			if strings.Contains(err.Error(), "failed to extract bundle") {
				return nil, &InvalidBundleError{Ref: ref, Err: err}
			}
			return nil, err
		}
	}

	if bundle.Contract == nil {
		return nil, &InvalidBundleError{Ref: ref, Err: fmt.Errorf("bundle has no contract")}
	}
	return bundle, nil
}

// FilterSemverTags returns only valid semver tags, sorted descending (latest first).
func FilterSemverTags(tags []string) []string {
	var versions []*semver.Version
	for _, t := range tags {
		v, err := semver.NewVersion(t)
		if err == nil {
			versions = append(versions, v)
		}
	}
	sort.Sort(sort.Reverse(semver.Collection(versions)))
	var out []string
	for _, v := range versions {
		out = append(out, v.Original())
	}
	return out
}
