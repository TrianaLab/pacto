package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	orasremote "oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// Remote is what a tag points at.
type Remote struct {
	Digest    string
	MediaType string
	Size      int64
	// ArtifactType is the manifest's artifactType, empty when it has none.
	ArtifactType string
	Annotations  map[string]string
	// Subject is the digest the manifest's subject names, empty when none.
	Subject string
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
	// Referrers lists the digests of repo@digest's referrers of artifactType:
	// the Referrers API where the registry serves it, the manifests its
	// pacto-sig write-once tags name where it does not. It never reads the
	// shared sha256-<hex> referrers-tag index.
	Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error)
	// Mode is how the registry holding repo attaches referrers, from the
	// process registry probe; digest is a manifest repo holds.
	Mode(ctx context.Context, repo, digest string) (capabilities.ReferrersMode, error)
	// PutSigTag names art, a referrer of repo@digest already written, with
	// its write-once tag [SigTag] and returns the tag.
	PutSigTag(ctx context.Context, repo, digest string, art ocireferrer.Artifact) (string, error)
	// Layer reads the one layer of the small manifest repo@digest, such as
	// a signature referrer, and the manifest's annotations.
	Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error)
}

// Client is the go-containerregistry Registry, with Pacto's credentials and
// its plain-HTTP hosts. It goes through an oci.Client, so a write tries every
// credential a pull would and fails with the same typed errors.
type Client struct {
	oci      *oci.Client
	keychain authn.Keychain
	insecure []string
}

// NewClient builds a Client over keychain that reaches insecure hosts over
// plain HTTP.
func NewClient(keychain authn.Keychain, insecure ...string) *Client {
	return &Client{oci: oci.NewClient(keychain, oci.WithInsecureRegistries(insecure...)), keychain: keychain, insecure: insecure}
}

// sigTagPrefix starts every write-once tag naming a signature or SBOM of
// the manifest with hex digest subjectHex.
const sigTagPrefix = "pacto-sig-"

// SigTagPrefix is the prefix of every write-once tag naming a referrer of
// digest: pacto-sig-<subject hex>-.
func SigTagPrefix(digest string) string {
	_, hex, _ := strings.Cut(digest, ":")
	return sigTagPrefix + hex + "-"
}

// SigTag is the write-once tag naming the referrer manifest of digest:
// pacto-sig-<subject hex>-<manifest hex12>. Content-addressed, so two
// writers never claim one tag for different manifests by accident.
func SigTag(digest, manifest string) string {
	_, hex, _ := strings.Cut(manifest, ":")
	return SigTagPrefix(digest) + hex[:12]
}

// open opens repo for the write-once writer and the probe.
func (c *Client) open(repo string) (*orasremote.Repository, error) {
	return ocireferrer.Open(repo, ocireferrer.Options{Keychain: c.keychain, Insecure: c.insecure})
}

// Mode implements Registry.
func (c *Client) Mode(ctx context.Context, repo, digest string) (capabilities.ReferrersMode, error) {
	r, err := c.open(repo)
	if err != nil {
		return "", err
	}
	caps, err := ocireferrer.Probes.Probe(ctx, r, digest, false)
	return caps.Referrers, err
}

// PutSigTag implements Registry.
func (c *Client) PutSigTag(ctx context.Context, repo, digest string, art ocireferrer.Artifact) (string, error) {
	r, err := c.open(repo)
	if err != nil {
		return "", err
	}
	tag := SigTag(digest, art.ManifestDesc.Digest.String())
	return tag, ocireferrer.PushWriteOnce(ctx, r, art, tag)
}

// maxSigTags bounds the write-once tags one listing reads behind: past it
// the listing fails, never truncates. A var so a test can lower it.
var maxSigTags = maxReferrers

// sigTagReferrers is what the pacto-sig tags of repo@digest name, read from
// each tag's manifest. A tag that cannot be read, that does not name the
// manifest it points at or whose manifest's subject is not digest (a moved
// tag) is an error.
func (c *Client) sigTagReferrers(ctx context.Context, repo, digest string) ([]Remote, error) {
	r, err := c.open(repo)
	if err != nil {
		return nil, err
	}
	tags, err := ocireferrer.Tags(ctx, r, SigTagPrefix(digest))
	if err != nil {
		return nil, err
	}
	if len(tags) > maxSigTags {
		return nil, fmt.Errorf("%s@%s has %d write-once signature tags; one check reads at most %d", repo, digest, len(tags), maxSigTags)
	}
	out := make([]Remote, 0, len(tags))
	for _, t := range tags {
		h, err := c.Head(ctx, repo+":"+t)
		if err == nil && h == nil {
			err = errors.New("the tag is gone")
		}
		if err == nil && (!strings.HasPrefix(h.Digest, "sha256:"+t[strings.LastIndex(t, "-")+1:]) || h.Subject != digest) {
			err = fmt.Errorf("it points at %s, whose subject is %q: a moved tag", h.Digest, h.Subject)
		}
		if err != nil {
			return nil, fmt.Errorf("reading the write-once tag %s: %w", t, err)
		}
		out = append(out, *h)
	}
	return out, nil
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
		ArtifactType string            `json:"artifactType"`
		Annotations  map[string]string `json:"annotations"`
		Subject      *struct {
			Digest string `json:"digest"`
		} `json:"subject"`
	}
	_ = json.Unmarshal(d.Manifest, &m)
	out := &Remote{Digest: d.Digest.String(), MediaType: string(d.MediaType), Size: d.Size, ArtifactType: m.ArtifactType, Annotations: m.Annotations}
	if m.Subject != nil {
		out.Subject = m.Subject.Digest
	}
	return out, nil
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
	tagged, refs, err := c.tagMode(ctx, repo, digest)
	if err != nil || tagged {
		var out []string
		for _, r := range refs {
			if r.ArtifactType == artifactType {
				out = append(out, r.Digest)
			}
		}
		return out, err
	}
	var idx v1.ImageIndex
	err = c.oci.Do(ctx, repo+"@"+digest, func(r name.Reference, opts []remote.Option) (err error) {
		idx, err = remote.Referrers(r.(name.Digest), append(opts, remote.WithFilter("artifactType", artifactType), noTagIndex)...)
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

// noTagIndex keeps go-containerregistry from reading the shared
// sha256-<hex> referrers-tag index when the Referrers API answers 404: anyone
// with push access rewrites that index, so on a registry probed native a
// missing API is an error.
var noTagIndex = remote.WithReferrersTagFallback(false)

// tagMode reports whether the registry holding repo serves no Referrers API
// and, if so, what repo@digest's write-once tags name.
func (c *Client) tagMode(ctx context.Context, repo, digest string) (bool, []Remote, error) {
	mode, err := c.Mode(ctx, repo, digest)
	if err != nil {
		return false, nil, err
	}
	if mode != capabilities.ReferrersWriteOnceTags {
		return false, nil, nil
	}
	refs, err := c.sigTagReferrers(ctx, repo, digest)
	return true, refs, err
}

// ErrReferrersPartial is returned, with the first page, when the registry
// paginates a referrers list: go-containerregistry reads only that page.
var ErrReferrersPartial = errors.New("the registry returned only the first page of referrers")

// nextPageRecorder notes a referrers response that links to a next page.
type nextPageRecorder struct {
	base http.RoundTripper
	next atomic.Bool
}

func (n *nextPageRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := n.base.RoundTrip(req)
	if err == nil && strings.Contains(req.URL.Path, "/referrers/") && strings.Contains(resp.Header.Get("Link"), `rel="next"`) {
		n.next.Store(true)
	}
	return resp, err
}

// ReferrerDescriptors lists the referrers of repo@digest, unfiltered, with
// their artifact type and annotations as the registry reports them, or as
// the manifests behind its write-once tags say on a registry without the
// Referrers API. A paginated list comes back as its first page with
// [ErrReferrersPartial].
func (c *Client) ReferrerDescriptors(ctx context.Context, repo, digest string) ([]v1.Descriptor, error) {
	tagged, refs, err := c.tagMode(ctx, repo, digest)
	if err != nil || tagged {
		var out []v1.Descriptor
		for _, r := range refs {
			h, _ := v1.NewHash(r.Digest) // the registry named it
			out = append(out, v1.Descriptor{MediaType: types.MediaType(r.MediaType), Size: r.Size, Digest: h, ArtifactType: r.ArtifactType, Annotations: r.Annotations})
		}
		return out, err
	}
	var idx v1.ImageIndex
	rec := &nextPageRecorder{base: remote.DefaultTransport}
	err = c.oci.Do(ctx, repo+"@"+digest, func(r name.Reference, opts []remote.Option) (err error) {
		idx, err = remote.Referrers(r.(name.Digest), append(opts, remote.WithTransport(rec), noTagIndex)...)
		return err
	})
	if err != nil {
		return nil, err
	}
	m, _ := idx.IndexManifest() // Referrers parsed the index already
	out := append([]v1.Descriptor{}, m.Manifests...)
	if rec.next.Load() {
		return out, ErrReferrersPartial
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
