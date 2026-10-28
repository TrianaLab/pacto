package publish

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

var errTest = errors.New("injected")

// fakeTools builds, signs and reads SBOMs in memory, counting each.
type fakeTools struct {
	builds    map[string]int
	signed    map[string]bool
	failBuild string
	failSign  bool
	failSBOM  bool
	sbom      []byte       // the SBOM file's content; nil is a valid SPDX one
	dirty     fstest.MapFS // a contract directory every build writes into
	nothing   bool         // write nothing
	index     bool         // build images as multi-platform indexes
	envs      [][]string
}

func newTools() *fakeTools { return &fakeTools{builds: map[string]int{}, signed: map[string]bool{}} }

func (f *fakeTools) Build(_ context.Context, a contract.PublishArtifact, out string, env []string) error {
	f.builds[a.Name]++
	f.envs = append(f.envs, env)
	if f.dirty != nil {
		f.dirty["sbom.spdx.json"] = &fstest.MapFile{Data: []byte(a.Name)}
	}
	if a.Name == f.failBuild {
		return errTest
	}
	if f.nothing {
		return nil
	}
	switch a.Type {
	case contract.ArtifactImage:
		p, err := layout.Write(out, empty.Index)
		if err != nil {
			return err
		}
		if f.index {
			idx, _ := random.Index(64, 1, 2)
			return p.AppendIndex(idx)
		}
		img, _ := random.Image(64, 1)
		return p.AppendImage(img)
	case contract.ArtifactChart:
		return os.WriteFile(filepath.Join(out, "payments-1.0.0.tgz"), chartTGZ(map[string]string{"payments/Chart.yaml": "name: payments\nversion: 1.0.0\n"}), 0o644)
	}
	var digests []string
	for _, e := range env {
		if strings.HasPrefix(e, "PACTO_DIGEST_") {
			digests = append(digests, e)
		}
	}
	return os.WriteFile(filepath.Join(out, "deployment.yaml"), []byte(strings.Join(digests, "\n")), 0o644)
}

func (f *fakeTools) Verify(_ context.Context, _ contract.PublishSign, ref string) error {
	if !f.signed[ref] {
		return errTest
	}
	return nil
}

func (f *fakeTools) Sign(_ context.Context, _ contract.PublishSign, ref string) (bool, error) {
	if f.failSign {
		return false, errTest
	}
	was := f.signed[ref]
	f.signed[ref] = true
	return !was, nil
}

func (f *fakeTools) SBOM(string) ([]byte, error) {
	if f.failSBOM {
		return nil, errTest
	}
	if f.sbom != nil {
		return f.sbom, nil
	}
	return []byte(`{"spdxVersion":"SPDX-2.3"}`), nil
}

func chartTGZ(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// anonymous is a keychain that never asks the machine's docker credential
// helper: the default one runs it for every registry call, which made these
// tests take minutes.
var anonymous = authn.NewMultiKeychain()

func newRegistry(t *testing.T) (string, *Client) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return host, NewClient(anonymous, host)
}

const workloadService = "service"

// bundleOf is a 2.1 contract for payments at version, publishing an image
// with an SBOM, a chart and the deploy manifests to host.
func bundleOf(host, version, workload string) *contract.Bundle {
	y := `pactoVersion: "2.1"
service: {name: payments, version: ` + version + `}
workload: ` + workload + `
publish:
  bundle: oci://` + host + `/pacto/payments
  deploy: manifests
  artifacts:
    - {name: api, type: image, ref: "oci://` + host + `/payments", sbom: sbom/api.spdx.json, build: {command: [x]}}
    - {name: chart, type: chart, ref: "oci://` + host + `/charts/payments", build: {command: [x]}}
    - {name: manifests, type: manifests, ref: "oci://` + host + `/deploy/payments", build: {command: [x]}}
  sign: {key: cosign.key}
`
	c, err := contract.Parse(strings.NewReader(y))
	if err != nil {
		panic(err)
	}
	return &contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{"pacto.yaml": {Data: []byte(y)}}}
}

func states(p Plan) string {
	var s []string
	for _, it := range p.Items {
		s = append(s, it.Name+"="+string(it.State))
	}
	return strings.Join(s, " ")
}

// TestApplyPublishesOnceAndResumes is the step-3 list: the plan lists what is
// missing and writes nothing, the publish writes it with the deploy artifact
// naming its contract, and running it again changes nothing.
// line joins values with spaces, for comparing several facts at once.
func line(vals ...any) string { return strings.TrimSpace(fmt.Sprintln(vals...)) }

// expect fails the test with the message unless ok.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func TestApplyPublishesOnceAndResumes(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	in := Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123", Checks: []Check{{Name: "validation", Passed: true}}}
	p := MakePlan(ctx, reg, in)
	got := line(p.Ready, p.Missing, states(p))
	expect(t, got == "true 4 api=absent chart=absent manifests=absent bundle=absent", "plan: %s", got)
	tags, _ := reg.Tags(ctx, host+"/payments")
	expect(t, len(tags) == 0, "the plan wrote: %v", tags)
	tools := newTools()
	res, err := Apply(ctx, reg, tools, in)
	expect(t, err == nil, "publish: %v", err)
	got = line(res.Deploy != "", len(res.SBOMs), len(tools.signed))
	expect(t, got == "true 1 5", "result: %+v signed %v", res, tools.signed)
	deploy, _ := reg.Head(ctx, host+"/deploy/payments:1.0.0")
	ann := deploy.Annotations
	got = strings.Join([]string{ann[delivery.ContractAnnotation], ann[AnnotationRevision], ann[AnnotationVersion]}, " ")
	expect(t, got == "oci://"+host+"/pacto/payments@"+res.Digests[BundleItem]+" abc123 1.0.0", "deploy annotations: %v", ann)
	env := strings.Join(tools.envs[2], " ")
	expect(t, strings.Contains(env, "PACTO_DIGEST_API="+res.Digests["api"]), "the manifests build saw %s", env)
	expect(t, strings.Contains(env, "PACTO_REF_CHART="+host+"/charts/payments"), "the manifests build saw %s", env)
	again, err := Apply(ctx, reg, tools, in)
	expect(t, err == nil, "second publish: %v", err)
	got = line(len(again.Actions), states(again.Plan), again.Deploy == res.Deploy, tools.builds["api"])
	expect(t, got == "0 api=adopt chart=adopt manifests=adopt bundle=identical true 1", "the second publish changed something: %s", got)
}

// failingTags fails the tag write of one repository.
type failingTags struct {
	*Client
	repo string
}

func (f failingTags) Tag(ctx context.Context, repo, digest, tag string) error {
	if repo == f.repo {
		return errTest
	}
	return f.Client.Tag(ctx, repo, digest, tag)
}

// TestApplyFinishesWhatStopped: a publish that stopped before its deploy tag
// finishes on the next run, reusing what it already pushed.
func TestApplyFinishesWhatStopped(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	in := Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123"}
	tools := newTools()
	if _, err := Apply(ctx, failingTags{reg, host + "/deploy/payments"}, tools, in); err == nil {
		t.Fatal("the deploy tag failure must surface")
	}
	if r, _ := reg.Head(ctx, host+"/deploy/payments:1.0.0"); r != nil {
		t.Fatal("the deploy tag is the commit point; it must not exist")
	}
	// Only the same revision finishes it. A run from another commit finds
	// tags it did not compute: the version is spent, and the bundle tag is
	// already public, though no Stage takes it without the deploy artifact.
	other := Input{Bundle: in.Bundle, Revision: "def456"}
	if p := MakePlan(ctx, reg, other); p.Ready || states(p) != "api=conflict chart=conflict manifests=absent bundle=identical" ||
		!strings.Contains(p.Items[0].Reason, "(version 1.0.0, revision abc123); it is never overwritten: publish a higher service.version") {
		t.Fatalf("a resume from another revision: %s %+v", states(p), p.Items[0])
	}
	p := MakePlan(ctx, reg, in)
	if states(p) != "api=adopt chart=adopt manifests=absent bundle=identical" {
		t.Fatalf("resume plan: %s", states(p))
	}
	res, err := Apply(ctx, reg, tools, in)
	if err != nil || res.Deploy == "" || tools.builds["api"] != 1 || tools.builds["manifests"] != 2 {
		t.Fatalf("resume: %v %+v %v", err, res, tools.builds)
	}
}

// TestNeverOverwrites: a tag holding something publish did not compute is a
// conflict, and nothing is written.
func TestNeverOverwrites(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	img, _ := random.Image(64, 1)
	d, _ := reg.Write(ctx, host+"/payments", img)
	if err := reg.Tag(ctx, host+"/payments", d, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	in := Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123"}
	res, err := Apply(ctx, reg, newTools(), in)
	if !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "conflict") || res.Plan.Items[0].State != Conflict {
		t.Fatalf("want a conflict: %v %s", err, states(res.Plan))
	}
	if r, _ := reg.Head(ctx, host+"/pacto/payments:1.0.0"); r != nil {
		t.Fatal("a refused publish wrote the bundle")
	}
	// A bundle tag with other content is a conflict too.
	in2 := Input{Bundle: bundleOf(host, "2.0.0", workloadService), Revision: "abc123"}
	other := bundleOf(host, "2.0.0", "job")
	img2, _ := oci.BundleImage(other)
	od, _ := oci.BundleDigest(other)
	h, _ := v1.NewHash(od)
	bd, err := reg.Write(ctx, host+"/pacto/payments", streamed{img2, h})
	if err != nil {
		t.Fatal(err)
	}
	_ = reg.Tag(ctx, host+"/pacto/payments", bd, "2.0.0")
	p := MakePlan(ctx, reg, in2)
	if p.Items[3].State != Conflict {
		t.Fatalf("bundle: %s", states(p))
	}
	// The refusal names the next step, and a bundle, which carries no
	// provenance, does not print empty fields that read as corruption.
	if why := p.Items[3].Reason; !strings.HasSuffix(why, "never overwritten: publish a higher service.version") || strings.Contains(why, "(") {
		t.Fatalf("bundle conflict reason: %s", why)
	}
}

// TestVersionRule: a breaking change is refused below a major bump (a minor
// before 1.0), and a potentially breaking one needs at least a minor.
func TestVersionRule(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	publishAt := func(version, workload string) (Plan, error) {
		res, err := Apply(ctx, reg, newTools(), Input{Bundle: bundleOf(host, version, workload), Revision: "r" + version})
		return res.Plan, err
	}
	if _, err := publishAt("1.0.0", workloadService); err != nil {
		t.Fatal(err)
	}
	p, err := publishAt("1.1.0", "job")
	if !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "needs a major bump over 1.0.0: publish 2.0.0 or higher") ||
		p.Baseline != "1.0.0" || p.RequiredBump != "major" {
		t.Fatalf("breaking as a minor: %v %+v", err, p)
	}
	if _, err := publishAt("2.0.0", "job"); err != nil {
		t.Fatalf("breaking as a major: %v", err)
	}
	// 1.5.0 compares against 1.0.0, the highest version below it.
	if p, _ := publishAt("1.5.0", workloadService); p.Baseline != "1.0.0" {
		t.Fatalf("baseline of 1.5.0: %s", p.Baseline)
	}
	host0, reg0 := newRegistry(t)
	pre := func(version, workload string) error {
		_, err := Apply(ctx, reg0, newTools(), Input{Bundle: bundleOf(host0, version, workload), Revision: "r" + version})
		return err
	}
	if err := pre("0.1.0", workloadService); err != nil {
		t.Fatal(err)
	}
	if err := pre("0.1.1", "job"); err == nil || !strings.Contains(err.Error(), "needs a minor bump") {
		t.Fatalf("pre-1.0 breaking as a patch: %v", err)
	}
	if err := pre("0.2.0", "job"); err != nil {
		t.Fatalf("pre-1.0 breaking as a minor: %v", err)
	}
	// A potentially breaking change says Pacto could not tell.
	b := bundleOf(host0, "0.3.0", "job")
	b.Contract.Capabilities = nil
	base := bundleOf(host0, "0.2.0", "job")
	base.Contract.Capabilities = []contract.Capability{{Type: "health"}}
	ch := versionCheck(ctx, stubBaseline{reg0, base}, b, &Plan{Version: "0.3.0"})
	if !ch.Passed || !strings.Contains(ch.Message, "could not tell") {
		t.Fatalf("potentially breaking: %+v", ch)
	}
}

// TestVersionRefusal: the version rule's refusal is what publish keeps; a
// version it accepts is none.
func TestVersionRefusal(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	publishAt := func(version, workload string) Plan {
		res, _ := Apply(ctx, reg, newTools(), Input{Bundle: bundleOf(host, version, workload), Revision: "r" + version})
		return res.Plan
	}
	publishAt("1.0.0", workloadService)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.FixedZone("CEST", 7200))
	want := delivery.PublishRefusal{Service: "payments", Version: "1.1.0", Baseline: "1.0.0", Classification: "BREAKING", RequiredBump: "major",
		At: "2026-10-22T10:00:00Z"}
	r, ok := VersionRefusal(publishAt("1.1.0", "job"), at)
	msg := r.Message
	r.Message = ""
	expect(t, ok && r == want && strings.Contains(msg, "needs a major bump"), "refusal: %+v %v", r, ok)
	_, ok = VersionRefusal(publishAt("2.0.0", "job"), at)
	expect(t, !ok, "a published version reads as refused")
}

// TestVersionRuleCandidate: a release candidate is exempt only from the bump
// its line already carries. A breaking change since 1.0.0 cannot ship as 1.1.0
// after a non-breaking 1.1.0-rc.1.
func TestVersionRuleCandidate(t *testing.T) {
	ctx := context.Background()
	hostRC, regRC := newRegistry(t)
	rc := func(version, workload string) (Plan, error) {
		res, err := Apply(ctx, regRC, newTools(), Input{Bundle: bundleOf(hostRC, version, workload), Revision: "r" + version})
		return res.Plan, err
	}
	for _, v := range []string{"1.0.0", "1.1.0-rc.1"} {
		if _, err := rc(v, workloadService); err != nil {
			t.Fatalf("%s: %v", v, err)
		}
	}
	for _, v := range []string{"1.1.0-rc.2", "1.1.0"} {
		p, err := rc(v, "job")
		if err == nil || !strings.Contains(err.Error(), "needs a major bump over 1.0.0: publish 2.0.0 or higher") ||
			p.Baseline != "1.0.0" || p.RequiredBump != "major" {
			t.Fatalf("breaking as %s after a minor candidate: %v %+v", v, err, p)
		}
	}
	for _, v := range []string{"2.0.0-rc.1", "2.0.0-rc.2", "2.0.0"} {
		if p, err := rc(v, "job"); err != nil || v != "2.0.0-rc.1" && p.Baseline == "1.0.0" {
			t.Fatalf("breaking as %s: %v %+v", v, err, p)
		}
	}
}

// stubBaseline answers every pull with base.
type stubBaseline struct {
	*Client
	base *contract.Bundle
}

func (s stubBaseline) Pull(context.Context, string) (*contract.Bundle, error) { return s.base, nil }

// brokenRegistry fails the call named in fail.
type brokenRegistry struct {
	*Client
	fail string
}

func (b brokenRegistry) Head(ctx context.Context, ref string) (*Remote, error) {
	if b.fail == "head" || b.fail == "head-sbom" && strings.Contains(ref, "@") {
		return nil, errTest
	}
	return b.Client.Head(ctx, ref)
}
func (b brokenRegistry) Tags(ctx context.Context, repo string) ([]string, error) {
	if b.fail == "tags" {
		return nil, errTest
	}
	return b.Client.Tags(ctx, repo)
}
func (b brokenRegistry) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if b.fail == "pull" {
		return nil, errTest
	}
	return b.Client.Pull(ctx, ref)
}
func (b brokenRegistry) Write(ctx context.Context, repo string, a Artifact) (string, error) {
	if b.fail == "write" || b.fail == "write-bundle" && strings.Contains(repo, "pacto") ||
		b.fail == "write-sbom" && isSBOM(a) {
		return "", errTest
	}
	return b.Client.Write(ctx, repo, a)
}
func (b brokenRegistry) Referrers(ctx context.Context, repo, digest, at string) ([]string, error) {
	if b.fail == "referrers" {
		return nil, errTest
	}
	return b.Client.Referrers(ctx, repo, digest, at)
}

func (b brokenRegistry) Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error) {
	if b.fail == "layer" {
		return nil, nil, errTest
	}
	return b.Client.Layer(ctx, repo, digest)
}

func isSBOM(a Artifact) bool {
	img, ok := a.(v1.Image)
	if !ok {
		return false
	}
	m, err := img.Manifest()
	return err == nil && m.ArtifactType == SPDXType
}

func failedCheck(p Plan) string {
	for _, c := range p.Checks {
		if !c.Passed {
			return c.Name
		}
	}
	return ""
}

func TestPlanChecks(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	b := bundleOf(host, "1.0.0", workloadService)
	nopub := bundleOf(host, "1.0.0", workloadService)
	nopub.Contract.Publish = nil
	p := MakePlan(ctx, reg, Input{Bundle: nopub})
	expect(t, !p.Ready && failedCheck(p) == "publish", "no publish section: %+v", p)
	nosign := bundleOf(host, "1.0.0", workloadService)
	nosign.Contract.Publish.Sign = nil
	p = MakePlan(ctx, reg, Input{Bundle: nosign, Revision: "r"})
	expect(t, failedCheck(p) == "sign", "no sign: %+v", p.Checks)
	p = MakePlan(ctx, reg, Input{Bundle: b})
	expect(t, failedCheck(p) == "revision", "no revision: %+v", p.Checks)
	clash := bundleOf(host, "1.0.0", workloadService)
	clash.Contract.Publish.Artifacts[0].Ref = clash.Contract.Publish.Bundle
	p = MakePlan(ctx, reg, Input{Bundle: clash, Revision: "r"})
	expect(t, failedCheck(p) == "refs" && strings.Contains(p.Checks[1].Message, "bundle and api are both published to"), "bundle on the image: %+v", p.Checks)
	clash.Contract.Publish.Artifacts[0].Ref = clash.Contract.Publish.Artifacts[1].Ref
	p = MakePlan(ctx, reg, Input{Bundle: clash, Revision: "r"})
	expect(t, failedCheck(p) == "refs", "two artifacts in one repository: %+v", p.Checks)
	docker := bundleOf(host, "1.0.0", workloadService)
	docker.Contract.Publish.Artifacts[0].Build = contract.PublishBuild{Dockerfile: &contract.DockerfileBuild{Context: "."}}
	p = MakePlan(ctx, reg, Input{Bundle: docker, Revision: "r"})
	expect(t, failedCheck(p) == "sbom" && strings.Contains(p.Checks[2].Message, "writes no SBOM file"), "an SBOM on a dockerfile build: %+v", p.Checks)
	for _, s := range []contract.PublishSign{{KMS: "awskms:///a"}, {Keyless: true, Identity: "ci@acme", Issuer: "https://issuer"}} {
		c := signCheck(&contract.Publish{Sign: &s})
		expect(t, c.Passed, "sign %+v: %+v", s, c)
	}
	off := false
	c := signCheck(&contract.Publish{Sign: &contract.PublishSign{Keyless: true, Identity: "ci@acme", Issuer: "https://issuer", TransparencyLog: &off}})
	expect(t, !c.Passed && strings.Contains(c.Message, "transparency log"), "keyless without the log: %+v", c)
	expect(t, refusal(Plan{}) == "not ready", "refusal of an empty plan")
}

func TestPlanUnreadable(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	b := bundleOf(host, "1.0.0", workloadService)
	p := MakePlan(ctx, brokenRegistry{reg, "tags"}, Input{Bundle: b, Revision: "r"})
	expect(t, failedCheck(p) == "version", "tags unreadable: %+v", p.Checks)
	p = MakePlan(ctx, brokenRegistry{reg, "head"}, Input{Bundle: b, Revision: "r"})
	expect(t, states(p) == "api=unreadable chart=unreadable manifests=unreadable bundle=unreadable", "heads unreadable: %s", states(p))
	_, err := Apply(ctx, reg, newTools(), Input{Bundle: b, Revision: "r"})
	expect(t, err == nil, "publish: %v", err)
	next := bundleOf(host, "1.1.0", workloadService)
	p = MakePlan(ctx, brokenRegistry{reg, "pull"}, Input{Bundle: next, Revision: "r"})
	expect(t, failedCheck(p) == "version", "baseline unreadable: %+v", p.Checks)
	_, refused := VersionRefusal(p, time.Now())
	expect(t, !refused && p.RequiredBump == "", "an unreadable baseline is no version refusal: %+v", p)
	unpackable := bundleOf(host, "1.1.0", workloadService)
	unpackable.FS = nil
	p = MakePlan(ctx, reg, Input{Bundle: unpackable, Revision: "r"})
	expect(t, p.Items[3].State == Unreadable, "unpackable bundle: %s", states(p))
}

func TestApplyFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		reg   func(*Client) Registry
		tools func(*fakeTools)
	}{
		{"build", nil, func(f *fakeTools) { f.failBuild = "api" }},
		{"sign", nil, func(f *fakeTools) { f.failSign = true }},
		{"sbom", nil, func(f *fakeTools) { f.failSBOM = true }},
		{"not an sbom", nil, func(f *fakeTools) { f.sbom = []byte(`{"bomFormat":"CycloneDX"}`) }},
		{"contract changed", nil, nil},
		{"wrote nothing", nil, func(f *fakeTools) { f.nothing = true }},
		{"write", func(c *Client) Registry { return brokenRegistry{c, "write"} }, nil},
		{"write bundle", func(c *Client) Registry { return brokenRegistry{c, "write-bundle"} }, nil},
		{"write sbom", func(c *Client) Registry { return brokenRegistry{c, "write-sbom"} }, nil},
		{"referrers", func(c *Client) Registry { return brokenRegistry{c, "referrers"} }, nil},
		{"read back", func(c *Client) Registry { return brokenRegistry{c, "head-sbom"} }, nil},
		{"tag", func(c *Client) Registry { return failingTags{c, ""} }, nil},
		{"no temp dir", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			host, client := newRegistry(t)
			var reg Registry = client
			if c.reg != nil {
				reg = c.reg(client)
			}
			if c.name == "tag" {
				reg = failingTags{client, host + "/payments"}
			}
			if c.name == "no temp dir" {
				t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
			}
			tools := newTools()
			if c.tools != nil {
				c.tools(tools)
			}
			b := bundleOf(host, "1.0.0", workloadService)
			if c.name == "contract changed" {
				tools.dirty = b.FS.(fstest.MapFS)
			}
			_, err := Apply(ctx, reg, tools, Input{Bundle: b, Revision: "r"})
			if err == nil {
				t.Fatal("the failure must surface")
			}
			if c.name == "contract changed" && !strings.Contains(err.Error(), "changed while publishing") {
				t.Fatalf("contract changed: %v", err)
			}
			if c.name == "not an sbom" && !strings.Contains(err.Error(), "is not an SBOM of type application/spdx+json") {
				t.Fatalf("not an sbom: %v", err)
			}
		})
	}
}

func TestLoadImage(t *testing.T) {
	dir := t.TempDir()
	_, err := load(contract.ArtifactImage, dir, nil)
	expect(t, err != nil, "no layout")
	p, _ := layout.Write(filepath.Join(dir, "two"), empty.Index)
	for range 2 {
		img, _ := random.Image(8, 1)
		_ = p.AppendImage(img)
	}
	_, err = load(contract.ArtifactImage, filepath.Join(dir, "two"), nil)
	expect(t, err != nil && strings.Contains(err.Error(), "2 manifests"), "two images: %v", err)
	tools := newTools()
	tools.index = true
	out := filepath.Join(dir, "index")
	_ = os.Mkdir(out, 0o755)
	_ = tools.Build(context.Background(), contract.PublishArtifact{Name: "api", Type: contract.ArtifactImage}, out, nil)
	a, err := load(contract.ArtifactImage, out, map[string]string{"k": "v"})
	idx, ok := a.(v1.ImageIndex)
	expect(t, err == nil && ok, "index: %v %T", err, a)
	m, _ := idx.IndexManifest()
	expect(t, m.Annotations["k"] == "v", "index annotations: %v", m.Annotations)
}

func TestLoadChart(t *testing.T) {
	chart := func(files map[string]string) error {
		d := t.TempDir()
		_ = os.WriteFile(filepath.Join(d, "c.tgz"), chartTGZ(files), 0o644)
		_, err := load(contract.ArtifactChart, d, nil)
		return err
	}
	err := chart(map[string]string{"c/values.yaml": "a: 1"})
	expect(t, err != nil && strings.Contains(err.Error(), "no Chart.yaml"), "no Chart.yaml: %v", err)
	expect(t, chart(map[string]string{"c/Chart.yaml": "[: :"}) != nil, "a broken Chart.yaml")
	_, err = load(contract.ArtifactChart, t.TempDir(), nil)
	expect(t, err != nil, "no chart")
	notgz := t.TempDir()
	_ = os.WriteFile(filepath.Join(notgz, "c.tgz"), []byte("not gzip"), 0o644)
	_, err = load(contract.ArtifactChart, notgz, nil)
	expect(t, err != nil, "not a gzip")
	truncated := t.TempDir()
	tgz := chartTGZ(map[string]string{"c/a": strings.Repeat("x", 4096)})
	_ = os.WriteFile(filepath.Join(truncated, "c.tgz"), tgz[:len(tgz)/2], 0o644)
	_, err = load(contract.ArtifactChart, truncated, nil)
	expect(t, err != nil, "a truncated chart")
	unreadable := t.TempDir()
	_ = os.Mkdir(filepath.Join(unreadable, "c.tgz"), 0o755)
	_, err = load(contract.ArtifactChart, unreadable, nil)
	expect(t, err != nil, "a chart that is a directory")
}

func TestLoadManifests(t *testing.T) {
	_, err := load(contract.ArtifactManifests, t.TempDir(), nil)
	expect(t, err != nil, "no manifests")
	_, err = load(contract.ArtifactManifests, filepath.Join(t.TempDir(), "missing"), nil)
	expect(t, err != nil, "a missing directory")
	broken := t.TempDir()
	_ = os.Symlink(filepath.Join(broken, "nowhere"), filepath.Join(broken, "link"))
	_, err = load(contract.ArtifactManifests, broken, nil)
	expect(t, err != nil, "a dangling symlink")
}

func TestClient(t *testing.T) {
	host, c := newRegistry(t)
	ctx := context.Background()
	_, err := c.Head(ctx, "BAD REF")
	expect(t, err != nil, "head of a bad ref")
	_, err = c.Tags(ctx, "BAD REF")
	expect(t, err != nil, "tags of a bad ref")
	tags, err := c.Tags(ctx, host+"/nothing")
	expect(t, err == nil && tags == nil, "an unknown repository: %v %v", tags, err)
	_, err = c.Write(ctx, "BAD REF", empty.Image)
	expect(t, err != nil, "write to a bad ref")
	_, err = c.Write(ctx, host+"/x", badArtifact{})
	expect(t, err != nil, "write of an artifact with no digest")
	_, err = c.Write(ctx, host+"/x", hashOnly{})
	expect(t, err != nil, "write of an unknown artifact kind")
	var scheme string
	err = NewClient(anonymous).oci.Do(ctx, "ghcr.io/acme/x:1", func(r name.Reference, _ []remote.Option) error {
		scheme = r.Context().Scheme()
		return nil
	})
	expect(t, err == nil && scheme == "https", "a secure host: %v", err)
}

// candidates offers a rejected credential before the accepted one, the way
// oci's keychain lists every source it knows for a registry.
type candidates struct{ authn.Keychain }

func (candidates) Candidates(authn.Resource) ([]oci.CredSource, error) {
	return []oci.CredSource{
		{Name: "stale", Authenticator: &authn.Basic{Username: "u", Password: "stale"}},
		{Name: "fresh", Authenticator: &authn.Basic{Username: "u", Password: "fresh"}},
	}, nil
}

// TestClientFallsThroughRejectedCredentials: a write tries the next credential
// when the registry rejects one, as a pull does, instead of failing on the
// first.
func TestClientFallsThroughRejectedCredentials(t *testing.T) {
	reg := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pw, ok := r.BasicAuth(); ok && pw != "fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	c := NewClient(candidates{anonymous}, host)
	d, err := c.Write(context.Background(), host+"/x", empty.Image)
	expect(t, err == nil, "write with a stale credential first: %v", err)
	r, err := c.Head(context.Background(), host+"/x@"+d)
	expect(t, err == nil && r != nil, "head: %v", err)
}

func TestClientTagsAndReferrers(t *testing.T) {
	host, c := newRegistry(t)
	ctx := context.Background()
	d, _ := c.Write(ctx, host+"/x", empty.Image)
	expect(t, c.Tag(ctx, "BAD REF", d, "1") != nil, "tag of a bad ref")
	expect(t, c.Tag(ctx, host+"/x", "sha256:"+strings.Repeat("0", 64), "1") != nil, "tag of a missing digest")
	expect(t, c.Tag(ctx, host+"/x", d, "1.0.0+build") != nil, "a tag Docker refuses")
	_, err := c.Referrers(ctx, "BAD REF", d, SPDXType)
	expect(t, err != nil, "referrers of a bad ref")
	gone := NewClient(anonymous, "127.0.0.1:1")
	_, err = gone.Head(ctx, "127.0.0.1:1/x:1")
	expect(t, err != nil, "an unreachable registry is never absent")
	_, err = gone.Referrers(ctx, "127.0.0.1:1/x", d, SPDXType)
	expect(t, err != nil, "referrers of an unreachable registry")
}

type badArtifact struct{}

func (badArtifact) Digest() (v1.Hash, error) { return v1.Hash{}, errTest }

type hashOnly struct{}

func (hashOnly) Digest() (v1.Hash, error) { return v1.NewHash("sha256:" + strings.Repeat("a", 64)) }

// TestApplyIndex: a multi-platform build publishes its image index.
func TestApplyIndex(t *testing.T) {
	host, reg := newRegistry(t)
	tools := newTools()
	tools.index = true
	res, err := Apply(context.Background(), reg, tools, Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := reg.Head(context.Background(), host+"/payments:1.0.0"); r == nil || r.Digest != res.Digests["api"] || !strings.Contains(r.MediaType, "index") {
		t.Fatalf("index: %+v", r)
	}
}

// TestLoadBrokenLayouts: a layout whose index or blobs are broken is refused.
func TestLoadBrokenLayouts(t *testing.T) {
	write := func(index string) string {
		d := t.TempDir()
		_ = os.WriteFile(filepath.Join(d, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644)
		_ = os.WriteFile(filepath.Join(d, "index.json"), []byte(index), 0o644)
		return d
	}
	missing := "sha256:" + strings.Repeat("b", 64)
	for name, index := range map[string]string{
		"bad json":      "{",
		"missing index": `{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.index.v1+json","digest":"` + missing + `","size":1}]}`,
		"unknown type":  `{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.acme.thing","digest":"` + missing + `","size":1}]}`,
	} {
		if _, err := load(contract.ArtifactImage, write(index), nil); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestDecide(t *testing.T) {
	if s, _ := Decide(&Remote{Digest: "d"}, nil, "", "1", ""); s != Conflict {
		t.Fatalf("no revision: %s", s)
	}
}

// TestApplyNeverSignsForeignContent: a tag whose unsigned provenance claims
// this version and revision is not adopted, and an SBOM someone else attached
// is never reused or signed.
func TestApplyNeverSignsForeignContent(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	img, _ := random.Image(64, 1)
	forged := mutate.Annotations(img, map[string]string{AnnotationVersion: "1.0.0", AnnotationRevision: "abc123"}).(v1.Image)
	d, _ := reg.Write(ctx, host+"/payments", forged)
	expect(t, reg.Tag(ctx, host+"/payments", d, "1.0.0") == nil, "tag")
	tools := newTools()
	res, err := Apply(ctx, reg, tools, Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123"})
	expect(t, errors.Is(err, ErrNotReady) && res.Plan.Items[0].State == Conflict && len(tools.signed) == 0,
		"a forged tag was adopted: %v %s signed %v", err, states(res.Plan), tools.signed)

	head, _ := reg.Head(ctx, host+"/payments@"+d)
	h, _ := v1.NewHash(d)
	subject := v1.Descriptor{MediaType: types.MediaType(head.MediaType), Size: head.Size, Digest: h}
	foreign, err := reg.Write(ctx, host+"/payments", sbomArtifact(SPDXType, []byte(`{"forged":true}`), subject, nil))
	expect(t, err == nil, "foreign SBOM: %v", err)
	ours, err := attachSBOM(ctx, reg, tools, contract.PublishSign{Key: "k"}, "sbom/api.spdx.json", host+"/payments", d, "1.0.0", "abc123", &Result{})
	expect(t, err == nil && ours != foreign, "the foreign SBOM was reused: %s %v", ours, err)
	tools.signed[host+"/payments@"+ours] = true
	again, err := attachSBOM(ctx, reg, tools, contract.PublishSign{Key: "k"}, "sbom/api.spdx.json", host+"/payments", d, "1.0.0", "abc123", &Result{})
	expect(t, err == nil && again == ours, "our signed SBOM is reused: %s %v", again, err)
}

// TestTagReadsAgain: a tag is read just before it is written, so a publish
// racing this one is refused instead of overwritten.
func TestTagReadsAgain(t *testing.T) {
	host, client := newRegistry(t)
	ctx := context.Background()
	a, _ := random.Image(64, 1)
	b, _ := random.Image(64, 1)
	da, _ := client.Write(ctx, host+"/x", a)
	db, _ := client.Write(ctx, host+"/x", b)
	tag := func(reg Registry) func() error {
		r := &applier{ctx: ctx, reg: reg, res: &Result{Plan: Plan{Version: "1.0.0"}}}
		r.owe(false, host+"/x", da)
		return r.tags[0]
	}
	expect(t, tag(brokenRegistry{client, "head"})() != nil, "an unreadable tag is not written")
	expect(t, tag(client)() == nil, "an absent tag is written")
	expect(t, tag(client)() == nil, "a tag already holding the digest is left alone")
	expect(t, client.Tag(ctx, host+"/x", db, "1.0.0") == nil, "race")
	err := tag(client)()
	expect(t, err != nil && strings.Contains(err.Error(), "never overwritten"), "a racing tag was overwritten: %v", err)
}

// TestSBOMFormat: the file's name picks the referrer type and its content
// must be that format.
func TestSBOMFormat(t *testing.T) {
	expect(t, sbomType("a.cdx.json") == CycloneDXType && sbomType("a.spdx.json") == SPDXType, "types")
	expect(t, checkSBOM("a.cdx.json", CycloneDXType, []byte(`{"bomFormat":"CycloneDX"}`)) == nil, "cyclonedx")
	expect(t, checkSBOM("a.spdx.json", SPDXType, []byte(`{"spdxVersion":"SPDX-2.3"}`)) == nil, "spdx")
	for _, b := range []string{`{"spdxVersion":"SPDX-2.3"}`, `nope`, `{}`} {
		expect(t, checkSBOM("a.cdx.json", CycloneDXType, []byte(b)) != nil, "accepted %s as CycloneDX", b)
	}
}

// TestMakePlanPreflight: a failed preflight check makes the plan not ready.
func TestMakePlanPreflight(t *testing.T) {
	host, reg := newRegistry(t)
	in := Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123",
		Preflight: func(Plan) []Check { return []Check{{Name: "docker", Message: "docker is not installed"}} }}
	p := MakePlan(context.Background(), reg, in)
	expect(t, !p.Ready && p.Checks[len(p.Checks)-1].Name == "docker", "plan: %+v", p)
}
