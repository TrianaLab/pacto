package publish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestLayout(t *testing.T) {
	for _, c := range []struct{ image, bundle, img, alt string }{
		{"", "r.io/acme/pay", "r.io/acme/images/pay", "r.io/acme/contracts/pay"},
		{"r.io/acme/pay", "r.io/acme/contracts/pay", "r.io/acme/pay", "r.io/acme/pay"},
		{"r.io/acme/team/api", "r.io/acme/pay", "r.io/acme/team/api", "r.io/acme/contracts/pay"},
		{"other.io/x/pay", "r.io/acme/pay", "r.io/acme/images/pay", "r.io/acme/contracts/pay"},
		{"docker.io/library/nginx", "docker.io/library/pay", "docker.io/library/images/pay", "docker.io/library/contracts/pay"},
	} {
		reg := "r.io/acme"
		if strings.HasPrefix(c.image, "docker.io/") {
			reg = "docker.io/library"
		}
		b, img, d := Layout(reg, "pay", c.image)
		got := line(b, img, d, AltLayout(reg, "pay", c.image))
		expect(t, got == line(c.bundle, c.img, reg+"/deploy/pay", c.alt), "%q: %s", c.image, got)
	}
}

// derivedSection is a section Pacto derived for a contract that declares none.
func derivedSection(host string) *contract.Publish {
	return &contract.Publish{Bundle: "oci://" + host + "/pacto/payments",
		Artifacts: []contract.PublishArtifact{{Name: "image", Type: contract.ArtifactImage, Ref: "oci://" + host + "/images/payments"}}}
}

// sectionless is bundleOf at version with no publish section.
func sectionless(host, version string) *contract.Bundle {
	b := bundleOf(host, version, workloadService)
	b.Contract.Publish = nil
	return b
}

// TestMakePlanDerivedPublishVersionRule: with no declared section, the plan
// judges the version against the derived bundle repository.
func TestMakePlanDerivedPublishVersionRule(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	pushBundle(t, reg, host, "1.0.0", bundleOf(host, "1.0.0", workloadService))
	p := MakePlan(ctx, reg, Input{Bundle: sectionless(host, "1.0.1"), Revision: "r1", Publish: derivedSection(host)})
	ch := p.Checks[len(p.Checks)-1]
	expect(t, ch.Name == "version" && ch.Passed && strings.Contains(ch.Message, "since 1.0.0") && p.Baseline == "1.0.0", "version: %+v", ch)
	expect(t, line(states(p)) == "image=absent bundle=absent", "items: %s", states(p))
	p = MakePlan(ctx, reg, Input{Bundle: sectionless(host, "1.0.1"), Revision: "r1"})
	expect(t, strings.Contains(p.Checks[0].Message, "pacto ci --publish derives one"), "no section: %+v", p.Checks)
}

// TestBuiltinReplacesTools: an artifact with a builtin is built by it, with
// the commit time stamped, and never reaches Tools.Build.
func TestBuiltinReplacesTools(t *testing.T) {
	host, reg := newRegistry(t)
	tools := newTools()
	var env []string
	bi := func(ctx context.Context, out string, e []string) error {
		env = e
		return tools.Build(ctx, contract.PublishArtifact{Name: "x", Type: contract.ArtifactImage}, out, e)
	}
	in := Input{Bundle: sectionless(host, "1.0.0"), Revision: "r1", Publish: derivedSection(host),
		Builtin: map[string]Builtin{"image": bi}, Created: "2026-11-01T00:00:00Z"}
	res, err := Apply(context.Background(), reg, tools, in)
	expect(t, err == nil && tools.builds["image"] == 0 && tools.builds["x"] == 1, "builtin: %v %v", err, tools.builds)
	expect(t, strings.Contains(strings.Join(env, " "), "PACTO_OUT="), "env: %v", env)
	h, _ := reg.Head(context.Background(), host+"/images/payments@"+res.Digests["image"])
	expect(t, h.Annotations[AnnotationCreated] == "2026-11-01T00:00:00Z", "annotations: %v", h.Annotations)
}

// swapRegistry writes other to the image tag once: just before this
// publish's pre-write read of it when before is set, else right after its
// tag write.
type swapRegistry struct {
	*Client
	other  string
	before bool
	heads  int
}

func (s *swapRegistry) swap(ctx context.Context, repo string) {
	_ = s.Client.Tag(ctx, repo, s.other, "1.0.0")
}

func (s *swapRegistry) Head(ctx context.Context, ref string) (*Remote, error) {
	if repo, ok := strings.CutSuffix(ref, ":1.0.0"); ok && s.before && strings.HasSuffix(repo, "/images/payments") {
		if s.heads++; s.heads == 2 { // the plan's read, then owe's
			s.swap(ctx, repo)
		}
	}
	return s.Client.Head(ctx, ref)
}

func (s *swapRegistry) Tag(ctx context.Context, repo, digest, tag string) error {
	err := s.Client.Tag(ctx, repo, digest, tag)
	if !s.before && strings.HasSuffix(repo, "/images/payments") {
		s.swap(ctx, repo)
	}
	return err
}

// TestOweReadsTheTagBack: a tag swapped just before or right after this
// publish writes it is ErrTagRace, and the deploy tag is never written; a
// read-back that fails is an error.
func TestOweReadsTheTagBack(t *testing.T) {
	for _, before := range []bool{true, false} {
		host, client := newRegistry(t)
		ctx := context.Background()
		other, _ := random.Image(8, 1)
		od, _ := client.Write(ctx, host+"/images/payments", other)
		pub := derivedSection(host)
		pub.Deploy = "manifests"
		pub.Artifacts = append(pub.Artifacts, contract.PublishArtifact{Name: "manifests", Type: contract.ArtifactManifests, Ref: "oci://" + host + "/deploy/payments"})
		_, err := Apply(ctx, &swapRegistry{Client: client, other: od, before: before}, newTools(), Input{Bundle: sectionless(host, "1.0.0"), Revision: "r1", Publish: pub})
		expect(t, errors.Is(err, ErrTagRace), "before=%v: %v", before, err)
		h, _ := client.Head(ctx, host+"/deploy/payments:1.0.0")
		expect(t, h == nil, "before=%v: the deploy tag was written", before)
	}
	host, client := newRegistry(t)
	img, _ := random.Image(8, 1)
	d, _ := client.Write(context.Background(), host+"/x", img)
	r := &applier{ctx: context.Background(), reg: &headAfterTag{Client: client}, res: &Result{Plan: Plan{Version: "1.0.0"}}}
	r.owe(false, host+"/x", d)
	err := r.tags[0]()
	expect(t, err != nil && strings.Contains(err.Error(), "reading "+host+"/x:1.0.0 back"), "read-back error: %v", err)
}

// headAfterTag fails every Head once a tag was written.
type headAfterTag struct {
	*Client
	tagged bool
}

func (h *headAfterTag) Head(ctx context.Context, ref string) (*Remote, error) {
	if h.tagged {
		return nil, errTest
	}
	return h.Client.Head(ctx, ref)
}

func (h *headAfterTag) Tag(ctx context.Context, repo, digest, tag string) error {
	h.tagged = true
	return h.Client.Tag(ctx, repo, digest, tag)
}

// TestAdoptOnlySignedForeignSignatureSentinel: an adopted tag no one Pacto
// trusts signed is a conflict marked ErrForeignSignature.
func TestAdoptOnlySignedForeignSignatureSentinel(t *testing.T) {
	p := Plan{Version: "1.0.0", Ready: true, Items: []Item{{Name: "api", Ref: "r/api:1.0.0", State: Adopt, Digest: "sha256:x"}}}
	adoptOnlySigned(context.Background(), newTools(), nil, &p)
	expect(t, p.Items[0].State == Conflict && errors.Is(p.Items[0].Err, ErrForeignSignature) && !p.Ready, "item: %+v", p.Items[0])
}

// TestLoadImageTarball: a docker-save tarball loads with the annotations; a
// file that is not one is refused by name.
func TestLoadImageTarball(t *testing.T) {
	dir := t.TempDir()
	img, _ := random.Image(16, 1)
	tag, _ := name.NewTag("example.com/x:1")
	expect(t, tarball.WriteToFile(filepath.Join(dir, "image.tar"), tag, img) == nil, "write")
	a, err := load(contract.ArtifactImage, dir, map[string]string{"k": "v"})
	expect(t, err == nil, "load: %v", err)
	m, _ := a.(v1.Image).Manifest()
	expect(t, m.Annotations["k"] == "v", "annotations: %v", m.Annotations)
	bad := t.TempDir()
	_ = os.WriteFile(filepath.Join(bad, "image.tar"), []byte("nope"), 0o644)
	_, err = load(contract.ArtifactImage, bad, nil)
	expect(t, err != nil && strings.Contains(err.Error(), "image.tar"), "bad tarball: %v", err)
}
