package oci

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/semver"
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

// Error reports the unparseable reference, prefixed with "invalid OCI reference".
func (e *InvalidRefError) Error() string {
	return fmt.Sprintf("invalid OCI reference %q: %v", e.Ref, e.Err)
}

// Unwrap returns the underlying parse error so errors.Is/As works.
func (e *InvalidRefError) Unwrap() error { return e.Err }

// InvalidBundleError indicates the pulled artifact is not a valid Pacto bundle.
type InvalidBundleError struct {
	Ref string
	Err error
}

// Error reports that the artifact at the ref is not a valid Pacto bundle.
func (e *InvalidBundleError) Error() string {
	return fmt.Sprintf("artifact at %s is not a valid Pacto bundle: %v", e.Ref, e.Err)
}

// Unwrap returns the underlying validation error so errors.Is/As works.
func (e *InvalidBundleError) Unwrap() error { return e.Err }

// NoMatchingVersionError indicates no tags satisfy the compatibility constraint.
type NoMatchingVersionError struct {
	Ref        string
	Constraint string
	Err        error
}

// Error reports that no tags satisfy the constraint, naming the ref and constraint.
func (e *NoMatchingVersionError) Error() string {
	return fmt.Sprintf("no versions of %s match constraint %q: %v", e.Ref, e.Constraint, e.Err)
}

// Unwrap returns the underlying error so errors.Is/As works.
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
	bundle, _, err := r.resolvePinned(ctx, ref, constraint, mode)
	return bundle, err
}

// ResolvePinned is [Resolver.Resolve] plus the manifest digest of the artifact
// the returned bundle actually came from, obtained as ONE observation with the
// content (see [CachedStore.PullPinned]). Asking the registry for a digest AFTER
// a pull is a second observation of a mutable tag: re-pushed in between, it
// names bytes the caller never read.
//
// LocalOnly reports the digest RECORDED beside the cached bundle, read from the
// same cache generation as the bytes — never re-derived, which offline would
// mean dialing a registry the mode promised not to touch. An entry that recorded
// no digest reports none.
func (r *Resolver) ResolvePinned(ctx context.Context, ref string, mode ResolveMode) (*contract.Bundle, string, error) {
	return r.resolvePinned(ctx, ref, "", mode)
}

func (r *Resolver) resolvePinned(ctx context.Context, ref, constraint string, mode ResolveMode) (*contract.Bundle, string, error) {
	ref = strings.TrimPrefix(ref, "oci://")

	if mode == LocalOnly {
		return r.resolveLocal(ctx, ref)
	}

	// For untagged refs in remote mode, resolve the best tag first.
	if !HasExplicitTag(ref) {
		resolved, err := ResolveRef(ctx, r.store, ref, constraint)
		if err != nil {
			// Pass through typed errors (auth/unreachable/not-found/invalid) —
			// only a genuine no-match-for-constraint becomes NoMatchingVersion.
			if typed := classifyPullError(err); typed != nil {
				return nil, "", typed
			}
			if constraint != "" {
				return nil, "", &NoMatchingVersionError{Ref: ref, Constraint: constraint, Err: err}
			}
			return nil, "", &ArtifactNotFoundError{Ref: ref, Err: err}
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
			logging.LoggerFromContext(ctx).Warn("failed to cache version", "ref", ref+":"+v, "error", pullErr)
		}
	}
	return versions, nil
}

// classifyPullError returns the error unchanged when it is already one of the
// resolver's typed errors (the client types auth/not-found/unreachable via
// wrapRemoteError, and invalid-ref/invalid-bundle at the parse/extract sites),
// or nil when it is unrecognized so the caller can apply a default.
func classifyPullError(err error) error {
	var (
		authErr          *AuthenticationError
		notFoundErr      *ArtifactNotFoundError
		unreachableErr   *RegistryUnreachableError
		invalidRefErr    *InvalidRefError
		invalidBundleErr *InvalidBundleError
	)
	switch {
	case errors.As(err, &authErr), errors.As(err, &notFoundErr), errors.As(err, &unreachableErr),
		errors.As(err, &invalidRefErr), errors.As(err, &invalidBundleErr):
		return err
	}
	return nil
}

// cachedPuller is a store that can serve from its own cache, with the identity
// recorded for what it serves, without contacting the registry (see
// [CachedStore.PullCachedPinned]).
type cachedPuller interface {
	PullCachedPinned(ctx context.Context, ref string) (*contract.Bundle, string, bool)
}

func (r *Resolver) resolveLocal(ctx context.Context, ref string) (*contract.Bundle, string, error) {
	// A cached store's Pull falls through to the REGISTRY on a cache miss, so
	// asking it to Pull would make LocalOnly a network call -- the exact opposite
	// of what the mode promises, and enough to hang a caller that walks thousands
	// of cached refs offline. Read the cache directly when the store can.
	if cp, ok := r.store.(cachedPuller); ok {
		bundle, digest, hit := cp.PullCachedPinned(ctx, ref)
		if !hit {
			return nil, "", &ArtifactNotFoundError{Ref: ref, Err: errors.New("not found in local cache")}
		}
		return localBundle(ref, bundle, digest)
	}
	// A store with no cache of its own has nothing "local" to read; its Pull is
	// the only option, and its typed auth/not-found/unreachable errors are more
	// useful to the caller than a blanket cache miss.
	bundle, err := r.store.Pull(ctx, ref)
	if err != nil {
		if typed := classifyPullError(err); typed != nil {
			return nil, "", typed
		}
		return nil, "", &ArtifactNotFoundError{Ref: ref, Err: fmt.Errorf("not found in local cache: %w", err)}
	}
	// Nothing recorded an identity for these bytes, so none is claimed.
	return localBundle(ref, bundle, "")
}

func localBundle(ref string, bundle *contract.Bundle, digest string) (*contract.Bundle, string, error) {
	if bundle.Contract == nil {
		return nil, "", &InvalidBundleError{Ref: ref, Err: fmt.Errorf("bundle has no contract")}
	}
	return bundle, digest, nil
}

func (r *Resolver) resolveWithFetch(ctx context.Context, ref string) (*contract.Bundle, string, error) {
	bundle, digest, err := pullPinned(ctx, r.store, ref)
	if err != nil {
		if typed := classifyPullError(err); typed != nil {
			return nil, "", typed
		}
		return nil, "", err
	}
	if bundle.Contract == nil {
		return nil, "", &InvalidBundleError{Ref: ref, Err: fmt.Errorf("bundle has no contract")}
	}
	return bundle, digest, nil
}

// pinnedPuller is a store that binds the manifest digest it reports to the bytes
// it downloaded (see [CachedStore.PullPinned]).
type pinnedPuller interface {
	PullPinned(ctx context.Context, ref string) (*contract.Bundle, string, error)
}

// pullPinned pulls ref and reports the digest of the artifact that answered. A
// store that can bind the two itself (and cache the result) is asked to; any
// other store is pinned generically.
func pullPinned(ctx context.Context, store BundleStore, ref string) (*contract.Bundle, string, error) {
	if pp, ok := store.(pinnedPuller); ok {
		return pp.PullPinned(ctx, ref)
	}
	return resolveAndPull(ctx, store, ref)
}

// resolveAndPull fetches ref and the manifest digest of the artifact that
// answered, as ONE observation.
//
// A tag is MUTABLE, so "download the tag" and "ask what the tag points at" are
// two observations of a moving target: re-pushed in between, the second answers
// with a digest naming bytes nobody downloaded, and the caller then publishes
// bundle A under digest B. So the tag is resolved ONCE and the content is
// fetched from the digest-pinned reference the resolve named — digest and bytes
// agree by construction. A registry that will not answer the resolve leaves the
// identity unknown and the tag is fetched as written: real content, no claimed
// digest, which is the honest report.
func resolveAndPull(ctx context.Context, store BundleStore, ref string) (*contract.Bundle, string, error) {
	digest, target := digestFromRef(ref), ref
	if digest == "" {
		// An empty digest is "the registry would not say", whether it arrived as an
		// error or as a blank answer. Pinning to it would fetch "<repo>@", which
		// names nothing.
		if d, err := store.Resolve(ctx, ref); err == nil && d != "" {
			digest, target = d, PinRefToDigest(ref, d)
		}
	}
	bundle, err := store.Pull(ctx, target)
	if err != nil {
		return nil, "", err
	}
	return bundle, digest, nil
}

// FilterSemverTags returns only valid semver tags, sorted descending (latest first).
// Thin wrapper over pkg/semver so the resolver and the dashboard share one impl.
func FilterSemverTags(tags []string) []string {
	return semver.Filter(tags)
}
