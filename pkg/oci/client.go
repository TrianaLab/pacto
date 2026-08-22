package oci

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/logging"
)

// BundleStore handles push and pull of contract bundles to/from OCI registries.
type BundleStore interface {
	Push(ctx context.Context, ref string, bundle *contract.Bundle) (string, error)
	Pull(ctx context.Context, ref string) (*contract.Bundle, error)
	Resolve(ctx context.Context, ref string) (string, error)
	ListTags(ctx context.Context, repo string) ([]string, error)
}

// ClientOption configures the OCI Client.
type ClientOption func(*Client)

// WithNameOptions adds name.Option values used when parsing OCI references.
func WithNameOptions(opts ...name.Option) ClientOption {
	return func(c *Client) {
		c.nameOpts = append(c.nameOpts, opts...)
	}
}

// WithInsecureRegistries marks specific registry hosts as plain-HTTP, so refs to
// them are pulled/resolved over http instead of https. It is scoped per host (not
// global), so production https registries are unaffected. Intended for a
// controlled in-cluster registry (e.g. the evidence-server E2E), never the public
// internet.
func WithInsecureRegistries(hosts ...string) ClientOption {
	return func(c *Client) {
		if c.insecure == nil {
			c.insecure = map[string]bool{}
		}
		for _, h := range hosts {
			if h != "" {
				c.insecure[h] = true
			}
		}
	}
}

// Function variables for testing.
var (
	buildImageFn  = bundleToImage
	imageDigestFn = func(img v1.Image) (v1.Hash, error) { return img.Digest() }
	// remoteListFn is the seam that lets a test observe the repository tag
	// listing is asked about — including the scheme it would be reached over,
	// which no loopback test registry can exercise (go-containerregistry treats
	// loopback as plain HTTP whether or not the allowance was applied).
	remoteListFn = remote.List
)

// Client implements BundleStore using go-containerregistry.
type Client struct {
	keychain authn.Keychain
	nameOpts []name.Option
	insecure map[string]bool // registry hosts to reach over plain HTTP
}

// NewClient creates a new OCI client with the given keychain.
func NewClient(keychain authn.Keychain, opts ...ClientOption) *Client {
	c := &Client{keychain: keychain}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// remoteOptions builds the remote.Option slice for all OCI operations.
func (c *Client) remoteOptions(ctx context.Context) []remote.Option {
	return []remote.Option{remote.WithAuthFromKeychain(c.keychain), remote.WithContext(ctx)}
}

// candidateProvider is implemented by keychains that can enumerate all
// credential sources for a registry so the Client can fall through on rejection.
type candidateProvider interface {
	Candidates(authn.Resource) ([]CredSource, error)
}

// wrapIfErr wraps a non-nil remote error into a domain error, passing nil through.
func wrapIfErr(ref string, err error) error {
	if err == nil {
		return nil
	}
	return wrapRemoteError(ref, err)
}

// doWithAuth runs op against each credential source in priority order, falling
// through to the next source when the registry returns 401/403. The first source
// that succeeds wins. Non-auth errors are returned immediately (wrapped). If all
// sources are rejected, a source-named AuthenticationError is returned.
func (c *Client) doWithAuth(ctx context.Context, res authn.Resource, ref string, op func(opts []remote.Option) error) error {
	cp, ok := c.keychain.(candidateProvider)
	if !ok {
		return wrapIfErr(ref, op(c.remoteOptions(ctx)))
	}

	cands, err := cp.Candidates(res)
	if err != nil {
		return wrapIfErr(ref, op(c.remoteOptions(ctx)))
	}
	if len(cands) == 0 {
		cands = []CredSource{{Name: "anonymous", Authenticator: authn.Anonymous}}
	}

	var tried, rejected []string
	var lastErr error
	for _, cand := range cands {
		tried = append(tried, cand.Name)
		err := op([]remote.Option{remote.WithAuth(cand.Authenticator), remote.WithContext(ctx)})
		switch {
		case err == nil:
			return nil
		case isAuthError(err):
			rejected = append(rejected, cand.Name)
			lastErr = err
		default:
			return wrapRemoteError(ref, err)
		}
	}
	return &AuthenticationError{Ref: ref, Err: lastErr, Tried: tried, Rejected: rejected}
}

// parseRef parses an OCI reference string with the client's name options. When
// the ref's registry host is marked insecure, name.Insecure is applied for that
// parse only, so that host is reached over plain HTTP without affecting others.
func (c *Client) parseRef(ref string) (name.Reference, error) {
	r, err := name.ParseReference(ref, c.nameOpts...)
	if err != nil {
		return nil, &InvalidRefError{Ref: ref, Err: err}
	}
	if c.insecure[r.Context().RegistryStr()] {
		opts := append(append([]name.Option{}, c.nameOpts...), name.Insecure)
		if ir, ierr := name.ParseReference(ref, opts...); ierr == nil {
			return ir, nil
		}
	}
	return r, nil
}

// Push converts a Bundle to an OCI image and pushes it to the given reference.
// Returns the digest of the pushed image.
func (c *Client) Push(ctx context.Context, ref string, bundle *contract.Bundle) (string, error) {
	r, err := c.parseRef(ref)
	if err != nil {
		return "", err
	}

	logging.LoggerFromContext(ctx).Debug("building OCI image from bundle", "ref", ref)
	img, err := buildImageFn(bundle)
	if err != nil {
		return "", fmt.Errorf("failed to build OCI image: %w", err)
	}

	logging.LoggerFromContext(ctx).Debug("writing image to registry", "ref", ref)
	if err := c.doWithAuth(ctx, r.Context(), ref, func(opts []remote.Option) error {
		return remote.Write(r, img, opts...)
	}); err != nil {
		return "", err
	}

	digest, err := imageDigestFn(img)
	if err != nil {
		return "", fmt.Errorf("failed to compute digest: %w", err)
	}

	logging.LoggerFromContext(ctx).Debug("image pushed successfully", "ref", ref, "digest", digest.String())
	return digest.String(), nil
}

// Pull fetches an OCI image from the given reference and converts it to a Bundle.
func (c *Client) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	r, err := c.parseRef(ref)
	if err != nil {
		return nil, err
	}

	logging.LoggerFromContext(ctx).Debug("fetching image from registry", "ref", ref)
	var img v1.Image
	if err := c.doWithAuth(ctx, r.Context(), ref, func(opts []remote.Option) error {
		var opErr error
		img, opErr = remote.Image(r, opts...)
		return opErr
	}); err != nil {
		return nil, err
	}

	logging.LoggerFromContext(ctx).Debug("extracting bundle from image", "ref", ref)
	bundle, err := imageToBundle(img)
	if err != nil {
		return nil, &InvalidBundleError{Ref: ref, Err: err}
	}

	return bundle, nil
}

// Resolve resolves a reference to its digest.
func (c *Client) Resolve(ctx context.Context, ref string) (string, error) {
	r, err := c.parseRef(ref)
	if err != nil {
		return "", err
	}

	logging.LoggerFromContext(ctx).Debug("resolving digest", "ref", ref)
	var desc *v1.Descriptor
	if err := c.doWithAuth(ctx, r.Context(), ref, func(opts []remote.Option) error {
		var opErr error
		desc, opErr = remote.Head(r, opts...)
		return opErr
	}); err != nil {
		return "", err
	}

	logging.LoggerFromContext(ctx).Debug("resolved digest", "ref", ref, "digest", desc.Digest.String())
	return desc.Digest.String(), nil
}

// ListTags returns all tags available for the given repository.
//
// The repository is parsed through parseRef, so the per-host plain-HTTP
// allowance decides how this host is reached exactly as it does for a pull. A
// separate name.NewRepository parse silently skipped it, and only for
// REPOSITORY questions: an in-cluster registry named by a service FQDN (not
// localhost, so https by default) could be pulled from by digest yet never
// asked which versions it holds — so resolving a semver constraint and
// discovering the newest published revision failed on a registry the client was
// explicitly told to reach over HTTP. A reference carries a tag or digest the
// repository does not; only its Context() (the repository) is used here.
func (c *Client) ListTags(ctx context.Context, repo string) ([]string, error) {
	ref, err := c.parseRef(repo)
	if err != nil {
		return nil, err
	}
	r := ref.Context()

	logging.LoggerFromContext(ctx).Debug("listing tags", "repo", repo)
	var tags []string
	if err := c.doWithAuth(ctx, r, repo, func(opts []remote.Option) error {
		var opErr error
		tags, opErr = remoteListFn(r, opts...)
		return opErr
	}); err != nil {
		return nil, err
	}

	logging.LoggerFromContext(ctx).Debug("tags listed", "repo", repo, "count", len(tags))
	return tags, nil
}
