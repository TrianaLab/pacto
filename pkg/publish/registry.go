package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

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
// its plain-HTTP hosts.
type Client struct {
	keychain authn.Keychain
	insecure []string
}

// NewClient builds a Client over keychain that reaches insecure hosts over
// plain HTTP.
func NewClient(keychain authn.Keychain, insecure ...string) *Client {
	return &Client{keychain: keychain, insecure: insecure}
}

func (c *Client) parse(ref string) (name.Reference, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return nil, err
	}
	if slices.Contains(c.insecure, r.Context().RegistryStr()) {
		return name.ParseReference(ref, name.Insecure)
	}
	return r, nil
}

func (c *Client) opts(ctx context.Context) []remote.Option {
	return []remote.Option{remote.WithAuthFromKeychain(c.keychain), remote.WithContext(ctx)}
}

func notFound(err error) bool {
	var t *transport.Error
	return errors.As(err, &t) && t.StatusCode == http.StatusNotFound
}

// Head implements Registry.
func (c *Client) Head(ctx context.Context, ref string) (*Remote, error) {
	r, err := c.parse(ref)
	if err != nil {
		return nil, err
	}
	d, err := remote.Get(r, c.opts(ctx)...)
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
	r, err := c.parse(repo)
	if err != nil {
		return nil, err
	}
	tags, err := remote.List(r.Context(), c.opts(ctx)...)
	if notFound(err) {
		return nil, nil
	}
	return tags, err
}

// Pull implements Registry.
func (c *Client) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	return oci.NewClient(c.keychain, oci.WithInsecureRegistries(c.insecure...)).Pull(ctx, ref)
}

// Write implements Registry.
func (c *Client) Write(ctx context.Context, repo string, a Artifact) (string, error) {
	d, err := a.Digest()
	if err != nil {
		return "", err
	}
	r, err := c.parse(repo + "@" + d.String())
	if err != nil {
		return "", err
	}
	switch a := a.(type) {
	case v1.Image:
		err = remote.Write(r, a, c.opts(ctx)...)
	case v1.ImageIndex:
		err = remote.WriteIndex(r, a, c.opts(ctx)...)
	default:
		err = fmt.Errorf("cannot push a %T", a)
	}
	return d.String(), err
}

// Tag implements Registry.
func (c *Client) Tag(ctx context.Context, repo, digest, tag string) error {
	src, err := c.parse(repo + "@" + digest)
	if err != nil {
		return err
	}
	d, err := remote.Get(src, c.opts(ctx)...)
	if err != nil {
		return err
	}
	dst, err := c.parse(repo + ":" + tag)
	if err != nil {
		return err
	}
	return remote.Tag(dst.(name.Tag), d, c.opts(ctx)...)
}

// Referrers implements Registry.
func (c *Client) Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error) {
	r, err := c.parse(repo + "@" + digest)
	if err != nil {
		return nil, err
	}
	idx, err := remote.Referrers(r.(name.Digest), append(c.opts(ctx), remote.WithFilter("artifactType", artifactType))...)
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
	r, err := c.parse(repo + "@" + digest)
	if err != nil {
		return nil, nil, err
	}
	img, err := remote.Image(r, c.opts(ctx)...)
	if err != nil {
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
