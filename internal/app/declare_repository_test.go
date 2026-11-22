package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	invoicesRepo = "https://github.com/acme/invoices"
	invoicesWF   = invoicesRepo + "/.github/workflows/release.yml@refs/heads/main"
	ghActions    = "https://token.actions.githubusercontent.com"
)

func ciID(repo string) delivery.PublisherIdentity {
	return delivery.PublisherIdentity{Issuer: ghActions, Subject: repo + "/.github/workflows/ci.yml@refs/heads/main", Repository: repo, CI: true}
}

// gitopsCluster is a K8sClient serving list pages: pages maps
// "resource|namespace" to a page's items JSON, errs a list error. It
// records every list asked for.
type gitopsCluster struct {
	pages map[string]string
	errs  map[string]error
	asked []string
}

func (c *gitopsCluster) PreferredVersions(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"argoproj.io": "v1alpha1", "kustomize.toolkit.fluxcd.io": "v1", "helm.toolkit.fluxcd.io": "v2", "source.toolkit.fluxcd.io": "v1"}, nil
}

func (c *gitopsCluster) DiscoverCRD(context.Context) (*k8sclient.CRDDiscovery, error) {
	return nil, errors.New("unused")
}

func (c *gitopsCluster) ListJSON(context.Context, k8sclient.CRDDiscovery, string) ([]byte, error) {
	return nil, errors.New("unused")
}

func (c *gitopsCluster) ClusterIdentity(context.Context, string) (kubecluster.Identity, error) {
	return kubecluster.Identity{}, errors.New("unused")
}

func (c *gitopsCluster) ListPage(_ context.Context, gvr schema.GroupVersionResource, ns, _ string) ([]byte, string, error) {
	key := gvr.Resource + "|" + ns
	c.asked = append(c.asked, key)
	if err := c.errs[key]; err != nil {
		return nil, "", err
	}
	return []byte(`{"items":[` + c.pages[key] + `]}`), "", nil
}

// invoicesWorkload is billing/invoices running image with annotations and
// labels.
func invoicesWorkload(image string, labels, annotations map[string]string) string {
	b, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": "invoices", "namespace": "billing", "labels": labels, "annotations": annotations},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "invoices", "image": image}}}}}})
	return string(b)
}

// argoTracked is billing/invoices tracked by the unprefixed Argo CD
// Application invoices.
func argoTracked(image string) string {
	return invoicesWorkload(image, nil, map[string]string{"argocd.argoproj.io/tracking-id": "invoices:apps/Deployment:billing/invoices"})
}

func argoApp(ns, spec string) string {
	return fmt.Sprintf(`{"metadata":{"name":"invoices","namespace":%q},"spec":%s,"status":{"resources":[{"group":"apps","kind":"Deployment","namespace":"billing","name":"invoices"}]}}`, ns, spec)
}

func gitRepository(url string) string {
	return fmt.Sprintf(`{"metadata":{"name":"platform","namespace":"flux-system"},"spec":{"url":%q}}`, url)
}

// withCluster makes e's service read c for the repository.
func (e *declareEnv) withCluster(c *gitopsCluster) {
	e.svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) { return c, nil }
}

func TestResolveSignedProvenanceFromBaseline(t *testing.T) {
	e := newDeclareEnv(t)
	e.withBaseline(t)
	req := e.req()
	req.Dir = e.dirFolder(t, e.invoicesBaseline("1.4.0", ledgerDependency)["pacto.yaml"], nil)
	p, d := e.plan(t, req)
	want := &contract.InferredSourceRepository{URL: invoicesRepo, Rung: contract.RepositoryRungSigned, Signer: invoicesWF, Folder: ".", FolderRung: contract.FolderRungDefault}
	if !reflect.DeepEqual(p.Repository, want) || !reflect.DeepEqual(d.record.Declaration.Repository, want) || p.RepositoryTried != nil {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
	rec, err := contract.ParseInferredRecord(d.record.Marshal())
	if err != nil || !reflect.DeepEqual(rec.Declaration.Repository, want) {
		t.Fatalf("%v %+v", err, rec)
	}
}

// signedImage pushes an image into the env's registry, signed by the CI
// identity of each repository, and returns its pinned reference.
func (e *declareEnv) signedImage(t *testing.T, repos ...string) string {
	t.Helper()
	repo := e.host + "/acme/app"
	d := e.pushImage(t, repo, "1.0.0")
	for _, r := range repos {
		s := e.signer(t, testutil.CIToken(r+"/.github/workflows/ci.yml@refs/heads/main", ghActions, r, "refs/heads/main"))
		if err := publish.SignDigest(t.Context(), e.reg, s, repo, d); err != nil {
			t.Fatal(err)
		}
	}
	return repo + "@" + d
}

func TestResolveSignedProvenanceFromImage(t *testing.T) {
	e := newDeclareEnv(t)
	e.withCluster(&gitopsCluster{pages: map[string]string{"deployments|billing": invoicesWorkload(e.signedImage(t, "https://github.com/acme/elsewhere"), nil, nil)}})
	p, _ := e.plan(t, e.req())
	if r := p.Repository; r == nil || r.URL != "https://github.com/acme/elsewhere" || r.Rung != contract.RepositoryRungSigned ||
		r.Signer != "https://github.com/acme/elsewhere/.github/workflows/ci.yml@refs/heads/main" {
		t.Fatalf("%+v %+v", r, p.RepositoryTried)
	}
}

// TestSignedProvenanceNeedsNoAllowlist: a CI certificate naming any
// repository answers, with no map or rule naming it.
func TestSignedProvenanceNeedsNoAllowlist(t *testing.T) {
	a := resolveRepository(repoInputs{ImageSigners: []delivery.PublisherIdentity{ciID("https://git.example.org/nobody/listed")}})
	if a.Repo == nil || a.Repo.URL != "https://git.example.org/nobody/listed" || a.Repo.Rung != contract.RepositoryRungSigned {
		t.Fatalf("%+v", a)
	}
}

func TestAmbiguousSignedProvenanceGivesNothing(t *testing.T) {
	two := []delivery.PublisherIdentity{ciID("https://github.com/acme/a"), ciID("https://github.com/acme/b"), ciID("https://github.com/acme/a")}
	a := resolveRepository(repoInputs{ImageSigners: two})
	if a.Repo != nil || len(a.Tried) != 4 || a.Tried[0].Reason != "2 signed repositories" {
		t.Fatalf("%+v", a)
	}
	base := ciID(invoicesRepo)
	if a := resolveRepository(repoInputs{Baseline: &base, ImageSigners: two}); a.Repo == nil || a.Repo.URL != invoicesRepo {
		t.Fatalf("the baseline did not answer alone: %+v", a)
	}
}

func TestPersonSignatureNamesNoRepository(t *testing.T) {
	person := delivery.PublisherIdentity{Issuer: "https://accounts.example", Subject: "ana@acme.com", Repository: invoicesRepo}
	a := resolveRepository(repoInputs{Baseline: &person, ImageSigners: []delivery.PublisherIdentity{person}})
	if a.Repo != nil || a.Tried[0].Reason != "baseline: its signer names no repository; image: no CI signature names a repository" {
		t.Fatalf("%+v", a)
	}
	for _, id := range []delivery.PublisherIdentity{
		{CI: true, Subject: invoicesWF, Repository: "file:///etc"},
		{CI: true, Subject: strings.Repeat("s", 513), Repository: invoicesRepo},
		{CI: true, Subject: "a\nb", Repository: invoicesRepo},
		{CI: true, Repository: invoicesRepo},
	} {
		if ciRepository(&id) != nil {
			t.Errorf("%+v answered", id)
		}
	}
}

func TestNoReferrerReasonNamesLegacyTags(t *testing.T) {
	e := newDeclareEnv(t)
	img := e.signedImage(t)
	e.withCluster(&gitopsCluster{pages: map[string]string{"deployments|billing": invoicesWorkload(img, nil, nil)}})
	p, _ := e.plan(t, e.req())
	if p.Repository != nil || !strings.Contains(p.RepositoryTried[0].Reason, "no Sigstore bundle referrer (legacy cosign .sig tags are not read)") {
		t.Fatalf("%+v", p.RepositoryTried)
	}
}

// modeRegistry reports mode for every repository, or fails.
type modeRegistry struct {
	publish.Registry
	mode capabilities.ReferrersMode
	err  error
}

func (m modeRegistry) Mode(context.Context, string, string) (capabilities.ReferrersMode, error) {
	return m.mode, m.err
}

func TestImageProvenanceWithoutReferrersAPIIsAReason(t *testing.T) {
	e := newDeclareEnv(t)
	img := e.signedImage(t)
	ref, _ := name.ParseReference(img)
	for mode, want := range map[capabilities.ReferrersMode]bool{capabilities.ReferrersWriteOnceTags: true, capabilities.ReferrersNative: false} {
		pl := &declarePlanner{reg: modeRegistry{e.reg, mode, nil}, req: e.req()}
		why := pl.signersWhy(t.Context(), ref.Context().Name(), ref.Identifier(), publish.ErrNoSignature)
		if got := strings.Contains(why, "the registry has no Referrers API; attestations under the sha256- tag schema are not read"); got != want {
			t.Errorf("%s: %q", mode, why)
		}
	}
	pl := &declarePlanner{reg: modeRegistry{e.reg, "", errors.New("probe failed")}}
	if why := pl.signersWhy(t.Context(), "r", "d", publish.ErrNoSignature); strings.Contains(why, "Referrers API") {
		t.Errorf("an unread mode named one: %q", why)
	}
	if why := pl.signersWhy(t.Context(), "r", "d", errors.New("registry down")); why != "the image's signatures were not read: registry down" {
		t.Errorf("%q", why)
	}
}

// blockingReferrers lists referrers only after its context ends.
type blockingReferrers struct{ publish.Registry }

func (blockingReferrers) Referrers(ctx context.Context, _, _, _ string) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestImageReadTimeoutIsAReason(t *testing.T) {
	old := imageReadTimeout
	imageReadTimeout = 10 * time.Millisecond
	t.Cleanup(func() { imageReadTimeout = old })
	e := newDeclareEnv(t)
	img := e.signedImage(t, invoicesRepo)
	e.withCluster(&gitopsCluster{pages: map[string]string{"deployments|billing": invoicesWorkload(img, nil, nil)}})
	req := e.req()
	req.Store = blockingReferrers{e.reg}
	p, _ := e.plan(t, req)
	if p.Repository != nil || !strings.Contains(p.RepositoryTried[0].Reason, "the image read timed out after 10ms") {
		t.Fatalf("%+v", p.RepositoryTried)
	}
}

func TestImageSignersReasons(t *testing.T) {
	e := newDeclareEnv(t)
	pl := &declarePlanner{reg: e.reg, req: e.req(), rec: &contract.InferredRecord{}}
	for image, want := range map[string]string{
		"":                       "the workload names no image",
		"Not A Reference":        "the image reference is not readable",
		e.host + "/acme/app:1.0": "the running image is not pinned by a digest Pacto read",
	} {
		if _, why := pl.imageSigners(t.Context(), image); why != want {
			t.Errorf("%q: %q", image, why)
		}
	}
	pl.req.Signing.TrustedRoot = "/nonexistent/root.json"
	if _, why := pl.imageSigners(t.Context(), e.host+"/acme/app@sha256:"+strings.Repeat("a", 64)); !strings.HasPrefix(why, "the Sigstore trusted root was not read: ") {
		t.Errorf("%q", why)
	}
}

func TestImageDigestsFromRecord(t *testing.T) {
	d1, d2, d3 := "sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("2", 64), "sha256:"+strings.Repeat("3", 64)
	pl := &declarePlanner{rec: &contract.InferredRecord{Fields: []contract.Inferred{{Field: "service.owner", Value: "x"},
		{Field: "image.digest[a]", Value: d1 + ", " + d2}, {Field: "image.digest[b]", Value: d1 + ", " + d3}}}}
	ref, _ := name.ParseReference("reg.io/acme/app:1")
	if got := pl.imageDigests(ref); !reflect.DeepEqual(got, []string{d1, d2}) {
		t.Fatalf("%v", got)
	}
	pinned, _ := name.ParseReference("reg.io/acme/app:1@" + d3)
	if got := pl.imageDigests(pinned); !reflect.DeepEqual(got, []string{d3}) {
		t.Fatalf("%v", got)
	}
}

// planGitOps plans e's invoices against a cluster where c serves
// billing/invoices as workload and the GitOps objects.
func planGitOps(t *testing.T, pages map[string]string) (*DeclarePlan, *declaration, *gitopsCluster) {
	t.Helper()
	e := newDeclareEnv(t)
	c := &gitopsCluster{pages: pages}
	e.withCluster(c)
	p, d := e.plan(t, e.req())
	return p, d, c
}

func TestResolveGitOpsArgo(t *testing.T) {
	p, d, _ := planGitOps(t, map[string]string{
		"deployments|billing": argoTracked("reg.io/acme/invoices:1"),
		"applications|argocd": argoApp("argocd", `{"source":{"repoURL":"git@github.com:Acme/Invoices.git","path":"deploy/"}}`),
	})
	want := &contract.InferredSourceRepository{URL: "https://github.com/Acme/Invoices", Rung: contract.RepositoryRungGitOps, Folder: "deploy", FolderRung: contract.RepositoryRungGitOps}
	if !reflect.DeepEqual(p.Repository, want) {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
	block := string(d.record.Marshal())
	if !strings.Contains(block, "rung: gitops") || !strings.Contains(block, "folderRung: gitops") || strings.Contains(block, "argocd") {
		t.Fatalf("%s", block)
	}
}

// TestResolveGitOpsArgoInArgocdNamespace: the Application lives in argocd,
// which the first read of billing never lists.
func TestResolveGitOpsArgoInArgocdNamespace(t *testing.T) {
	p, _, c := planGitOps(t, map[string]string{
		"deployments|billing": argoTracked("reg.io/acme/invoices:1"),
		"applications|argocd": argoApp("argocd", `{"source":{"repoURL":"https://github.com/acme/invoices"}}`),
	})
	if p.Repository == nil || p.Repository.Rung != contract.RepositoryRungGitOps || p.Repository.Folder != "." || p.Repository.FolderRung != contract.RepositoryRungGitOps {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
	if !strings.Contains(strings.Join(c.asked, " "), "applications|argocd") {
		t.Fatalf("argocd was never read: %v", c.asked)
	}
}

func TestResolveGitOpsFluxKustomization(t *testing.T) {
	p, _, _ := planGitOps(t, map[string]string{
		"deployments|billing": invoicesWorkload("reg.io/acme/invoices:1",
			map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil),
		"kustomizations|flux-system": `{"metadata":{"name":"apps","namespace":"flux-system"},"spec":{"sourceRef":{"kind":"GitRepository","name":"platform"},"path":"./apps/invoices"},` +
			`"status":{"inventory":{"entries":[{"id":"billing_invoices_apps_Deployment","v":"v1"}]}}}`,
		"gitrepositories|flux-system": gitRepository("ssh://git@github.com:22/acme/platform.git"),
	})
	want := &contract.InferredSourceRepository{URL: "https://github.com/acme/platform", Rung: contract.RepositoryRungGitOps, Folder: "apps/invoices", FolderRung: contract.RepositoryRungGitOps}
	if !reflect.DeepEqual(p.Repository, want) {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
}

// TestResolveGitOpsFluxInFluxSystem: the Kustomization and its
// GitRepository live in flux-system.
func TestResolveGitOpsFluxInFluxSystem(t *testing.T) {
	p, _, c := planGitOps(t, map[string]string{
		"deployments|billing": invoicesWorkload("reg.io/acme/invoices:1",
			map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil),
		"kustomizations|flux-system": `{"metadata":{"name":"apps","namespace":"flux-system"},"spec":{"sourceRef":{"kind":"GitRepository","name":"platform"},"path":""},` +
			`"status":{"inventory":{"entries":[{"id":"billing_invoices_apps_Deployment","v":"v1"}]}}}`,
		"gitrepositories|flux-system": gitRepository("https://github.com/acme/platform"),
	})
	if p.Repository == nil || p.Repository.Rung != contract.RepositoryRungGitOps || p.Repository.Folder != "." {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
	if !strings.Contains(strings.Join(c.asked, " "), "gitrepositories|flux-system") {
		t.Fatalf("flux-system was never read: %v", c.asked)
	}
}

func TestResolveGitOpsFluxHelmRelease(t *testing.T) {
	p, _, _ := planGitOps(t, map[string]string{
		"deployments|billing": invoicesWorkload("reg.io/acme/invoices:1",
			map[string]string{"helm.toolkit.fluxcd.io/name": "invoices", "helm.toolkit.fluxcd.io/namespace": "flux-system"}, nil),
		"helmreleases|flux-system": `{"metadata":{"name":"invoices","namespace":"flux-system"},"spec":{"targetNamespace":"billing",` +
			`"chart":{"spec":{"chart":"./charts/invoices","sourceRef":{"kind":"GitRepository","name":"platform"}}}}}`,
		"gitrepositories|flux-system": gitRepository("https://github.com/acme/platform/"),
	})
	want := &contract.InferredSourceRepository{URL: "https://github.com/acme/platform", Rung: contract.RepositoryRungGitOps, Folder: "charts/invoices", FolderRung: contract.RepositoryRungGitOps}
	if !reflect.DeepEqual(p.Repository, want) {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
}

func TestAmbiguousGitOpsGivesNothing(t *testing.T) {
	a := resolveRepository(repoInputs{GitOps: []fleet.InferredRepository{{URL: "https://github.com/acme/a"}, {URL: "https://github.com/acme/b"}, {URL: "not a url"}}})
	if a.Repo != nil || a.Tried[1] != (RungNote{Rung: contract.RepositoryRungGitOps, Reason: "2 GitOps repositories"}) {
		t.Fatalf("%+v", a)
	}
}

func TestFolderFromGitOpsOnlyForSameRepository(t *testing.T) {
	base := ciID(invoicesRepo)
	a := resolveRepository(repoInputs{Baseline: &base, GitOps: []fleet.InferredRepository{{URL: "https://github.com/acme/platform", Path: "apps/invoices"}}})
	if a.Repo.Folder != "." || a.Repo.FolderRung != contract.FolderRungDefault {
		t.Fatalf("%+v", a.Repo)
	}
	a = resolveRepository(repoInputs{Baseline: &base, GitOps: []fleet.InferredRepository{{URL: invoicesRepo + ".git", Path: "deploy"}}})
	if a.Repo.Folder != "deploy" || a.Repo.FolderRung != contract.RepositoryRungGitOps {
		t.Fatalf("%+v", a.Repo)
	}
	// A path a record cannot hold leaves the folder at the root.
	a = resolveRepository(repoInputs{GitOps: []fleet.InferredRepository{{URL: invoicesRepo, Path: "../escape"}}})
	if a.Repo.Folder != "." || a.Repo.FolderRung != contract.FolderRungDefault {
		t.Fatalf("%+v", a.Repo)
	}
}

func TestFolderDefaultIsRoot(t *testing.T) {
	base := ciID(invoicesRepo)
	if a := resolveRepository(repoInputs{Baseline: &base}); a.Repo.Folder != "." || a.Repo.FolderRung != contract.FolderRungDefault {
		t.Fatalf("%+v", a.Repo)
	}
}

func TestNoAnswerNoBlock(t *testing.T) {
	a := resolveRepository(repoInputs{})
	want := []RungNote{{contract.RepositoryRungSigned, "baseline: no published baseline; image: no CI signature names a repository"},
		{contract.RepositoryRungGitOps, "no GitOps source lists this workload"},
		{contract.RepositoryRungImageLabel, "no image label names a repository"}, {contract.RepositoryRungMap, "no --repo-map given"}}
	if a.Repo != nil || !reflect.DeepEqual(a.Tried, want) {
		t.Fatalf("%+v", a)
	}
	e := newDeclareEnv(t)
	p, d := e.plan(t, e.req())
	if p.Repository != nil || d.record.Declaration.Repository != nil || strings.Contains(string(d.record.Marshal()), "repository:") ||
		!strings.Contains(p.RepositoryTried[1].Reason, "no kubeconfig") {
		t.Fatalf("%+v\n%s", p.RepositoryTried, d.record.Marshal())
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/invoices":           invoicesRepo,
		"https://GitHub.COM/acme/invoices/":          invoicesRepo,
		"git@github.com:acme/invoices.git":           invoicesRepo,
		"ssh://git@github.com:22/acme/invoices.git":  invoicesRepo,
		"git://github.com/acme/invoices":             invoicesRepo,
		"git+ssh://github.com/acme/invoices":         invoicesRepo,
		"https://user:secret@github.com/acme/r.git":  "https://github.com/acme/r",
		"https://github.com/acme/r?ref=main#frag":    "https://github.com/acme/r",
		"http://127.0.0.1:3000/acme/invoices.git/":   "http://127.0.0.1:3000/acme/invoices",
		"https://git.example.org:8443/group/sub/svc": "https://git.example.org:8443/group/sub/svc",
		" https://github.com/acme/invoices ":         invoicesRepo,
	} {
		if got, ok := normalizeRepoURL(in); !ok || got != want {
			t.Errorf("normalizeRepoURL(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "file:///etc/passwd", "ftp://host/o/r", "https://github.com/", "github.com", "https://[::1]/acme/r", "git@host:a b"} {
		if got, ok := normalizeRepoURL(in); ok {
			t.Errorf("normalizeRepoURL(%q) = %q, want refused", in, got)
		}
	}
}

// TestNormalizedURLAlwaysParses: every URL normalizeRepoURL answers is one a
// record holds.
func TestNormalizedURLAlwaysParses(t *testing.T) {
	for _, in := range []string{invoicesRepo, "git@GitLab.com:g/s/p.git", "ssh://h.example:2222/x", "http://localhost:3000/o/r", "https://h/o/r.git.git", "https://h/a%20b"} {
		u, ok := normalizeRepoURL(in)
		if !ok {
			continue
		}
		if !contract.ValidRepositoryURL(u) {
			t.Errorf("%q gave %q, which a record refuses", in, u)
		}
	}
}

func TestSignerLabel(t *testing.T) {
	for in, want := range map[string]string{
		invoicesWF: "workflow release.yml@refs/heads/main",
		"https://gitlab.com/acme/invoices//.gitlab-ci.yml@refs/heads/main": "pipeline .gitlab-ci.yml@refs/heads/main",
		"https://ci.example/build/42":                                      "https://ci.example/build/42",
		"ana@acme.com":                                                     "ana@acme.com",
	} {
		if got := signerLabel(in); got != want {
			t.Errorf("signerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepositoryText(t *testing.T) {
	r := &contract.InferredSourceRepository{URL: invoicesRepo, Rung: contract.RepositoryRungSigned, Signer: invoicesWF, Folder: "deploy", FolderRung: contract.RepositoryRungGitOps}
	if got := RepositoryText(r); got != "github.com/acme/invoices (signed provenance, workflow release.yml@refs/heads/main), folder: deploy/ (GitOps source)" {
		t.Errorf("%q", got)
	}
	r = &contract.InferredSourceRepository{URL: invoicesRepo, Rung: contract.RepositoryRungGitOps, Folder: ".", FolderRung: contract.FolderRungDefault}
	if got := RepositoryText(r); got != "github.com/acme/invoices (GitOps source), folder: . (no source names the folder; pass --folder)" {
		t.Errorf("%q", got)
	}
}

func TestCollectWithDirReadsNoWorkload(t *testing.T) {
	pl := &declarePlanner{s: &Service{sys: sysDeps{newK8s: func(string) (k8sclient.K8sClient, error) {
		t.Fatal("a --dir run read the cluster for the repository")
		return nil, nil
	}}}, req: DeclareRequest{Dir: "/some/folder"}}
	if in := pl.collectRepoInputs(t.Context(), givenRepo{}); in.GitOpsWhy != dirReadsNoWorkload || in.ImageWhy != dirReadsNoWorkload {
		t.Fatalf("%+v", in)
	}
}

func TestCollectPicksTheDraftedWorkload(t *testing.T) {
	other := strings.Replace(argoTracked("reg.io/acme/other:1"), `"name":"invoices"`, `"name":"invoices-v2"`, 1)
	p, _, _ := planGitOps(t, map[string]string{
		"deployments|billing": other + "," + invoicesWorkload("reg.io/acme/invoices:1", nil, nil),
		"applications|argocd": argoApp("argocd", `{"source":{"repoURL":"https://github.com/acme/other"}}`),
	})
	if p.Repository != nil || p.RepositoryTried[1].Reason != "no GitOps source lists this workload" {
		t.Fatalf("another workload's repository answered: %+v %+v", p.Repository, p.RepositoryTried)
	}
	// A cluster that lists no such workload names why.
	p, _, _ = planGitOps(t, map[string]string{})
	if p.RepositoryTried[1].Reason != "the workload was not read for the repository" {
		t.Fatalf("%+v", p.RepositoryTried)
	}
}

func TestCollectWorkloadListFails(t *testing.T) {
	e := newDeclareEnv(t)
	errs := map[string]error{}
	for _, r := range []string{"deployments", "statefulsets", "daemonsets", "cronjobs"} {
		errs[r+"|billing"] = errors.New("forbidden")
	}
	e.withCluster(&gitopsCluster{errs: errs})
	p, _ := e.plan(t, e.req())
	if !strings.HasPrefix(p.RepositoryTried[1].Reason, "the workload was not read for the repository: ") {
		t.Fatalf("%+v", p.RepositoryTried)
	}
}

func TestGitOpsNamespaces(t *testing.T) {
	for _, c := range []struct {
		w    fleet.RawInferredService
		want []string
	}{
		{fleet.RawInferredService{}, []string{"billing"}},
		{fleet.RawInferredService{Group: &fleet.InferredGroup{Rule: "argocd"}}, []string{"billing", "argocd", "flux-system"}},
		{fleet.RawInferredService{Group: &fleet.InferredGroup{Rule: "flux-kustomization", Namespace: "flux-system"},
			RepositoryUnknownFrom: []fleet.WorkloadScope{{Namespace: "flux-system"}, {Namespace: "sources"}, {Namespace: ""}, {Namespace: "billing"}}},
			[]string{"billing", "flux-system", "sources"}},
	} {
		if got := gitopsNamespaces("billing", c.w); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v: %v, want %v", c.w, got, c.want)
		}
	}
}

// TestCollectForbiddenGitOpsNamespaceIsAReason: the person may not list
// Applications in argocd: the reason names where Pacto looked and what to
// pass instead.
func TestCollectForbiddenGitOpsNamespaceIsAReason(t *testing.T) {
	p, _, _ := func() (*DeclarePlan, *declaration, *gitopsCluster) {
		e := newDeclareEnv(t)
		c := &gitopsCluster{pages: map[string]string{"deployments|billing": argoTracked("reg.io/acme/invoices:1")},
			errs: map[string]error{"applications|argocd": errors.New("forbidden")}}
		e.withCluster(c)
		p, d := e.plan(t, e.req())
		return p, d, c
	}()
	why := p.RepositoryTried[1].Reason
	if p.Repository != nil || !strings.Contains(why, "namespaces read: billing, argocd, flux-system") || !strings.Contains(why, "pass --repository <url>") {
		t.Fatalf("%q", why)
	}
}

// TestHTTPPlanUsesViewerRepositories: an HTTP surface hands in the
// caller's restricted view: what it dropped never answers, and the
// service's own cluster client is never built.
func TestHTTPPlanUsesViewerRepositories(t *testing.T) {
	e := newDeclareEnv(t)
	e.svc.sys.newK8s = func(string) (k8sclient.K8sClient, error) {
		t.Fatal("an HTTP plan built the service's own cluster client")
		return nil, nil
	}
	req := e.req()
	req.Observed = &DeclareObserved{Namespace: "billing", Kind: "Deployment", Name: "invoices"}
	if p, _ := e.plan(t, req); p.Repository != nil || p.RepositoryTried[1].Reason != "no GitOps source lists this workload" {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
	req.Observed.Repositories = []fleet.InferredRepository{{URL: invoicesRepo, Path: "deploy"}}
	if p, _ := e.plan(t, req); p.Repository == nil || p.Repository.URL != invoicesRepo || p.Repository.Folder != "deploy" {
		t.Fatalf("%+v %+v", p.Repository, p.RepositoryTried)
	}
	req.Observed.Name = "someone-else"
	if p, _ := e.plan(t, req); p.Repository != nil || p.RepositoryTried[1].Reason != "the workload is not in your fleet view" {
		t.Fatalf("%+v", p.RepositoryTried)
	}
}

// imageReadCounter counts the reads of the image repository repo.
type imageReadCounter struct {
	publish.Registry
	repo  string
	reads *int
}

func (c imageReadCounter) Referrers(ctx context.Context, repo, digest, at string) ([]string, error) {
	if repo == c.repo {
		*c.reads++
	}
	return c.Registry.Referrers(ctx, repo, digest, at)
}

func TestHTTPPlanNeverReadsForeignImage(t *testing.T) {
	e := newDeclareEnv(t)
	img := e.signedImage(t, invoicesRepo)
	ref, _ := name.ParseReference(img)
	reads := 0
	req := e.req()
	req.Store = imageReadCounter{e.reg, ref.Context().Name(), &reads}
	req.Registry = e.host + "/elsewhere"
	req.Observed = &DeclareObserved{Namespace: "billing", Kind: "Deployment", Name: "invoices", Image: img}
	p, _ := e.plan(t, req)
	if reads != 0 || !strings.Contains(p.RepositoryTried[0].Reason, "image not read on the server: run pacto declare locally") {
		t.Fatalf("%d reads: %+v", reads, p.RepositoryTried)
	}
	// Inside the installation's registry the image is read.
	req.Registry = e.host + "/acme"
	if p, _ := e.plan(t, req); reads == 0 || p.Repository == nil || p.Repository.URL != invoicesRepo {
		t.Fatalf("%d reads: %+v %+v", reads, p.Repository, p.RepositoryTried)
	}
}

// TestRepositoryAnswerNeverChangesGate: the same declaration with and
// without a repository answer is decided alike: its state, signer, version,
// publish plan and intent digest.
func TestRepositoryAnswerNeverChangesGate(t *testing.T) {
	e := newDeclareEnv(t)
	req := e.req()
	without, _ := e.plan(t, req)
	req.Observed = &DeclareObserved{Namespace: "billing", Kind: "Deployment", Name: "invoices", Repositories: []fleet.InferredRepository{{URL: invoicesRepo}}}
	with, _ := e.plan(t, req)
	if with.Repository == nil || without.Repository != nil {
		t.Fatalf("%+v %+v", with.Repository, without.Repository)
	}
	if with.State != without.State || with.Signer != without.Signer || with.Version != without.Version || with.IntentDigest != without.IntentDigest ||
		with.Publish.Classification != without.Publish.Classification || !reflect.DeepEqual(with.Publish.Checks, without.Publish.Checks) {
		t.Fatalf("the repository answer moved the plan:\n%+v\n%+v", with, without)
	}
}
