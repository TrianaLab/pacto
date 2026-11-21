package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// buildRun stands in for the build tools: it records every call and writes
// what each tool would. fail names a program that fails.
type buildRun struct {
	calls []string
	envs  [][]string
	fail  string
	// skaffold is what skaffold writes to its build file.
	skaffold string
}

func argOf(args []string, prefix string) string {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, prefix); ok {
			return v
		}
	}
	return ""
}

func writeLayout(dir string) error {
	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		return err
	}
	img, _ := random.Image(32, 1)
	return p.AppendImage(img)
}

func writeTarball(path string) error {
	img, _ := random.Image(32, 1)
	tag, _ := name.NewTag("example.com/built:1")
	return tarball.WriteToFile(path, tag, img)
}

func (b *buildRun) run(_ context.Context, _ string, env []string, prog string, args ...string) ([]byte, error) {
	b.calls = append(b.calls, strings.Join(append([]string{prog}, args...), " "))
	b.envs = append(b.envs, env)
	switch {
	case prog == b.fail || prog == "git":
		return nil, errors.New("injected: " + prog)
	case prog == "skaffold":
		return nil, os.WriteFile(argOf(args, "--file-output="), []byte(b.skaffold), 0o644)
	case prog == "ko":
		return nil, writeLayout(argOf(args, "--oci-layout-path="))
	case prog == "docker" && args[0] == "buildx":
		dest, _, _ := strings.Cut(argOf(args, "type=oci,dest="), ",")
		return nil, writeLayout(dest)
	case prog == "docker":
		return nil, writeTarball(args[3]) // image save -o <path> <tag>
	case prog == "pack":
		return nil, nil
	}
	return nil, writeTarball(argOf(args, "-Djib.outputPaths.tar="))
}

const skaffoldBuilt = `{"builds":[{"imageName":"pay","tag":"r.io/acme/pay:pacto-0123456789ab"}]}`

// buildWith runs bp's builder into a fresh directory and returns it, with
// the programs the builder names.
func buildWith(t *testing.T, r *buildRun, bp discover.BuildPlan, epoch string) (string, []string, error) {
	t.Helper()
	bi, programs := imageBuilder(t.TempDir(), bp, "r.io/acme/pay", r.run, epoch)
	out := t.TempDir()
	return out, programs, bi(t.Context(), out, []string{"PACTO_VERSION=1.2.3", "PACTO_REVISION=0123456789abcdef"})
}

func wantCalls(t *testing.T, r *buildRun, err error, want ...string) {
	t.Helper()
	if err != nil || !slices.Equal(r.calls, want) {
		t.Fatalf("%v\ncalls %q\nwant  %q", err, r.calls, want)
	}
}

func TestDeriveKoArgs(t *testing.T) {
	r := &buildRun{}
	out, programs, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolKo, Main: "./cmd/api"}, "1700000000")
	wantCalls(t, r, err, "ko build --push=false --bare --sbom=none --oci-layout-path="+out+" ./cmd/api")
	env := strings.Join(r.envs[0], " ")
	if !strings.Contains(env, "KO_DOCKER_REPO=r.io/acme/pay") || !strings.Contains(env, "SOURCE_DATE_EPOCH=1700000000") || !slices.Equal(programs, []string{"ko"}) {
		t.Fatalf("env %s programs %v", env, programs)
	}
}

func TestDeriveJibMavenArgs(t *testing.T) {
	r := &buildRun{}
	out, programs, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolJib, Wrapper: "./mvnw"}, "")
	wantCalls(t, r, err, "./mvnw -q compile jib:buildTar -Dimage=r.io/acme/pay:1.2.3 -Djib.outputPaths.tar="+filepath.Join(out, "image.tar"))
	if !slices.Equal(programs, []string{"./mvnw"}) || slices.ContainsFunc(r.envs[0], func(e string) bool { return strings.HasPrefix(e, "SOURCE_DATE_EPOCH=") }) {
		t.Fatalf("programs %v env %v", programs, r.envs[0])
	}
}

func TestDeriveJibGradleArgs(t *testing.T) {
	r := &buildRun{}
	out, _, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolJib, Wrapper: "gradle"}, "")
	wantCalls(t, r, err, "gradle jibBuildTar -Dimage=r.io/acme/pay:1.2.3 -Djib.outputPaths.tar="+filepath.Join(out, "image.tar"))
}

func TestDeriveBuildpacksArgs(t *testing.T) {
	r := &buildRun{}
	out, programs, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolBuildpacks}, "")
	wantCalls(t, r, err, "pack build r.io/acme/pay:pacto-0123456789ab --path .",
		"docker image save -o "+filepath.Join(out, "image.tar")+" r.io/acme/pay:pacto-0123456789ab")
	if !slices.Equal(programs, []string{"pack", "docker"}) {
		t.Fatalf("programs %v", programs)
	}
	r = &buildRun{fail: "pack"}
	if _, _, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolBuildpacks}, ""); err == nil || len(r.calls) != 1 {
		t.Fatalf("a failed pack build saves nothing: %v %v", err, r.calls)
	}
}

func TestDeriveSkaffoldArgs(t *testing.T) {
	r := &buildRun{skaffold: skaffoldBuilt}
	out, programs, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolSkaffold}, "")
	wantCalls(t, r, err, "skaffold build --push=false --file-output="+filepath.Join(out, "build.json")+" --tag=pacto-0123456789ab",
		"docker image save -o "+filepath.Join(out, "image.tar")+" r.io/acme/pay:pacto-0123456789ab")
	if !slices.Equal(programs, []string{"skaffold", "docker"}) {
		t.Fatalf("programs %v", programs)
	}
	for _, body := range []string{`{"builds":[]}`, `nope`} {
		r = &buildRun{skaffold: body}
		if _, _, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolSkaffold}, ""); err == nil || !strings.Contains(err.Error(), "exactly one image") {
			t.Fatalf("%s: %v", body, err)
		}
	}
	r = &buildRun{fail: "skaffold"}
	if _, _, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolSkaffold}, ""); err == nil || len(r.calls) != 1 {
		t.Fatalf("a failed skaffold build saves nothing: %v %v", err, r.calls)
	}
}

func TestDeriveDockerfileArgsRewriteTimestamp(t *testing.T) {
	r := &buildRun{}
	out, programs, err := buildWith(t, r, discover.BuildPlan{Tool: discover.ToolDockerfile, ToolSource: "build/api.Dockerfile:1"}, "1700000000")
	wantCalls(t, r, err, "docker buildx build --output type=oci,dest="+out+",tar=false,rewrite-timestamp=true --label "+publish.AnnotationVersion+"=1.2.3 --label "+
		publish.AnnotationRevision+"=0123456789abcdef --file build/api.Dockerfile build")
	if !slices.Equal(programs, []string{"docker"}) {
		t.Fatalf("programs %v", programs)
	}
	r = &buildRun{}
	out, _, err = buildWith(t, r, discover.BuildPlan{Tool: discover.ToolDockerfile, ToolSource: "Dockerfile:1"}, "")
	wantCalls(t, r, err, "docker buildx build --output type=oci,dest="+out+",tar=false --label "+publish.AnnotationVersion+"=1.2.3 --label "+
		publish.AnnotationRevision+"=0123456789abcdef .")
}

// TestBuildersRunNoShell: no derived builder runs a shell.
func TestBuildersRunNoShell(t *testing.T) {
	r := &buildRun{skaffold: skaffoldBuilt}
	for _, bp := range []discover.BuildPlan{{Tool: discover.ToolSkaffold}, {Tool: discover.ToolKo, Main: "."}, {Tool: discover.ToolJib, Wrapper: "mvn"},
		{Tool: discover.ToolJib, Wrapper: "./gradlew"}, {Tool: discover.ToolBuildpacks}, {Tool: discover.ToolDockerfile, ToolSource: "Dockerfile:1"}} {
		if _, _, err := buildWith(t, r, bp, "1"); err != nil {
			t.Fatalf("%s: %v", bp.Tool, err)
		}
	}
	for _, c := range r.calls {
		if prog, _, _ := strings.Cut(c, " "); prog == "sh" || prog == "bash" || prog == "/bin/sh" || strings.Contains(c, " -c ") {
			t.Fatalf("a shell ran: %s", c)
		}
	}
}

func TestCommitEpoch(t *testing.T) {
	env := func(k string) string { return map[string]string{"SOURCE_DATE_EPOCH": "42"}[k] }
	gitSays := func(out string, err error) runner {
		return func(context.Context, string, []string, string, ...string) ([]byte, error) { return []byte(out), err }
	}
	if got := commitEpoch(t.Context(), env, gitSays("7\n", nil), "."); got != "42" {
		t.Fatalf("SOURCE_DATE_EPOCH wins: %q", got)
	}
	if got := commitEpoch(t.Context(), nil, gitSays("1700000000\n", nil), "."); got != "1700000000" || createdAt(got) != "2023-11-14T22:13:20Z" {
		t.Fatalf("git: %q %q", got, createdAt(got))
	}
	if got := commitEpoch(t.Context(), nil, gitSays("", errors.New("no git")), "."); got != "" || createdAt(got) != "" {
		t.Fatalf("unknown: %q", got)
	}
	if rev12([]string{"PACTO_REVISION=abc"}) != "abc" || rev12(nil) != "" {
		t.Fatal("rev12")
	}
}

// sectionless writes a 2.5 payments contract with no publish section at
// version ("" leaves it undeclared), extra appended, and files.
func sectionless(t *testing.T, version, extra string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	y := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n"
	if version != "" {
		y += "  version: " + version + "\n"
	}
	writeFiles(t, dir, map[string]string{"pacto.yaml": y + extra})
	writeFiles(t, dir, files)
	return dir
}

// deployment is a Deployment running image.
func deployment(image string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: payments}\nspec:\n  template:\n    spec:\n      containers:\n        - name: api\n          image: " + image + "\n"
}

// signedOpts signs with a key outside the folder, logged nowhere, and
// publishes under host/acme.
func signedOpts(t *testing.T, dir, host string) PublishOptions {
	t.Helper()
	keys := t.TempDir()
	keyFiles(t, keys)
	return PublishOptions{Path: dir, Revision: "r1", Registry: host + "/acme", SignKey: filepath.Join(keys, "cosign.key"), NoTransparencyLog: true}
}

func check(p publish.Plan, name string) publish.Check {
	for _, c := range p.Checks {
		if c.Name == name {
			return c
		}
	}
	return publish.Check{}
}

const digest64 = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestPublishDerivedBuildsWithTheTool: a section-less folder with a ko
// config publishes its image built by ko, the commit time unknown named.
func TestPublishDerivedBuildsWithTheTool(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := sectionless(t, "1.0.0", "", map[string]string{".ko.yaml": "builds:\n  - main: ./cmd/api\n", "k8s/deploy.yaml": deployment(host + "/acme/payments:1.0")})
	r := &buildRun{}
	res, err := publishService(&fakeRun{}, reg).withRun(r).Publish(t.Context(), signedOpts(t, dir, host))
	if err != nil || res.Digests[derivedImage] == "" || !slices.Contains(res.Notes, "not reproducible: no commit time (set SOURCE_DATE_EPOCH)") {
		t.Fatalf("%v %+v", err, res)
	}
	if c := check(res.Plan, "build"); c.Message != "ko (.ko.yaml), image "+host+"/acme/payments" {
		t.Fatalf("build check %+v", c)
	}
	if res.Plan.Items[2].Ref != host+"/acme/contracts/payments:1.0.0" {
		t.Fatalf("the bundle moves to contracts/ when the image holds the name: %+v", res.Plan.Items)
	}
}

func (s *Service) withRun(r *buildRun) *Service {
	s.sys.run = r.run
	return s
}

func TestPublishImageFlagNeedsDigest(t *testing.T) {
	_, err := (&Service{}).Publish(t.Context(), PublishOptions{Image: "r.io/x:1"})
	if err == nil || err.Error() != "--image needs a digest: a tag can move" {
		t.Fatal(err)
	}
}

func TestPublishImageFlagSkipsBuild(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := sectionless(t, "1.0.0", "", map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment("r.io/x:1")})
	r := &buildRun{}
	opts := signedOpts(t, dir, host)
	opts.Image = host + "/other@" + digest64
	res, err := publishService(&fakeRun{}, reg).withRun(r).Publish(t.Context(), opts)
	if err != nil || len(res.Plan.Items) != 2 || res.Plan.Items[0].Name != contract.ArtifactManifests || slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "docker") }) {
		t.Fatalf("%v %+v calls %v", err, res, r.calls)
	}
}

func TestPublishPinnedImageSkipsBuild(t *testing.T) {
	host, reg := publishRegistry(t)
	pinned := host + "/acme/payments@" + digest64
	dir := sectionless(t, "1.0.0", "", map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment(pinned)})
	r := &buildRun{}
	res, err := publishService(&fakeRun{}, reg).withRun(r).Publish(t.Context(), signedOpts(t, dir, host))
	if err != nil || len(res.Plan.Items) != 2 || res.Plan.Items[0].Name != contract.ArtifactManifests || check(res.Plan, "build").Message != "image supplied, not built by Pacto: "+pinned || len(r.calls) != 1 {
		t.Fatalf("%v %+v calls %v", err, res, r.calls)
	}
	bad := sectionless(t, "1.0.0", "", map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment(host + "/acme/payments@sha256:abc")})
	if _, err := publishService(&fakeRun{}, reg).Publish(t.Context(), signedOpts(t, bad, host)); err == nil || !strings.Contains(err.Error(), "workload image "+host+"/acme/payments@sha256:abc: the digest is not a sha256 digest") {
		t.Fatal(err)
	}
}

func TestPublishNoToolNoImageRefuses(t *testing.T) {
	host, reg := publishRegistry(t)
	for _, version := range []string{"1.0.0", ""} { // declared, derived
		dir := sectionless(t, version, "", map[string]string{"k8s/deploy.yaml": deployment("r.io/x:1")})
		_, err := publishService(&fakeRun{}, reg).Publish(t.Context(), signedOpts(t, dir, host))
		if err == nil || !strings.Contains(err.Error(), "payments declares no publish section and Pacto cannot derive one: finding: k8s/deploy.yaml") ||
			!strings.Contains(err.Error(), "names an image no Skaffold, ko, Jib, buildpacks or Dockerfile builds; pass --image or declare publish") {
			t.Fatalf("version %q: %v", version, err)
		}
	}
}

// refRegistry records every reference it is asked about.
type refRegistry struct {
	publish.Registry
	refs []string
}

func (r *refRegistry) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	r.refs = append(r.refs, ref)
	return r.Registry.Head(ctx, ref)
}

func (r *refRegistry) Write(ctx context.Context, repo string, a publish.Artifact) (string, error) {
	r.refs = append(r.refs, repo)
	return r.Registry.Write(ctx, repo, a)
}

func (r *refRegistry) Tag(ctx context.Context, repo, digest, tag string) error {
	r.refs = append(r.refs, repo)
	return r.Registry.Tag(ctx, repo, digest, tag)
}

func (r *refRegistry) Referrers(ctx context.Context, repo, digest, at string) ([]string, error) {
	r.refs = append(r.refs, repo)
	return r.Registry.Referrers(ctx, repo, digest, at)
}

// TestPublishImageFlagSignsNoExternalImage: a supplied image is never read,
// signed or copied, and the record says it was supplied; the version is
// derived for the derived bundle repository.
func TestPublishImageFlagSignsNoExternalImage(t *testing.T) {
	host, inner := publishRegistry(t)
	reg := &refRegistry{Registry: inner}
	dir := sectionless(t, "", "", map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment("r.io/x:1")})
	opts := signedOpts(t, dir, host)
	opts.Image = "other.example/img@" + digest64
	res, err := publishService(&fakeRun{}, reg).withRun(&buildRun{}).Publish(t.Context(), opts)
	if err != nil || res.Plan.Version != publish.FirstVersion {
		t.Fatalf("%v %+v", err, res)
	}
	if slices.ContainsFunc(reg.refs, func(r string) bool { return strings.Contains(r, "other.example") }) {
		t.Fatalf("the supplied image was touched: %v", reg.refs)
	}
	b, err := inner.Pull(t.Context(), host+"/acme/payments:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := contract.ReadInferredRecord(b.FS)
	i := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "publish.build" })
	if i < 0 || rec.Fields[i].Value != "image supplied, not built by Pacto: "+opts.Image || rec.Fields[i].Source != "flag:--image" {
		t.Fatalf("record %+v", rec.Fields)
	}
	if err := contract.VerifyInferredRecord(b.FS, rec); err != nil {
		t.Fatal(err)
	}
}

// TestPublishPlanChecksDerivedBuilders: a missing build program and a
// missing OIDC token fail the plan before anything is built.
func TestPublishPlanChecksDerivedBuilders(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := sectionless(t, "1.0.0", "", map[string]string{".ko.yaml": "builds: []\n"})
	r := &buildRun{}
	svc := publishService(&fakeRun{}, reg).withRun(r)
	svc.sys.lookPath = func(p string) (string, error) {
		if p == "ko" {
			return "", errors.New("not found")
		}
		return p, nil
	}
	opts := signedOpts(t, dir, host)
	opts.Plan = true
	res, err := svc.Publish(t.Context(), opts)
	if c := check(res.Plan, "builders"); err != nil || c.Passed || !strings.Contains(c.Message, "the build of image runs ko, which is not an executable here") {
		t.Fatalf("%v %+v", err, c)
	}
	t.Setenv("SIGSTORE_ID_TOKEN", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	opts.SignKey, opts.NoTransparencyLog = "", false
	res, err = svc.Publish(t.Context(), opts)
	if c := check(res.Plan, "signing key"); err != nil || c.Passed || !strings.Contains(c.Message, "no OIDC token") {
		t.Fatalf("%v %+v", err, c)
	}
	if slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "ko") }) {
		t.Fatalf("the plan built: %v", r.calls)
	}
}

// A relative --sign-key is read from the working directory, never from the
// contract directory a derived section is resolved against.
func TestPublishDerivedRelativeSignKey(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := sectionless(t, "1.0.0", "", nil)
	keys := t.TempDir()
	pub := keyFiles(t, keys)
	t.Chdir(keys)
	opts := PublishOptions{Path: dir, Revision: "r1", Registry: host + "/acme", SignKey: "cosign.key", NoTransparencyLog: true}
	res, err := publishService(&fakeRun{}, reg).Publish(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := publish.VerifyDigest(t.Context(), reg, publish.PolicyVerifier(signing.Policy{Key: pub}), host+"/acme/payments", res.Digests[publish.BundleItem]); err != nil {
		t.Fatal(err)
	}
}

// A relative --sign-key with no working directory to resolve it against is
// an error naming the flag.
func TestPublishSignKeyWithoutWorkingDirectory(t *testing.T) {
	gone := t.TempDir()
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PWD", "")
	if _, err := os.Getwd(); err == nil {
		t.Skip("this platform still names a removed working directory")
	}
	_, err := (&Service{}).Publish(t.Context(), PublishOptions{Path: t.TempDir(), SignKey: "cosign.key"})
	if err == nil || !strings.HasPrefix(err.Error(), "--sign-key cosign.key: ") {
		t.Fatal(err)
	}
}

func TestPublishDerivedWithSignKey(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := sectionless(t, "1.0.0", "", nil)
	keys := t.TempDir()
	pub := keyFiles(t, keys)
	opts := PublishOptions{Path: dir, Revision: "r1", Registry: host + "/acme", SignKey: filepath.Join(keys, "cosign.key"), NoTransparencyLog: true}
	res, err := publishService(&fakeRun{}, reg).Publish(t.Context(), opts)
	if err != nil || check(res.Plan, "sign").Message != "signed with Sigstore (key "+opts.SignKey+")" || check(res.Plan, "build").Message != "no image to build" {
		t.Fatalf("%v %+v", err, res)
	}
	if err := publish.VerifyDigest(t.Context(), reg, publish.PolicyVerifier(signing.Policy{Key: pub}), host+"/acme/payments", res.Digests[publish.BundleItem]); err != nil {
		t.Fatal(err)
	}
	declared := writePublishBundle(t, host, "")
	for _, o := range []PublishOptions{{Path: declared, SignKey: "k"}, {Path: declared, Image: "r@" + digest64}, {Path: declared, Registry: "r.io"}} {
		if _, err := publishService(&fakeRun{}, reg).Publish(t.Context(), o); err == nil || !strings.Contains(err.Error(), "apply only when the contract declares no publish section") {
			t.Fatalf("%+v: %v", o, err)
		}
	}
	// A truncated digest is never signed into the images record as deployed.
	if _, err := publishService(&fakeRun{}, reg).Publish(t.Context(), PublishOptions{Path: dir, Image: "r.io/acme/payments@sha256:abc"}); err == nil ||
		!strings.Contains(err.Error(), "the digest is not a sha256 digest") {
		t.Fatal(err)
	}
	if _, err := publishService(&fakeRun{}, reg).Publish(t.Context(), PublishOptions{Path: dir, NoTransparencyLog: true}); err == nil ||
		!strings.Contains(err.Error(), "--no-transparency-log needs --sign-key") {
		t.Fatal(err)
	}
}

// TestKeepRefusalDerivedSection: a refusal of a section-less contract is
// kept, signed, in the derived bundle repository.
func TestKeepRefusalDerivedSection(t *testing.T) {
	freshProbes(t)
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := publish.NewClient(anonymous, host)
	dir := sectionless(t, "1.0.0", "workload: service\n", nil)
	opts := signedOpts(t, dir, host)
	svc := publishService(&fakeRun{}, reg)
	if _, err := svc.Publish(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	dir2 := sectionless(t, "1.0.1", "workload: job\n", nil)
	opts.Path = dir2
	res, err := svc.Publish(t.Context(), opts)
	if !errors.Is(err, publish.ErrNotReady) || res.Refusal != "the refusal is kept, signed, in "+host+"/acme/payments" {
		t.Fatalf("%v %q", err, res.Refusal)
	}
}

// TestDeriveSectionRefusals: what keeps Pacto from deriving a section.
func TestDeriveSectionRefusals(t *testing.T) {
	host, reg := publishRegistry(t)
	behind := sectionless(t, "1.0.0", "", nil)
	writeFiles(t, behind, map[string]string{"pacto.yaml": "pactoVersion: \"2.4\"\nservice:\n  name: payments\n  version: 1.0.0\n"})
	for dir, want := range map[string]string{
		behind:                           "payments declares no publish section; Pacto derives one for pactoVersion 2.5: set pactoVersion: \"2.5\"",
		sectionless(t, "1.0.0", "", nil): "no registry to publish to: pass --registry",
	} {
		_, err := publishService(&fakeRun{}, reg).Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"})
		if err == nil || err.Error() != want {
			t.Fatalf("%s: %v", want, err)
		}
	}
	_, err := publishService(&fakeRun{}, reg).Publish(t.Context(), PublishOptions{Path: sectionless(t, "1.0.0", "", nil), Registry: "oci://u:p@" + host})
	if err == nil || !strings.Contains(err.Error(), "--registry: want") {
		t.Fatal(err)
	}
	c, _ := contract.Parse(strings.NewReader("pactoVersion: \"2.5\"\nservice: {name: payments}\n"))
	unreadable := t.TempDir()
	_ = os.Mkdir(filepath.Join(unreadable, ".pactoignore"), 0o755)
	if _, _, err := deriveSection(t.Context(), unreadable, &contract.Bundle{Contract: c}, PublishOptions{}, nil, contract.InferredInputs{}); err == nil {
		t.Fatal("an unreadable folder derives nothing")
	}
	bad := &contract.Bundle{Contract: c, FS: contract.WithInferredRecord(os.DirFS(t.TempDir()), []byte("nope"))}
	if _, err := suppliedImage(bad, "r@"+digest64, contract.InferredInputs{}); err == nil {
		t.Fatal("a broken record is refused")
	}
}
