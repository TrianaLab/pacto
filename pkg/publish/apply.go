package publish

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// Tools run what Pacto does not do itself. internal/app implements them by
// running docker buildx or the declared command, reading files and signing
// with sigstore-go.
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
	// SBOM reads the SBOM file the build wrote, at path relative to the
	// contract directory. Pacto attaches and signs it; it never makes one.
	SBOM(path string) ([]byte, error)
}

// ErrNotReady is a publish refused by its plan: a failed check, a conflict or
// an unreadable registry. Nothing was written.
var ErrNotReady = errors.New("publish refused")

// ErrTagRace is a tag another publish wrote while this one ran: found just
// before writing it, or read back right after. Nothing is overwritten.
var ErrTagRace = errors.New("lost a tag to a concurrent publish")

// ErrForeignSignature marks an item whose tag holds this version's
// provenance without a signature by the configured identity.
var ErrForeignSignature = errors.New("not signed by the configured identity")

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
	// Refusal says where a version-rule refusal was kept, signed, or why it
	// could not be. It never changes the publish's outcome.
	Refusal string `json:"refusal,omitempty"`
	// Notes are what deriving the version found worth saying, such as a
	// higher tag it ignored.
	Notes []string `json:"notes,omitempty"`
	// Images are the images this publish deploys, sorted by name, and
	// ImagesRecord the digest of the signed referrer of the bundle that
	// lists them; both empty when it deploys none.
	Images       []ImageRecord `json:"images,omitempty"`
	ImagesRecord string        `json:"imagesRecord,omitempty"`
	// Signer is who signed what this publish wrote, as the caller names it:
	// keyless (<identity>), key <path> or kms <uri>.
	Signer string `json:"signer,omitempty"`
}

// Apply plans, then publishes what is absent: builds it, pushes it by digest,
// attaches SBOMs, signs every digest, pushes the bundle and writes the tags,
// the deploy artifact's last. Its tag is the commit point: once it exists,
// everything it points at exists and is signed.
func Apply(ctx context.Context, reg Registry, tools Tools, in Input) (*Result, error) {
	p := MakePlan(ctx, reg, in)
	if p.Ready {
		adoptOnlySigned(ctx, tools, p.pub, &p)
	}
	res := &Result{Plan: p, Actions: []string{}, Digests: map[string]string{}, SBOMs: map[string]string{}}
	if !p.Ready {
		return res, fmt.Errorf("%w: %s", ErrNotReady, refusal(p))
	}
	r := &applier{ctx: ctx, reg: reg, tools: tools, in: in, res: res, pub: p.pub}
	if err := r.artifacts(); err != nil {
		return res, err
	}
	if err := r.bundle(); err != nil {
		return res, err
	}
	if err := r.images(); err != nil {
		return res, err
	}
	if err := signAll(ctx, tools, SignOf(r.pub), r.pub, Repo(r.pub.Bundle), res); err != nil {
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
		if err := tools.Verify(ctx, SignOf(pub), strings.TrimSuffix(it.Ref, ":"+p.Version)+"@"+it.Digest); err != nil {
			p.Items[i].State, p.Ready = Conflict, false
			p.Items[i].Err = fmt.Errorf("%w: %w", ErrForeignSignature, err)
			p.Items[i].Reason = "the tag holds " + it.Digest + " with this version's provenance, but no signature by the configured identity, " +
				"so Pacto did not publish it; it is never signed or overwritten (" + err.Error() + ")" + NextVersion
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
			if r.in.Created != "" {
				ann[AnnotationCreated] = r.in.Created
			}
			if r.in.Source != "" {
				ann[AnnotationSource] = r.in.Source
			}
			if a.Name == r.pub.Deploy {
				ann[delivery.ContractAnnotation] = contractRef(bundleRepo, bundleDigest)
			}
			var err error
			if digest, err = buildAndPush(r.ctx, r.reg, r.tools, a, r.in.Builtin[a.Name], env, ann); err != nil {
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
		if a.SBOM == "" {
			continue
		}
		sbom, err := attachSBOM(r.ctx, r.reg, r.tools, SignOf(r.pub), a.SBOM, repo, digest, version, r.in.Revision, r.res)
		if err != nil {
			return fmt.Errorf("SBOM of %s: %w", a.Name, err)
		}
		r.res.SBOMs[a.Name] = sbom
	}
	return nil
}

// owe records a tag to write once everything is signed. The tag is read again
// just before it is written, so a publish racing this one is refused in the
// moment between the read and the write, not over the whole build, and read
// back right after, so a write landing in that moment is seen. Both are
// ErrTagRace.
func (r *applier) owe(deploy bool, repo, digest string) {
	tag := func() error {
		ref := repo + ":" + r.res.Plan.Version
		now, err := r.reg.Head(r.ctx, ref)
		switch {
		case err != nil:
			return err
		case now == nil:
			if err := r.reg.Tag(r.ctx, repo, digest, r.res.Plan.Version); err != nil {
				return err
			}
			back, err := r.reg.Head(r.ctx, ref)
			if err != nil {
				return fmt.Errorf("reading %s back: %w", ref, err)
			}
			if back == nil || back.Digest != digest {
				return fmt.Errorf("%w: %s no longer holds %s right after this publish tagged it; another publish wrote it meanwhile", ErrTagRace, ref, digest)
			}
		case now.Digest != digest:
			return fmt.Errorf("%w: %s now holds %s, written while this publish ran; it is never overwritten"+NextVersion, ErrTagRace, ref, now.Digest)
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
	// The bundle is read from the contract directory again, after the builds.
	// A build that wrote into it (an SBOM, say) changed the digest the
	// artifacts were annotated with.
	if d, err := oci.BundleDigest(r.in.Bundle); d != it.Digest {
		return errors.Join(fmt.Errorf("the contract directory changed while publishing (planned bundle %s, now %q); "+
			"list build outputs such as the SBOM in .pactoignore", it.Digest, d), err)
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

// buildAndPush runs a's builder (bi when set, else tools) into a fresh
// directory, loads what it wrote and pushes it by digest. The directory lives
// until the push is done: an image layout is read lazily.
func buildAndPush(ctx context.Context, reg Registry, tools Tools, a contract.PublishArtifact, bi Builtin, env []string, ann map[string]string) (string, error) {
	out, err := os.MkdirTemp("", "pacto-publish-"+a.Name+"-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(out) }()
	env = append(env, "PACTO_OUT="+out)
	if bi == nil {
		bi = func(ctx context.Context, out string, env []string) error { return tools.Build(ctx, a, out, env) }
	}
	if err := bi(ctx, out, env); err != nil {
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

// attachSBOM attaches the SBOM file at path to repo@digest unless an SBOM of
// its format signed by the configured identity is there already, and returns
// the SBOM's digest. An SBOM anyone else attached is left alone and never
// signed.
func attachSBOM(ctx context.Context, reg Registry, tools Tools, sign contract.PublishSign, path, repo, digest, version, revision string, res *Result) (string, error) {
	mt := sbomType(path)
	body := func() ([]byte, error) {
		sbom, err := tools.SBOM(path)
		if err != nil {
			return nil, err
		}
		return sbom, checkSBOM(path, mt, sbom)
	}
	return attachReferrer(ctx, reg, tools, sign, referrer{mt, "the SBOM", body}, repo, digest, version, revision, res)
}

// referrer is what attachReferrer attaches: its artifact type, what it is
// called in the actions, and its body, read only when it is written.
type referrer struct {
	artifactType, what string
	body               func() ([]byte, error)
}

// attachReferrer attaches ref to repo@digest unless a referrer of its type
// signed by the configured identity is there already, and returns the
// referrer's digest. One anyone else attached is left alone and never
// signed.
func attachReferrer(ctx context.Context, reg Registry, tools Tools, sign contract.PublishSign, ref referrer, repo, digest, version, revision string, res *Result) (string, error) {
	have, err := reg.Referrers(ctx, repo, digest, ref.artifactType)
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
	body, err := ref.body()
	if err != nil {
		return "", err
	}
	mode, err := reg.Mode(ctx, repo, digest)
	if err != nil {
		return "", err
	}
	h, _ := v1.NewHash(digest)
	subject := v1.Descriptor{MediaType: types.MediaType(head.MediaType), Size: head.Size, Digest: h}
	ann := map[string]string{AnnotationVersion: version, AnnotationRevision: revision}
	img, art := referrerArtifact(ref.artifactType, body, subject, ann)
	if err := writeReferrer(ctx, reg, mode, repo, digest, img, art); err != nil {
		return "", err
	}
	d := art.ManifestDesc.Digest.String()
	res.Actions = append(res.Actions, "attached "+ref.what+" "+repo+"@"+d)
	return d, nil
}

// images attaches the images record to the bundle: every image this
// publish deploys, built (each image artifact) or supplied (Input.Images),
// with its digest and where it came from, sorted by name so the same
// publish always writes the same record. A publish with no image writes
// none.
func (r *applier) images() error {
	imgs := slices.Clone(r.in.Images)
	for _, a := range r.pub.Artifacts {
		if a.Type == contract.ArtifactImage {
			imgs = append(imgs, ImageRecord{Name: a.Name, Repository: Repo(a.Ref), Digest: r.res.Digests[a.Name], Source: SourceBuilt})
		}
	}
	if len(imgs) == 0 {
		return nil
	}
	slices.SortFunc(imgs, func(a, b ImageRecord) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Digest, b.Digest))
	})
	body, _ := json.Marshal(imagesRecord{imgs})
	read := func() ([]byte, error) { return body, nil }
	d, err := attachReferrer(r.ctx, r.reg, r.tools, SignOf(r.pub), referrer{ImagesType, "the images record", read},
		Repo(r.pub.Bundle), r.res.Digests[BundleItem], r.res.Plan.Version, r.in.Revision, r.res)
	if err != nil {
		return fmt.Errorf("the images record: %w", err)
	}
	r.res.Images, r.res.ImagesRecord = imgs, d
	return nil
}

// signAll signs every published digest: artifacts, SBOMs, the bundle and
// its images record.
func signAll(ctx context.Context, tools Tools, sign contract.PublishSign, pub *contract.Publish, bundleRepo string, res *Result) error {
	var refs []string // each artifact, its SBOM if any, then the bundle
	for _, a := range pub.Artifacts {
		refs = append(refs, Repo(a.Ref)+"@"+res.Digests[a.Name])
		if sbom := res.SBOMs[a.Name]; sbom != "" {
			refs = append(refs, Repo(a.Ref)+"@"+sbom)
		}
	}
	refs = append(refs, bundleRepo+"@"+res.Digests[BundleItem])
	if res.ImagesRecord != "" {
		refs = append(refs, bundleRepo+"@"+res.ImagesRecord)
	}
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
