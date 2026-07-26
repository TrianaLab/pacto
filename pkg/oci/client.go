package oci

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/trianalab/pacto/v3/pkg/contract"
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

// Function variables for testing.
var (
	buildImageFn  = bundleToImage
	imageDigestFn = func(img v1.Image) (v1.Hash, error) { return img.Digest() }
)

// Client implements BundleStore using go-containerregistry.
type Client struct {
	keychain authn.Keychain
	nameOpts []name.Option
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

// parseRef parses an OCI reference string with the client's name options.
func (c *Client) parseRef(ref string) (name.Reference, error) {
	r, err := name.ParseReference(ref, c.nameOpts...)
	if err != nil {
		return nil, &InvalidRefError{Ref: ref, Err: err}
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

	slog.Debug("building OCI image from bundle", "ref", ref)
	img, err := buildImageFn(bundle)
	if err != nil {
		return "", fmt.Errorf("failed to build OCI image: %w", err)
	}

	slog.Debug("writing image to registry", "ref", ref)
	if err := c.doWithAuth(ctx, r.Context(), ref, func(opts []remote.Option) error {
		return remote.Write(r, img, opts...)
	}); err != nil {
		return "", err
	}

	digest, err := imageDigestFn(img)
	if err != nil {
		return "", fmt.Errorf("failed to compute digest: %w", err)
	}

	slog.Debug("image pushed successfully", "ref", ref, "digest", digest.String())
	return digest.String(), nil
}

// Pull fetches an OCI image from the given reference and converts it to a Bundle.
func (c *Client) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	r, err := c.parseRef(ref)
	if err != nil {
		return nil, err
	}

	slog.Debug("fetching image from registry", "ref", ref)
	var img v1.Image
	if err := c.doWithAuth(ctx, r.Context(), ref, func(opts []remote.Option) error {
		var opErr error
		img, opErr = remote.Image(r, opts...)
		return opErr
	}); err != nil {
		return nil, err
	}

	slog.Debug("extracting bundle from image", "ref", ref)
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

	slog.Debug("resolving digest", "ref", ref)
	var desc *v1.Descriptor
	if err := c.doWithAuth(ctx, r.Context(), ref, func(opts []remote.Option) error {
		var opErr error
		desc, opErr = remote.Head(r, opts...)
		return opErr
	}); err != nil {
		return "", err
	}

	slog.Debug("resolved digest", "ref", ref, "digest", desc.Digest.String())
	return desc.Digest.String(), nil
}

// ListTags returns all tags available for the given repository.
func (c *Client) ListTags(ctx context.Context, repo string) ([]string, error) {
	r, err := name.NewRepository(repo, c.nameOpts...)
	if err != nil {
		return nil, &InvalidRefError{Ref: repo, Err: err}
	}

	slog.Debug("listing tags", "repo", repo)
	var tags []string
	if err := c.doWithAuth(ctx, r, repo, func(opts []remote.Option) error {
		var opErr error
		tags, opErr = remote.List(r, opts...)
		return opErr
	}); err != nil {
		return nil, err
	}

	slog.Debug("tags listed", "repo", repo, "count", len(tags))
	return tags, nil
}
