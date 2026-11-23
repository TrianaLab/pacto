package inferredcontract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// Deps is what Publish reads and writes with: the registry, the installation
// signer and a verifier of that signer's own key, the Pacto version the
// records name and the clock.
type Deps struct {
	Registry publish.Registry
	Signer   delivery.Signer
	Verify   publish.BundleVerifier
	Pacto    string
	Now      func() time.Time
}

// Entry is one inferred contract a run published, re-signed or could not
// handle: the identity, its repository, the version and digest, and the
// error when there was one.
type Entry struct {
	Identity   string `json:"identity"`
	Repository string `json:"repository"`
	Version    string `json:"version,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Report is what one Publish run did: the contracts pushed, the unsigned
// newest tags it signed because it rebuilt them byte for byte, how many
// externals were unchanged or skipped (other egress, no consumer), every
// error and whether the per-run bound left externals for the next run.
type Report struct {
	Published []Entry `json:"published"`
	Repaired  []Entry `json:"repaired,omitempty"`
	Unchanged int     `json:"unchanged"`
	Skipped   int     `json:"skipped"`
	Errors    []Entry `json:"errors,omitempty"`
	Capped    bool    `json:"capped,omitempty"`
}

// ErrNoSigner is returned, before any write, by a Publish with no signer.
var ErrNoSigner = errors.New("no signer: nothing is published")

// Publish publishes the inferred contract of every external system of snap
// that has a consumer, under root (the installation's
// <registry>/pacto-inferred), when its surface changed since the newest tag
// this installation signed. Each push is a new 0.0.<n> tag, annotated
// run.pacto.contract.provenance: inferred and signed with d.Signer. A tag
// signed by anyone else is never compared or merged; on a partial snapshot
// the previous values are kept.
func Publish(ctx context.Context, d Deps, root string, snap *fleet.FleetSnapshot) (Report, error) {
	if d.Signer == nil || d.Verify == nil {
		return Report{}, ErrNoSigner
	}
	if path.Base(root) != contract.InferredRepository {
		return Report{}, fmt.Errorf("%s is no %s repository", root, contract.InferredRepository)
	}
	if !strings.Contains(path.Dir(root), "/") { // contract.InferredRef skips a first path segment
		return Report{}, fmt.Errorf("%s sits right under the registry host, where readers take it for an organization's service: set registry to <host>/<path>", root)
	}
	r := &run{d: d, root: root, snap: snap, partial: Partial(snap), now: d.Now(), in: inputs(d, snap)}
	rep := Report{Published: []Entry{}}
	for _, key := range slices.Sorted(maps.Keys(snap.Externals)) {
		if key == fleet.OtherEgress || snap.Externals[key].ConsumersTotal == 0 {
			rep.Skipped++
			continue
		}
		if len(rep.Published) == MaxInferredPublishesPerRun {
			rep.Capped = true
			break
		}
		r.one(ctx, key, &rep)
	}
	return rep, nil
}

// inputs are the record inputs of every contract of one run.
func inputs(d Deps, snap *fleet.FleetSnapshot) contract.InferredInputs {
	in := contract.InferredInputs{Pacto: d.Pacto, Discovery: Version, Sources: []contract.InferredSourceFile{}}
	for _, c := range snap.ExternalCatalogs {
		in.Catalog = append(in.Catalog, c.Origin+" "+c.Digest)
	}
	return in
}

// run is one Publish run.
type run struct {
	d       Deps
	root    string
	snap    *fleet.FleetSnapshot
	partial bool
	now     time.Time
	in      contract.InferredInputs
}

// one publishes key's inferred contract into rep when its surface changed.
func (r *run) one(ctx context.Context, key fleet.ExternalKey, rep *Report) {
	repo := Repository(r.root, key)
	fail := func(err error) {
		rep.Errors = append(rep.Errors, Entry{Identity: string(key), Repository: repo, Error: err.Error()})
	}
	tags, err := r.d.Registry.Tags(ctx, repo)
	if err == nil && len(tags) > MaxInferredTags {
		err = fmt.Errorf("the repository holds %d tags; one run reads at most %d", len(tags), MaxInferredTags)
	}
	if err != nil {
		fail(err)
		return
	}
	cur := Surface(r.snap, key, r.now)
	link := openAPIOf(r.snap, key)
	engine := fleet.ExternalEngineOf(r.snap.Externals[key])
	prev, prevSurface, err := r.previous(ctx, repo, key, versions(tags), cur, link, engine, rep)
	if err != nil {
		return // a signer that failed on the repair would leave a new tag unsigned too
	}
	if prev != nil && r.partial {
		cur = Merge(*prevSurface, cur)
	}
	if prev != nil {
		link = r.keepLink(key, link, prev)
		engine = r.keepEngine(engine, prev)
	}
	version := NextVersion(tags)
	b, err := Bundle(key, cur, version, r.in, link, engine)
	if err != nil {
		fail(err)
		return
	}
	if prev != nil && !Changed(ctx, prev, b) {
		rep.Unchanged++
		return
	}
	if !versionTag.MatchString(version) {
		// A foreign tag at the top of the range: a tag pushed past it could
		// never be read back, so every later run would try to push it again.
		fail(fmt.Errorf("the next version %s is past 0.0.999999999, the highest one an inferred contract is read back from; remove the foreign tag above it", version))
		return
	}
	digest, err := r.push(ctx, repo, version, b)
	if err != nil {
		fail(err)
		return
	}
	rep.Published = append(rep.Published, Entry{Identity: string(key), Repository: repo, Version: version, Digest: digest})
}

// previous is the newest of tags (newest first) this installation signed,
// with its surface, or nil. Every tag it passes over is an error in rep. The
// newest tag, when unsigned, is signed only when this run rebuilds it byte
// for byte (a push whose signing failed), so the key never signs bytes this
// routine would not have pushed. It returns errSigning when that signature
// failed.
func (r *run) previous(ctx context.Context, repo string, key fleet.ExternalKey, tags []string, cur contract.InferredSurface, link, engine string, rep *Report) (*contract.Bundle, *contract.InferredSurface, error) {
	for i, tag := range tags {
		if i == maxTagsVerified {
			break
		}
		b, s, repaired, err := r.verified(ctx, repo, tag, key, cur, link, engine, i == 0)
		e := Entry{Identity: string(key), Repository: repo, Version: tag}
		if err != nil {
			e.Error = err.Error()
			rep.Errors = append(rep.Errors, e)
			if errors.Is(err, errSigning) {
				return nil, nil, err
			}
			continue
		}
		if repaired != "" {
			e.Digest = repaired
			rep.Repaired = append(rep.Repaired, e)
		}
		return b, s, nil
	}
	return nil, nil, nil
}

// errSigning marks a failed signature of an unsigned newest tag.
var errSigning = errors.New("signing the unsigned tag")

// verified pulls repo:tag and returns it with its surface when this
// installation signed it or, for the newest tag, when it signs it now, with
// the digest it signed. The rebuild that decides a repair carries link and
// engine, the OpenAPI link and observed engines this run computed, never
// ones read from the pulled bytes.
func (r *run) verified(ctx context.Context, repo, tag string, key fleet.ExternalKey, cur contract.InferredSurface, link, engine string, newest bool) (*contract.Bundle, *contract.InferredSurface, string, error) {
	reg := r.d.Registry
	h, err := reg.Head(ctx, repo+":"+tag)
	if err != nil || h == nil {
		return nil, nil, "", fmt.Errorf("reading the tag: %v", err)
	}
	b, err := reg.Pull(ctx, repo+"@"+h.Digest)
	if err != nil {
		return nil, nil, "", fmt.Errorf("pulling the tag: %w", err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil || rec.Surface == nil {
		return nil, nil, "", fmt.Errorf("never used: it holds no inferred surface (%v)", err)
	}
	verr := publish.VerifyDigest(ctx, reg, r.d.Verify, repo, h.Digest)
	if verr == nil {
		return b, rec.Surface, "", nil
	}
	refused := fmt.Errorf("never used: this installation did not sign it (%v)", verr)
	if !newest {
		return nil, nil, "", refused
	}
	s := cur
	s.AsOf, s.Completeness = rec.Surface.AsOf, rec.Surface.Completeness
	if rebuilt, err := Bundle(key, s, tag, r.in, link, engine); err != nil || !digestsOf(rebuilt).Same(h.Digest) {
		return nil, nil, "", refused
	}
	if err := publish.SignDigest(ctx, reg, r.d.Signer, repo, h.Digest); err != nil {
		return nil, nil, "", fmt.Errorf("%w: %w", errSigning, err)
	}
	return b, rec.Surface, h.Digest, nil
}

// openAPIOf is the vendor OpenAPI document key's catalog entry links in
// snap, or "".
func openAPIOf(snap *fleet.FleetSnapshot, key fleet.ExternalKey) string {
	if m := snap.Externals[key].Catalog; m != nil {
		return m.OpenAPI
	}
	return ""
}

// keepLink is the OpenAPI link key's new contract records: link, or the
// one prev (a tag this installation signed) records when link is empty.
// Only a complete run whose match comes from an organization catalog removes
// a link: an organization entry replaces the default whole, so one without
// openapi is the organization removing it, while the default catalog (the
// operator's) links nothing and a partial run may have missed the catalog.
func (r *run) keepLink(key fleet.ExternalKey, link string, prev *contract.Bundle) string {
	if link != "" {
		return link
	}
	if m := r.snap.Externals[key].Catalog; !r.partial && m != nil && m.Origin == externalcatalog.OriginOrganization {
		return ""
	}
	return openAPILink(prev)
}

// keepEngine is the observed engines key's new contract records: engine, or
// the ones prev (a tag this installation signed) records when engine is
// empty on a partial run, which may have missed the resources. A complete
// run with no observed engine removes them.
func (r *run) keepEngine(engine string, prev *contract.Bundle) string {
	if engine != "" || !r.partial {
		return engine
	}
	return engineOf(prev)
}

// push writes b to repo, tags it version and signs it, returning its
// digest. A tag already there is never overwritten.
func (r *run) push(ctx context.Context, repo, version string, b *contract.Bundle) (string, error) {
	reg := r.d.Registry
	h, err := reg.Head(ctx, repo+":"+version)
	if err == nil && h != nil {
		err = fmt.Errorf("the tag %s is already there; a tag is never overwritten", version)
	}
	if err != nil {
		return "", err
	}
	digest := digestOf(b)
	hash, _ := v1.NewHash(digest) // digestOf returns an image digest
	if _, err := reg.Write(ctx, repo, withDigest{annotated(b), hash}); err != nil {
		return "", fmt.Errorf("pushing %s: %w", version, err)
	}
	if err := reg.Tag(ctx, repo, digest, version); err != nil {
		return "", fmt.Errorf("tagging %s: %w", version, err)
	}
	if err := publish.SignDigest(ctx, reg, r.d.Signer, repo, digest); err != nil {
		return "", fmt.Errorf("signing %s@%s: %w", version, digest, err)
	}
	return digest, nil
}

// annotated is b's image with the manifest annotation that marks it inferred.
func annotated(b *contract.Bundle) v1.Image {
	img, _ := oci.BundleImage(b) // Bundle always holds a contract and an FS
	return annotate(img)
}

// annotate adds the manifest annotation that marks an image inferred.
func annotate(img v1.Image) v1.Image {
	return mutate.Annotations(img, map[string]string{contract.AnnotationProvenance: contract.ProvenanceInferred}).(v1.Image)
}

// digestOf is the digest b's annotated image is pushed under, computed
// without pushing.
func digestOf(b *contract.Bundle) string { return drained(annotated(b)) }

// digestsOf are both digests b's annotated image can be known by: the typed
// one a push writes now and the legacy one a Pacto before 4.7 wrote, so a
// repair of an older tag of the same content is never refused.
func digestsOf(b *contract.Bundle) oci.Digests {
	legacy, _ := oci.LegacyBundleImage(b) // Bundle always holds a contract and an FS
	return oci.Digests{Typed: digestOf(b), Legacy: drained(annotate(legacy))}
}

// drained is img's digest once its layer stream is read through.
func drained(img v1.Image) string {
	// The bundle is in memory: reading its one layer cannot fail.
	layers, _ := img.Layers()
	rc, _ := layers[0].Compressed()
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	h, _ := img.Digest()
	return h.String()
}

// withDigest is an annotated image with the digest digestOf computed: its
// layer is a stream, so it only knows its own digest once pushed.
type withDigest struct {
	v1.Image
	digest v1.Hash
}

func (w withDigest) Digest() (v1.Hash, error) { return w.digest, nil }
