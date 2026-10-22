package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// Tools run what Pacto does not do itself. internal/app implements them by
// running docker buildx, the declared command and syft, and by signing with
// sigstore-go.
type Tools interface {
	// Build runs a's builder, which writes the artifact to out. env holds
	// PACTO_OUT, PACTO_VERSION, PACTO_REVISION and PACTO_DIGEST_<NAME> /
	// PACTO_REF_<NAME> for every artifact already built.
	Build(ctx context.Context, a contract.PublishArtifact, out string, env []string) error
	// Verify returns nil when ref (repo@digest) carries a valid signature by
	// the configured identity.
	Verify(ctx context.Context, sign contract.PublishSign, ref string) error
	// Sign signs ref (repo@digest) unless Verify passes; signed says whether
	// it wrote one.
	Sign(ctx context.Context, sign contract.PublishSign, ref string) (signed bool, err error)
	// SBOM makes the SPDX JSON SBOM of the image at ref (repo@digest).
	SBOM(ctx context.Context, ref string) ([]byte, error)
}

// ErrNotReady is a publish refused by its plan: a failed check, a conflict or
// an unreadable registry. Nothing was written.
var ErrNotReady = errors.New("publish refused")

// Result is what a publish did.
type Result struct {
	Plan Plan `json:"plan"`
	// Actions lists every registry write, in order. Empty means nothing new
	// was published.
	Actions []string `json:"actions"`
	// Digests maps every item name to the digest published under it.
	Digests map[string]string `json:"digests"`
	// SBOMs maps an image artifact's name to its SBOM referrer's digest.
	SBOMs map[string]string `json:"sboms,omitempty"`
	// Deploy is the deploy artifact as repo@digest, when there is one.
	Deploy string `json:"deploy,omitempty"`
}

// Apply plans, then publishes what is absent: builds it, pushes it by digest,
// attaches SBOMs, signs every digest, pushes the bundle and writes the tags,
// the deploy artifact's last. Its tag is the commit point: once it exists,
// everything it points at exists and is signed.
func Apply(ctx context.Context, reg Registry, tools Tools, in Input) (*Result, error) {
	p := MakePlan(ctx, reg, in)
	if p.Ready {
		adoptOnlySigned(ctx, tools, in.Bundle.Contract.Publish, &p)
	}
	res := &Result{Plan: p, Actions: []string{}, Digests: map[string]string{}, SBOMs: map[string]string{}}
	if !p.Ready {
		return res, fmt.Errorf("%w: %s", ErrNotReady, refusal(p))
	}
	r := &applier{ctx: ctx, reg: reg, tools: tools, in: in, res: res, pub: in.Bundle.Contract.Publish}
	if err := r.artifacts(); err != nil {
		return res, err
	}
	if err := r.bundle(); err != nil {
		return res, err
	}
	if err := signAll(ctx, tools, *r.pub.Sign, r.pub, Repo(r.pub.Bundle), res); err != nil {
		return res, err
	}
	// The deploy artifact's tag goes last: it is the commit point.
	tags := r.tags
	if r.deployTag != nil {
		tags = append(tags, r.deployTag)
	}
	for _, tag := range tags {
		if err := tag(); err != nil {
			return res, fmt.Errorf("tagging: %w", err)
		}
	}
	if len(tags) > 0 {
		res.Actions = append(res.Actions, fmt.Sprintf("tagged %d artifacts %s", len(tags), p.Version))
	}
	return res, nil
}

// adoptOnlySigned turns an adopted tag that carries no signature by the
// configured identity into a conflict. Provenance annotations are unsigned:
// anyone who can push can write them, and a tag Pacto wrote was signed before
// it was tagged. Signing such a tag would sign someone else's content.
func adoptOnlySigned(ctx context.Context, tools Tools, pub *contract.Publish, p *Plan) {
	for i, it := range p.Items {
		if it.State != Adopt {
			continue
		}
		if err := tools.Verify(ctx, *pub.Sign, strings.TrimSuffix(it.Ref, ":"+p.Version)+"@"+it.Digest); err != nil {
			p.Items[i].State, p.Ready = Conflict, false
			p.Items[i].Reason = "the tag holds " + it.Digest + " with this version's provenance, but no signature by the configured identity, " +
				"so Pacto did not publish it; it is never signed or overwritten (" + err.Error() + ")"
		}
	}
}

// applier carries one publish: what it pushed and the tags it still owes.
type applier struct {
	ctx       context.Context
	reg       Registry
	tools     Tools
	in        Input
	res       *Result
	pub       *contract.Publish
	tags      []func() error
	deployTag func() error
}

// artifacts builds and pushes every absent artifact in declaration order,
// reusing what is already there, and attaches the SBOMs.
func (r *applier) artifacts() error {
	p, version := r.res.Plan, r.res.Plan.Version
	bundleRepo, bundleDigest := Repo(r.pub.Bundle), p.Items[len(p.Items)-1].Digest
	env := []string{"PACTO_VERSION=" + version, "PACTO_REVISION=" + r.in.Revision}
	for i, a := range r.pub.Artifacts {
		repo, digest := Repo(a.Ref), p.Items[i].Digest
		if p.Items[i].State == Absent {
			ann := map[string]string{AnnotationVersion: version, AnnotationRevision: r.in.Revision}
			if a.Name == r.pub.Deploy {
				ann[delivery.ContractAnnotation] = contractRef(bundleRepo, bundleDigest)
			}
			var err error
			if digest, err = buildAndPush(r.ctx, r.reg, r.tools, a, env, ann); err != nil {
				return fmt.Errorf("%s: %w", a.Name, err)
			}
			r.res.Actions = append(r.res.Actions, "pushed "+a.Name+" "+repo+"@"+digest)
			r.owe(a.Name == r.pub.Deploy, repo, digest)
		}
		r.res.Digests[a.Name] = digest
		if a.Name == r.pub.Deploy {
			r.res.Deploy = repo + "@" + digest
		}
		env = append(env, "PACTO_DIGEST_"+envName(a.Name)+"="+digest, "PACTO_REF_"+envName(a.Name)+"="+repo)
		if !a.SBOM {
			continue
		}
		sbom, err := attachSBOM(r.ctx, r.reg, r.tools, *r.pub.Sign, repo, digest, version, r.in.Revision, r.res)
		if err != nil {
			return fmt.Errorf("SBOM of %s: %w", a.Name, err)
		}
		r.res.SBOMs[a.Name] = sbom
	}
	return nil
}

// owe records a tag to write once everything is signed. The tag is read again
// just before it is written, so a publish racing this one is refused in the
// moment between the read and the write, not over the whole build.
func (r *applier) owe(deploy bool, repo, digest string) {
	tag := func() error {
		ref := repo + ":" + r.res.Plan.Version
		now, err := r.reg.Head(r.ctx, ref)
		switch {
		case err != nil:
			return err
		case now == nil:
			return r.reg.Tag(r.ctx, repo, digest, r.res.Plan.Version)
		case now.Digest != digest:
			return fmt.Errorf("%s now holds %s, written while this publish ran; it is never overwritten", ref, now.Digest)
		}
		return nil
	}
	if deploy {
		r.deployTag = tag
		return
	}
	r.tags = append(r.tags, tag)
}

// bundle pushes the contract bundle by the digest the plan computed.
func (r *applier) bundle() error {
	it := r.res.Plan.Items[len(r.res.Plan.Items)-1]
	repo := Repo(r.pub.Bundle)
	r.res.Digests[BundleItem] = it.Digest
	if it.State != Absent {
		return nil
	}
	img, _ := oci.BundleImage(r.in.Bundle) // packed once already by the plan
	h, _ := v1.NewHash(it.Digest)
	if _, err := r.reg.Write(r.ctx, repo, streamed{img, h}); err != nil {
		return fmt.Errorf("pushing the bundle: %w", err)
	}
	r.res.Actions = append(r.res.Actions, "pushed bundle "+repo+"@"+it.Digest)
	r.owe(false, repo, it.Digest)
	return nil
}

// streamed is the bundle image with the digest the plan computed: its layer
// is a stream, so it only knows its own digest once pushed.
type streamed struct {
	v1.Image
	digest v1.Hash
}

func (s streamed) Digest() (v1.Hash, error) { return s.digest, nil }

// refusal says why a plan is not ready.
func refusal(p Plan) string {
	for _, c := range p.Checks {
		if !c.Passed {
			return c.Name + ": " + c.Message
		}
	}
	for _, it := range p.Items {
		if it.State == Conflict || it.State == Unreadable {
			return it.Ref + " is " + string(it.State) + ": " + it.Reason
		}
	}
	return "not ready"
}

// buildAndPush runs a's builder into a fresh directory, loads what it wrote
// and pushes it by digest. The directory lives until the push is done: an
// image layout is read lazily.
func buildAndPush(ctx context.Context, reg Registry, tools Tools, a contract.PublishArtifact, env []string, ann map[string]string) (string, error) {
	out, err := os.MkdirTemp("", "pacto-publish-"+a.Name+"-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(out) }()
	if err := tools.Build(ctx, a, out, append(env, "PACTO_OUT="+out)); err != nil {
		return "", err
	}
	art, err := load(a.Type, out, ann)
	if err != nil {
		return "", err
	}
	d, err := reg.Write(ctx, Repo(a.Ref), art)
	if err != nil {
		return "", fmt.Errorf("pushing: %w", err)
	}
	return d, nil
}

// attachSBOM attaches an SBOM to repo@digest unless one signed by the
// configured identity is there already, and returns the SBOM's digest. An
// SBOM anyone else attached is left alone and never signed.
func attachSBOM(ctx context.Context, reg Registry, tools Tools, sign contract.PublishSign, repo, digest, version, revision string, res *Result) (string, error) {
	have, err := reg.Referrers(ctx, repo, digest, SBOMType)
	if err != nil {
		return "", err
	}
	for _, h := range have {
		if tools.Verify(ctx, sign, repo+"@"+h) == nil {
			return h, nil
		}
	}
	head, err := reg.Head(ctx, repo+"@"+digest)
	if err != nil || head == nil {
		return "", fmt.Errorf("reading %s@%s back: %v", repo, digest, err)
	}
	sbom, err := tools.SBOM(ctx, repo+"@"+digest)
	if err != nil {
		return "", err
	}
	h, _ := v1.NewHash(digest)
	subject := v1.Descriptor{MediaType: types.MediaType(head.MediaType), Size: head.Size, Digest: h}
	ann := map[string]string{AnnotationVersion: version, AnnotationRevision: revision}
	d, err := reg.Write(ctx, repo, sbomArtifact(sbom, subject, ann))
	if err != nil {
		return "", err
	}
	res.Actions = append(res.Actions, "attached the SBOM "+repo+"@"+d)
	return d, nil
}

// signAll signs every published digest: artifacts, SBOMs and the bundle.
func signAll(ctx context.Context, tools Tools, sign contract.PublishSign, pub *contract.Publish, bundleRepo string, res *Result) error {
	refs := make([]string, 0, len(pub.Artifacts)+1)
	for _, a := range pub.Artifacts {
		refs = append(refs, Repo(a.Ref)+"@"+res.Digests[a.Name])
		if sbom := res.SBOMs[a.Name]; sbom != "" {
			refs = append(refs, Repo(a.Ref)+"@"+sbom)
		}
	}
	refs = append(refs, bundleRepo+"@"+res.Digests[BundleItem])
	for _, ref := range refs {
		signed, err := tools.Sign(ctx, sign, ref)
		if err != nil {
			return fmt.Errorf("signing %s: %w", ref, err)
		}
		if signed {
			res.Actions = append(res.Actions, "signed "+ref)
		}
	}
	return nil
}
