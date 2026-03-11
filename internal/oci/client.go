package oci

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/trianalab/pacto/pkg/contract"
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

// WithRemoteOptions adds remote.Option values used for all remote operations.
func WithRemoteOptions(opts ...remote.Option) ClientOption {
	return func(c *Client) {
		c.remoteOpts = append(c.remoteOpts, opts...)
	}
}

// Function variables for testing.
var (
	buildImageFn  = bundleToImage
	imageDigestFn = func(img v1.Image) (v1.Hash, error) { return img.Digest() }
)

// Client implements BundleStore using go-containerregistry.
type Client struct {
	keychain   authn.Keychain
	nameOpts   []name.Option
	remoteOpts []remote.Option
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
	return append([]remote.Option{remote.WithAuthFromKeychain(c.keychain), remote.WithContext(ctx)}, c.remoteOpts...)
}

// Push converts a Bundle to an OCI image and pushes it to the given reference.
// Returns the digest of the pushed image.
func (c *Client) Push(ctx context.Context, ref string, bundle *contract.Bundle) (string, error) {
	r, err := name.ParseReference(ref, c.nameOpts...)
	if err != nil {
		return "", fmt.Errorf("invalid reference %q: %w", ref, err)
	}

	slog.Debug("building OCI image from bundle", "ref", ref)
	img, err := buildImageFn(bundle)
	if err != nil {
		return "", fmt.Errorf("failed to build OCI image: %w", err)
	}

	slog.Debug("writing image to registry", "ref", ref)
	if err := remote.Write(r, img, c.remoteOptions(ctx)...); err != nil {
		return "", wrapRemoteError(ref, err)
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
	r, err := name.ParseReference(ref, c.nameOpts...)
	if err != nil {
		return nil, fmt.Errorf("invalid reference %q: %w", ref, err)
	}

	slog.Debug("fetching image from registry", "ref", ref)
	img, err := remote.Image(r, c.remoteOptions(ctx)...)
	if err != nil {
		return nil, wrapRemoteError(ref, err)
	}

	slog.Debug("extracting bundle from image", "ref", ref)
	bundle, err := imageToBundle(img)
	if err != nil {
		return nil, fmt.Errorf("failed to extract bundle: %w", err)
	}

	return bundle, nil
}

// Resolve resolves a reference to its digest.
func (c *Client) Resolve(ctx context.Context, ref string) (string, error) {
	r, err := name.ParseReference(ref, c.nameOpts...)
	if err != nil {
		return "", fmt.Errorf("invalid reference %q: %w", ref, err)
	}

	slog.Debug("resolving digest", "ref", ref)
	desc, err := remote.Head(r, c.remoteOptions(ctx)...)
	if err != nil {
		return "", wrapRemoteError(ref, err)
	}

	slog.Debug("resolved digest", "ref", ref, "digest", desc.Digest.String())
	return desc.Digest.String(), nil
}

// ListTags returns all tags available for the given repository.
func (c *Client) ListTags(ctx context.Context, repo string) ([]string, error) {
	r, err := name.NewRepository(repo, c.nameOpts...)
	if err != nil {
		return nil, fmt.Errorf("invalid repository %q: %w", repo, err)
	}

	slog.Debug("listing tags", "repo", repo)
	tags, err := remote.List(r, c.remoteOptions(ctx)...)
	if err != nil {
		return nil, wrapRemoteError(repo, err)
	}

	slog.Debug("tags listed", "repo", repo, "count", len(tags))
	return tags, nil
}
