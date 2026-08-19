package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/trianalab/pacto/v3/internal/fleetsrc"
	"github.com/trianalab/pacto/v3/pkg/catalog"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// catalogOCIBase is the declaring context recorded for a revision that came out
// of a registry. It is deliberately not a filesystem path: a contract that
// arrived over the network must never be able to make the catalog read a local
// directory of its choosing, so a local reference declared inside a registry
// bundle fails closed here exactly as it does in the lock builder. A local base
// is always an absolute path, so the two can never be confused.
const catalogOCIBase = "oci://"

// CatalogResolver adapts this service to pkg/catalog's Resolver port.
//
// The split is the point. The catalog owns the model — content identity,
// provenance, bounds, completeness — and knows nothing about registries,
// credentials, caches or the filesystem. This adapter owns exactly those and
// nothing else, reusing the same reference parsing, bundle loading, digest
// pinning and content hashing the lock builder already depends on, so a catalog
// and a lockfile cannot disagree about what a reference resolves to.
func (s *Service) CatalogResolver() catalog.Resolver { return catalogResolver{svc: s} }

type catalogResolver struct{ svc *Service }

func (r catalogResolver) Resolve(ctx context.Context, req catalog.ResolveRequest) (catalog.Resolution, error) {
	parsed := graph.ParseDependencyRef(req.Ref)
	if parsed.Location == "" {
		return catalog.Resolution{}, catalogErr(catalog.ReasonInvalidReference, "the reference names nothing")
	}
	if parsed.IsOCI() {
		return r.remote(ctx, parsed.Location, req.Constraint)
	}
	return catalogLocal(parsed.Location, req.Base)
}

// catalogLocal reads a contract bundle from disk. The content identity is a
// hash over the bundle's whole file set, not its path or its declared version:
// two directories claiming the same service and version but holding different
// bytes are two revisions, and only a content hash says so.
func catalogLocal(path, base string) (catalog.Resolution, error) {
	dir, err := catalogLocalDir(path, base)
	if err != nil {
		return catalog.Resolution{}, err
	}
	if _, _, err := resolveLocalPath(dir); err != nil {
		return catalog.Resolution{}, catalogErr(catalog.ReasonNotFound, "no contract bundle at the referenced path")
	}
	b, err := loadLocalBundle(dir)
	if err != nil {
		return catalog.Resolution{}, catalogErr(catalog.ReasonInvalidContract, "the contract bundle at the referenced path could not be read")
	}
	h, err := lock.HashFS(b.FS)
	if err != nil {
		return catalog.Resolution{}, catalogErr(catalog.ReasonInvalidContract, "the contract bundle at the referenced path could not be hashed")
	}
	// Base is the resolved absolute directory rather than the content hash: two
	// byte-identical bundle directories still resolve their own relative
	// references against themselves. The catalog validates the identity it is
	// handed, so it is built here rather than pre-validated twice.
	return catalog.Resolution{
		Contract: b.Contract,
		Content:  catalog.ContentID{Scheme: catalog.SchemeLocal, Digest: h},
		Base:     dir,
	}, nil
}

// catalogLocalDir decides which directory a local reference means. A reference
// declared by a registry bundle means none: honouring it would let a remote
// contract choose which local files the catalog reads.
func catalogLocalDir(path, base string) (string, error) {
	if base == catalogOCIBase {
		return "", catalogErr(catalog.ReasonInvalidReference, "a local reference declared inside a registry bundle cannot be resolved")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if base == "" {
		// Only a root arrives without a declaring base, and a root's relative path
		// is relative to wherever the caller invoked Pacto.
		abs, err := absPathFn(path)
		if err != nil {
			return "", catalogErr(catalog.ReasonInvalidReference, "the reference could not be resolved to an absolute path")
		}
		return abs, nil
	}
	return filepath.Join(base, path), nil
}

// remote pins a registry reference to the digest it currently names. That digest
// is the identity; the tag that led to it is provenance, and the catalog keeps
// both apart.
func (r catalogResolver) remote(ctx context.Context, location, constraint string) (catalog.Resolution, error) {
	if err := r.svc.requireBundleStore(); err != nil {
		return catalog.Resolution{}, catalogErr(catalog.ReasonUnavailable, "no registry client is configured")
	}
	resolvedRef, dgst, err := resolveDigest(ctx, r.svc.BundleStore, location, constraint)
	if err != nil {
		return catalog.Resolution{}, catalogFailure(err)
	}
	b, err := r.svc.BundleStore.Pull(ctx, resolvedRef)
	if err != nil {
		return catalog.Resolution{}, catalogFailure(err)
	}
	return catalog.Resolution{
		Contract:    b.Contract,
		Domain:      fleetsrc.OciDomain(location),
		Content:     catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: dgst},
		ResolvedRef: oci.PinRefToDigest(resolvedRef, dgst),
		Base:        catalogOCIBase,
	}, nil
}

// catalogFailure reduces a resolution failure to a category. The underlying
// error is deliberately not echoed: a registry error carries the host, the
// repository path and, when a credential is rejected, the account it was
// rejected for, and none of that belongs in a catalog someone else may read.
func catalogFailure(err error) error {
	var (
		auth        *oci.AuthenticationError
		notFound    *oci.ArtifactNotFoundError
		noVersion   *oci.NoMatchingVersionError
		badRef      *oci.InvalidRefError
		badBundle   *oci.InvalidBundleError
		unreachable *oci.RegistryUnreachableError
	)
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return catalogErr(catalog.ReasonCancelled, "the resolution ended before the registry answered")
	case errors.As(err, &auth):
		return catalogErr(catalog.ReasonAuthFailed, "the registry rejected the available credentials")
	case errors.As(err, &notFound), errors.As(err, &noVersion), errors.Is(err, oci.ErrNoMatchingTag):
		return catalogErr(catalog.ReasonNotFound, "the registry holds nothing matching the reference")
	case errors.As(err, &badRef):
		return catalogErr(catalog.ReasonInvalidReference, "the reference could not be parsed")
	case errors.As(err, &badBundle):
		return catalogErr(catalog.ReasonInvalidContract, "the artifact is not a valid contract bundle")
	case errors.As(err, &unreachable):
		return catalogErr(catalog.ReasonUnavailable, "the registry could not be reached")
	}
	return catalogErr(catalog.ReasonUnavailable, "the reference could not be resolved")
}

func catalogErr(code, msg string) error { return &catalog.ResolveError{Code: code, Message: msg} }
