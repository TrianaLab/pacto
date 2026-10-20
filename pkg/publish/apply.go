package publish

import (
	"context"
	"errors"
	"fmt"
	"os"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// Tools run what Pacto does not do itself. internal/app implements them by
// running docker buildx, the declared command, cosign and syft.
type Tools interface {
	// Build runs a's builder, which writes the artifact to out. env holds
	// PACTO_OUT, PACTO_VERSION, PACTO_REVISION and PACTO_DIGEST_<NAME> /
	// PACTO_REF_<NAME> for every artifact already built.
	Build(ctx context.Context, a contract.PublishArtifact, out string, env []string) error
	// Sign signs ref (repo@digest) unless a valid signature by the configured
	// identity is already there; signed says whether it wrote one.
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
	res := &Result{Plan: p, Actions: []string{}, Digests: map[string]string{}, SBOMs: map[string]string{}}
	if !p.Ready {
		return res, fmt.Errorf("%w: %s", ErrNotReady, refusal(p))
	}
	pub, version := in.Bundle.Contract.Publish, p.Version
	bundleItem := p.Items[len(p.Items)-1]
	bundleDigest := bundleItem.Digest
	bundleRepo := Repo(pub.Bundle)
	env := []string{"PACTO_VERSION=" + version, "PACTO_REVISION=" + in.Revision}
	var tags []func() error
	var deployTag func() error
	for i, a := range pub.Artifacts {
		it, repo := p.Items[i], Repo(a.Ref)
		digest := it.Digest
		if it.State == Absent {
			ann := map[string]string{AnnotationVersion: version, AnnotationRevision: in.Revision}
			if a.Name == pub.Deploy {
				ann[delivery.ContractAnnotation] = contractRef(bundleRepo, bundleDigest)
			}
			var err error
			if digest, err = buildAndPush(ctx, reg, tools, a, env, ann); err != nil {
				return res, fmt.Errorf("%s: %w", a.Name, err)
			}
			res.Actions = append(res.Actions, "pushed "+a.Name+" "+repo+"@"+digest)
			tag := func() error { return reg.Tag(ctx, repo, digest, version) }
			if a.Name == pub.Deploy {
				deployTag = tag
			} else {
				tags = append(tags, tag)
			}
		}
		res.Digests[a.Name] = digest
		env = append(env, "PACTO_DIGEST_"+envName(a.Name)+"="+digest, "PACTO_REF_"+envName(a.Name)+"="+repo)
		if a.SBOM {
			sbom, err := attachSBOM(ctx, reg, tools, repo, digest, version, in.Revision, res)
			if err != nil {
				return res, fmt.Errorf("SBOM of %s: %w", a.Name, err)
			}
			res.SBOMs[a.Name] = sbom
		}
	}
	if bundleItem.State == Absent {
		img, _ := oci.BundleImage(in.Bundle) // packed once already by the plan
		h, _ := v1.NewHash(bundleDigest)
		if _, err := reg.Write(ctx, bundleRepo, streamed{img, h}); err != nil {
			return res, fmt.Errorf("pushing the bundle: %w", err)
		}
		res.Actions = append(res.Actions, "pushed bundle "+bundleRepo+"@"+bundleDigest)
		tags = append(tags, func() error { return reg.Tag(ctx, bundleRepo, bundleDigest, version) })
	}
	res.Digests[BundleItem] = bundleDigest
	if err := signAll(ctx, tools, *pub.Sign, pub, bundleRepo, res); err != nil {
		return res, err
	}
	if deployTag != nil {
		tags = append(tags, deployTag)
	}
	for _, tag := range tags {
		if err := tag(); err != nil {
			return res, fmt.Errorf("tagging: %w", err)
		}
	}
	if len(tags) > 0 {
		res.Actions = append(res.Actions, fmt.Sprintf("tagged %d artifacts %s", len(tags), version))
	}
	if pub.Deploy != "" {
		for _, a := range pub.Artifacts {
			if a.Name == pub.Deploy {
				res.Deploy = Repo(a.Ref) + "@" + res.Digests[a.Name]
			}
		}
	}
	return res, nil
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

// attachSBOM attaches an SBOM to repo@digest unless one is there already, and
// returns the SBOM's digest.
func attachSBOM(ctx context.Context, reg Registry, tools Tools, repo, digest, version, revision string, res *Result) (string, error) {
	have, err := reg.Referrers(ctx, repo, digest, SBOMType)
	if err != nil {
		return "", err
	}
	if len(have) > 0 {
		return have[0], nil
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
