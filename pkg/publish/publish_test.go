package publish

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

var errTest = errors.New("injected")

// fakeTools builds, signs and makes SBOMs in memory, counting each.
type fakeTools struct {
	builds    map[string]int
	signed    map[string]bool
	failBuild string
	failSign  bool
	failSBOM  bool
	nothing   bool // write nothing
	index     bool // build images as multi-platform indexes
	envs      [][]string
}

func newTools() *fakeTools { return &fakeTools{builds: map[string]int{}, signed: map[string]bool{}} }

func (f *fakeTools) Build(_ context.Context, a contract.PublishArtifact, out string, env []string) error {
	f.builds[a.Name]++
	f.envs = append(f.envs, env)
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

func (f *fakeTools) Sign(_ context.Context, _ contract.PublishSign, ref string) (bool, error) {
	if f.failSign {
		return false, errTest
	}
	was := f.signed[ref]
	f.signed[ref] = true
	return !was, nil
}

func (f *fakeTools) SBOM(context.Context, string) ([]byte, error) {
	if f.failSBOM {
		return nil, errTest
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

func newRegistry(t *testing.T) (string, *Client) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return host, NewClient(authn.DefaultKeychain, host)
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
    - {name: api, type: image, ref: "oci://` + host + `/payments", sbom: true, build: {command: [x]}}
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
func TestApplyPublishesOnceAndResumes(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	in := Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123", Checks: []Check{{Name: "validation", Passed: true}}}
	p := MakePlan(ctx, reg, in)
	if !p.Ready || p.Missing != 4 || states(p) != "api=absent chart=absent manifests=absent bundle=absent" {
		t.Fatalf("plan: %+v", p)
	}
	if tags, _ := reg.Tags(ctx, host+"/payments"); len(tags) != 0 {
		t.Fatalf("the plan wrote: %v", tags)
	}
	tools := newTools()
	res, err := Apply(ctx, reg, tools, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deploy == "" || len(res.SBOMs) != 1 || len(tools.signed) != 5 {
		t.Fatalf("result: %+v signed %v", res, tools.signed)
	}
	deploy, _ := reg.Head(ctx, host+"/deploy/payments:1.0.0")
	if deploy.Annotations[delivery.ContractAnnotation] != "oci://"+host+"/pacto/payments@"+res.Digests[BundleItem] ||
		deploy.Annotations[AnnotationRevision] != "abc123" || deploy.Annotations[AnnotationVersion] != "1.0.0" {
		t.Fatalf("deploy annotations: %v", deploy.Annotations)
	}
	if env := strings.Join(tools.envs[2], " "); !strings.Contains(env, "PACTO_DIGEST_API="+res.Digests["api"]) ||
		!strings.Contains(env, "PACTO_REF_CHART="+host+"/charts/payments") {
		t.Fatalf("the manifests build saw %s", env)
	}
	again, err := Apply(ctx, reg, tools, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Actions) != 0 || states(again.Plan) != "api=adopt chart=adopt manifests=adopt bundle=identical" ||
		again.Deploy != res.Deploy || tools.builds["api"] != 1 {
		t.Fatalf("the second publish changed something: %v %s", again.Actions, states(again.Plan))
	}
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
	if p := MakePlan(ctx, reg, in2); p.Items[3].State != Conflict {
		t.Fatalf("bundle: %s", states(p))
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

func isSBOM(a Artifact) bool {
	img, ok := a.(v1.Image)
	if !ok {
		return false
	}
	m, err := img.Manifest()
	return err == nil && m.ArtifactType == SBOMType
}

func TestPlanChecks(t *testing.T) {
	host, reg := newRegistry(t)
	ctx := context.Background()
	b := bundleOf(host, "1.0.0", workloadService)
	failed := func(p Plan) string {
		for _, c := range p.Checks {
			if !c.Passed {
				return c.Name
			}
		}
		return ""
	}
	nopub := bundleOf(host, "1.0.0", workloadService)
	nopub.Contract.Publish = nil
	if p := MakePlan(ctx, reg, Input{Bundle: nopub}); p.Ready || failed(p) != "publish" {
		t.Fatalf("no publish section: %+v", p)
	}
	nosign := bundleOf(host, "1.0.0", workloadService)
	nosign.Contract.Publish.Sign = nil
	if p := MakePlan(ctx, reg, Input{Bundle: nosign, Revision: "r"}); p.Ready || failed(p) != "sign" {
		t.Fatalf("no sign: %+v", p.Checks)
	}
	if p := MakePlan(ctx, reg, Input{Bundle: b}); p.Ready || failed(p) != "revision" {
		t.Fatalf("no revision: %+v", p.Checks)
	}
	if p := MakePlan(ctx, brokenRegistry{reg, "tags"}, Input{Bundle: b, Revision: "r"}); failed(p) != "version" {
		t.Fatalf("tags unreadable: %+v", p.Checks)
	}
	if p := MakePlan(ctx, brokenRegistry{reg, "head"}, Input{Bundle: b, Revision: "r"}); p.Ready || states(p) != "api=unreadable chart=unreadable manifests=unreadable bundle=unreadable" {
		t.Fatalf("heads unreadable: %s", states(p))
	}
	if _, err := Apply(ctx, reg, newTools(), Input{Bundle: b, Revision: "r"}); err != nil {
		t.Fatal(err)
	}
	next := bundleOf(host, "1.1.0", workloadService)
	if p := MakePlan(ctx, brokenRegistry{reg, "pull"}, Input{Bundle: next, Revision: "r"}); failed(p) != "version" {
		t.Fatalf("baseline unreadable: %+v", p.Checks)
	}
	unpackable := bundleOf(host, "1.1.0", workloadService)
	unpackable.FS = nil
	if p := MakePlan(ctx, reg, Input{Bundle: unpackable, Revision: "r"}); p.Items[3].State != Unreadable {
		t.Fatalf("unpackable bundle: %s", states(p))
	}
	for _, s := range []contract.PublishSign{{KMS: "awskms:///a"}, {Keyless: true}} {
		if c := signCheck(&contract.Publish{Sign: &s}); !c.Passed || c.Message == "" {
			t.Fatalf("sign %+v: %+v", s, c)
		}
	}
	if refusal(Plan{}) != "not ready" {
		t.Fatal("refusal of an empty plan")
	}
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
			if _, err := Apply(ctx, reg, tools, Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "r"}); err == nil {
				t.Fatal("the failure must surface")
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := load(contract.ArtifactImage, dir, nil); err == nil {
		t.Fatal("no layout")
	}
	p, _ := layout.Write(filepath.Join(dir, "two"), empty.Index)
	for range 2 {
		img, _ := random.Image(8, 1)
		_ = p.AppendImage(img)
	}
	if _, err := load(contract.ArtifactImage, filepath.Join(dir, "two"), nil); err == nil || !strings.Contains(err.Error(), "2 manifests") {
		t.Fatalf("two images: %v", err)
	}
	tools := newTools()
	tools.index = true
	out := filepath.Join(dir, "index")
	_ = os.Mkdir(out, 0o755)
	_ = tools.Build(context.Background(), contract.PublishArtifact{Name: "api", Type: contract.ArtifactImage}, out, nil)
	a, err := load(contract.ArtifactImage, out, map[string]string{"k": "v"})
	if idx, ok := a.(v1.ImageIndex); err != nil || !ok {
		t.Fatalf("index: %v %T", err, a)
	} else if m, _ := idx.IndexManifest(); m.Annotations["k"] != "v" {
		t.Fatalf("index annotations: %v", m.Annotations)
	}
	chart := func(files map[string]string) error {
		d := t.TempDir()
		_ = os.WriteFile(filepath.Join(d, "c.tgz"), chartTGZ(files), 0o644)
		_, err := load(contract.ArtifactChart, d, nil)
		return err
	}
	if err := chart(map[string]string{"c/values.yaml": "a: 1"}); err == nil || !strings.Contains(err.Error(), "no Chart.yaml") {
		t.Fatalf("no Chart.yaml: %v", err)
	}
	if err := chart(map[string]string{"c/Chart.yaml": "[: :"}); err == nil {
		t.Fatal("a broken Chart.yaml")
	}
	if _, err := load(contract.ArtifactChart, t.TempDir(), nil); err == nil {
		t.Fatal("no chart")
	}
	notgz := t.TempDir()
	_ = os.WriteFile(filepath.Join(notgz, "c.tgz"), []byte("not gzip"), 0o644)
	if _, err := load(contract.ArtifactChart, notgz, nil); err == nil {
		t.Fatal("not a gzip")
	}
	truncated := t.TempDir()
	tgz := chartTGZ(map[string]string{"c/a": strings.Repeat("x", 4096)})
	_ = os.WriteFile(filepath.Join(truncated, "c.tgz"), tgz[:len(tgz)/2], 0o644)
	if _, err := load(contract.ArtifactChart, truncated, nil); err == nil {
		t.Fatal("a truncated chart")
	}
	unreadable := t.TempDir()
	_ = os.Mkdir(filepath.Join(unreadable, "c.tgz"), 0o755)
	if _, err := load(contract.ArtifactChart, unreadable, nil); err == nil {
		t.Fatal("a chart that is a directory")
	}
	if _, err := load(contract.ArtifactManifests, t.TempDir(), nil); err == nil {
		t.Fatal("no manifests")
	}
	if _, err := load(contract.ArtifactManifests, filepath.Join(dir, "missing"), nil); err == nil {
		t.Fatal("a missing directory")
	}
	broken := t.TempDir()
	_ = os.Symlink(filepath.Join(broken, "nowhere"), filepath.Join(broken, "link"))
	if _, err := load(contract.ArtifactManifests, broken, nil); err == nil {
		t.Fatal("a dangling symlink")
	}
}

func TestClient(t *testing.T) {
	host, c := newRegistry(t)
	ctx := context.Background()
	if _, err := c.Head(ctx, "BAD REF"); err == nil {
		t.Fatal("head of a bad ref")
	}
	if _, err := c.Tags(ctx, "BAD REF"); err == nil {
		t.Fatal("tags of a bad ref")
	}
	if tags, err := c.Tags(ctx, host+"/nothing"); err != nil || tags != nil {
		t.Fatalf("an unknown repository: %v %v", tags, err)
	}
	if _, err := c.Write(ctx, "BAD REF", empty.Image); err == nil {
		t.Fatal("write to a bad ref")
	}
	if _, err := c.Write(ctx, host+"/x", badArtifact{}); err == nil {
		t.Fatal("write of an artifact with no digest")
	}
	if _, err := c.Write(ctx, host+"/x", hashOnly{}); err == nil {
		t.Fatal("write of an unknown artifact kind")
	}
	d, _ := c.Write(ctx, host+"/x", empty.Image)
	if err := c.Tag(ctx, "BAD REF", d, "1"); err == nil {
		t.Fatal("tag of a bad ref")
	}
	if err := c.Tag(ctx, host+"/x", "sha256:"+strings.Repeat("0", 64), "1"); err == nil {
		t.Fatal("tag of a missing digest")
	}
	if err := c.Tag(ctx, host+"/x", d, "1.0.0+build"); err == nil {
		t.Fatal("a tag Docker refuses")
	}
	if _, err := c.Referrers(ctx, "BAD REF", d, SBOMType); err == nil {
		t.Fatal("referrers of a bad ref")
	}
	dead, deadClient := newRegistry(t)
	_ = dead
	srvGone := NewClient(authn.DefaultKeychain, "127.0.0.1:1")
	if _, err := srvGone.Head(ctx, "127.0.0.1:1/x:1"); err == nil {
		t.Fatal("an unreachable registry is never absent")
	}
	if _, err := srvGone.Referrers(ctx, "127.0.0.1:1/x", d, SBOMType); err == nil {
		t.Fatal("referrers of an unreachable registry")
	}
	_ = deadClient
	secure := NewClient(authn.DefaultKeychain)
	if r, err := secure.parse("ghcr.io/acme/x:1"); err != nil || r.Context().Scheme() != "https" {
		t.Fatalf("a secure host: %v", err)
	}
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
