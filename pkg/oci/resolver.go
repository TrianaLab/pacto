package oci

import (
	"context"
	"errors"
	"fmt"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/semver"
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
// A cache miss triggers a pull from the registry.
//
// Errors are typed:
//   - *InvalidRefError: ref cannot be parsed
//   - *ArtifactNotFoundError: not in registry (404)
//   - *AuthenticationError: credentials rejected (401/403)
//   - *RegistryUnreachableError: network/DNS failure
//   - *InvalidBundleError: pulled artifact is not a valid Pacto bundle
func (r *Resolver) Resolve(ctx context.Context, ref string) (*contract.Bundle, error) {
	return r.ResolveConstrained(ctx, ref, "")
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
func (r *Resolver) ResolveConstrained(ctx context.Context, ref, constraint string) (*contract.Bundle, error) {
	bundle, _, err := r.resolvePinned(ctx, ref, constraint)
	return bundle, err
}

// ResolvePinned is [Resolver.Resolve] plus the identity of the artifact the
// returned bundle actually came from, obtained as ONE observation with the
// content (see [CachedStore.PullPinned]). Asking the registry for a digest AFTER
// a pull is a second observation of a mutable tag: re-pushed in between, it
// names bytes the caller never read.
//
// It reports the digest alone, because there is no second identity to carry: the bytes came from the registry under the reference this
// call named, or from a cache entry that STATES that reference — a stored
// generation naming any other reference is not a hit for this one and the
// registry is asked (see [CachedStore.cachedEntryFrom]).
func (r *Resolver) ResolvePinned(ctx context.Context, ref string) (*contract.Bundle, CachedRef, error) {
	return r.resolvePinned(ctx, ref, "")
}

func (r *Resolver) resolvePinned(ctx context.Context, ref, constraint string) (*contract.Bundle, CachedRef, error) {
	ref = contract.TrimOCIScheme(ref)

	// For untagged refs, resolve the best tag first.
	if !HasExplicitTag(ref) {
		resolved, err := ResolveRef(ctx, r.store, ref, constraint)
		if err != nil {
			// Pass through typed errors (auth/unreachable/not-found/invalid) —
			// only a genuine no-match-for-constraint becomes NoMatchingVersion.
			if typed := classifyPullError(err); typed != nil {
				return nil, CachedRef{}, typed
			}
			if constraint != "" {
				return nil, CachedRef{}, &NoMatchingVersionError{Ref: ref, Constraint: constraint, Err: err}
			}
			return nil, CachedRef{}, &ArtifactNotFoundError{Ref: ref, Err: err}
		}
		ref = resolved
	}

	return r.resolveWithFetch(ctx, ref)
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

func (r *Resolver) resolveWithFetch(ctx context.Context, ref string) (*contract.Bundle, CachedRef, error) {
	bundle, digest, err := PullPinned(ctx, r.store, ref)
	if err != nil {
		if typed := classifyPullError(err); typed != nil {
			return nil, CachedRef{}, typed
		}
		return nil, CachedRef{}, err
	}
	if bundle.Contract == nil {
		return nil, CachedRef{}, &InvalidBundleError{Ref: ref, Err: fmt.Errorf("bundle has no contract")}
	}
	// Only the digest: these bytes were fetched under the reference this call
	// named — or served by a cache entry that states it, since one stating any
	// other reference is a miss — so the caller already holds their reference and
	// no cache generation is claiming a different one.
	return bundle, CachedRef{Digest: digest}, nil
}

// pinnedPuller is a store that binds the manifest digest it reports to the bytes
// it downloaded (see [CachedStore.PullPinned]).
type pinnedPuller interface {
	PullPinned(ctx context.Context, ref string) (*contract.Bundle, string, error)
}

// PullPinned pulls ref and reports the digest of the artifact that answered. A
// store that can bind the two itself (and cache the result) is asked to; any
// other store is pinned generically by [resolveAndPull].
//
// This is the only supported way to learn what a [BundleStore] just handed you.
// Calling Resolve and Pull separately is TWO observations of a mutable tag, and
// against a [CachedStore] it is not even a race: Resolve always reaches the
// registry while Pull may serve a generation cached under repo:tag, so a warm
// cache plus a re-pushed tag pairs the new digest with the old bundle every
// time.
func PullPinned(ctx context.Context, store BundleStore, ref string) (*contract.Bundle, string, error) {
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
