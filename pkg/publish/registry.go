package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// Remote is what a tag points at.
type Remote struct {
	Digest      string
	MediaType   string
	Size        int64
	Annotations map[string]string
}

// Artifact is something publish pushes: a v1.Image or a v1.ImageIndex.
type Artifact interface {
	Digest() (v1.Hash, error)
}

// Registry is the registry access publish needs. *Client implements it. Every
// write is by digest or a tag write, never an overwrite decided here: the plan
// decides, from Head, whether a tag may be written at all.
type Registry interface {
	// Head reads what ref (repo:tag) points at; nil and no error when the
	// registry answered that it does not exist. Any other failure is an error:
	// unreadable is never absent.
	Head(ctx context.Context, ref string) (*Remote, error)
	// Tags lists repo's tags; none and no error when the repository does not exist.
	Tags(ctx context.Context, repo string) ([]string, error)
	Pull(ctx context.Context, ref string) (*contract.Bundle, error)
	// Write pushes a by digest and returns that digest.
	Write(ctx context.Context, repo string, a Artifact) (string, error)
	Tag(ctx context.Context, repo, digest, tag string) error
	// Referrers lists the digests of repo@digest's referrers of artifactType.
	Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error)
	// Layer reads the one layer of the small manifest repo@digest, such as
	// a signature referrer, and the manifest's annotations.
	Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error)
}

// Client is the go-containerregistry Registry, with Pacto's credentials and
// its plain-HTTP hosts. It goes through an oci.Client, so a write tries every
// credential a pull would and fails with the same typed errors.
type Client struct {
	oci *oci.Client
}

// NewClient builds a Client over keychain that reaches insecure hosts over
// plain HTTP.
func NewClient(keychain authn.Keychain, insecure ...string) *Client {
	return &Client{oci: oci.NewClient(keychain, oci.WithInsecureRegistries(insecure...))}
}

func notFound(err error) bool {
	var nf *oci.ArtifactNotFoundError
	return errors.As(err, &nf)
}

// Head implements Registry.
func (c *Client) Head(ctx context.Context, ref string) (*Remote, error) {
	var d *remote.Descriptor
	err := c.oci.Do(ctx, ref, func(r name.Reference, opts []remote.Option) (err error) {
		d, err = remote.Get(r, opts...)
		return err
	})
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m struct {
		Annotations map[string]string `json:"annotations"`
	}
	_ = json.Unmarshal(d.Manifest, &m)
	return &Remote{Digest: d.Digest.String(), MediaType: string(d.MediaType), Size: d.Size, Annotations: m.Annotations}, nil
}

// Tags implements Registry.
func (c *Client) Tags(ctx context.Context, repo string) ([]string, error) {
	var tags []string
	err := c.oci.Do(ctx, repo, func(r name.Reference, opts []remote.Option) (err error) {
		tags, err = remote.List(r.Context(), opts...)
		return err
	})
	if notFound(err) {
		return nil, nil
	}
	return tags, err
}

// Pull implements Registry.
func (c *Client) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	return c.oci.Pull(ctx, ref)
}

// Write implements Registry.
func (c *Client) Write(ctx context.Context, repo string, a Artifact) (string, error) {
	d, err := a.Digest()
	if err != nil {
		return "", err
	}
	var write func(r name.Reference, opts []remote.Option) error
	switch a := a.(type) {
	case v1.Image:
		write = func(r name.Reference, opts []remote.Option) error { return remote.Write(r, a, opts...) }
	case v1.ImageIndex:
		write = func(r name.Reference, opts []remote.Option) error { return remote.WriteIndex(r, a, opts...) }
	default:
		return d.String(), fmt.Errorf("cannot push a %T", a)
	}
	return d.String(), c.oci.Do(ctx, repo+"@"+d.String(), write)
}

// Tag implements Registry.
func (c *Client) Tag(ctx context.Context, repo, digest, tag string) error {
	var d *remote.Descriptor
	if err := c.oci.Do(ctx, repo+"@"+digest, func(src name.Reference, opts []remote.Option) (err error) {
		d, err = remote.Get(src, opts...)
		return err
	}); err != nil {
		return err
	}
	return c.oci.Do(ctx, repo+":"+tag, func(dst name.Reference, opts []remote.Option) error {
		return remote.Tag(dst.(name.Tag), d, opts...)
	})
}

// Referrers implements Registry.
func (c *Client) Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error) {
	var idx v1.ImageIndex
	err := c.oci.Do(ctx, repo+"@"+digest, func(r name.Reference, opts []remote.Option) (err error) {
		idx, err = remote.Referrers(r.(name.Digest), append(opts, remote.WithFilter("artifactType", artifactType))...)
		return err
	})
	if err != nil {
		return nil, err
	}
	// Referrers parsed the index already and filtered it on artifactType.
	m, _ := idx.IndexManifest()
	var out []string
	for _, d := range m.Manifests {
		out = append(out, d.Digest.String())
	}
	return out, nil
}

// maxLayer bounds what Layer reads: a signature bundle is a few kilobytes.
const maxLayer = 4 << 20

// Layer implements Registry.
func (c *Client) Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error) {
	var img v1.Image
	if err := c.oci.Do(ctx, repo+"@"+digest, func(r name.Reference, opts []remote.Option) (err error) {
		img, err = remote.Image(r, opts...)
		return err
	}); err != nil {
		return nil, nil, err
	}
	m, _ := img.Manifest() // remote.Image fetched and parsed it already
	if len(m.Layers) != 1 || m.Layers[0].Size > maxLayer {
		return nil, nil, fmt.Errorf("%s@%s is not one small layer", repo, digest)
	}
	l, _ := img.LayerByDigest(m.Layers[0].Digest) // the manifest names it
	rc, err := l.Compressed()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxLayer))
	return b, m.Annotations, err
}
