package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// goldenPlan is a fixed plan the intent digest was computed for before
// --pr existed.
func goldenPlan() *DeclarePlan {
	return &DeclarePlan{InferredFrom: "k8s:billing/Deployment/invoices", Service: "invoices", Version: "1.5.0", BundleRepo: "ghcr.io/acme/invoices",
		Baseline: &DeclareBaseline{InferredBaseline: contract.InferredBaseline{Bundle: "ghcr.io/acme/invoices@sha256:" + "ab"}}}
}

// TestIntentDigestUnchangedWithoutPR: a plan without --pr digests to the
// same bytes it did before --pr existed, its repository answer included.
func TestIntentDigestUnchangedWithoutPR(t *testing.T) {
	t.Parallel()
	p := goldenPlan()
	acc := []contract.InferredAcceptance{{Field: "dependencies.ledger.compatibility", Range: "^1.0.0"}}
	const golden = "sha256:1bf4388e116a352353f318ba0bb37c869cdcb9968a535199f6a22e8b41492380"
	if got := intentDigest(p, []byte("pactoVersion: \"2.5\"\n"), acc, ""); got != golden {
		t.Fatalf("%s", got)
	}
}

// labelledImage pushes an image whose config labels its source src (an
// index annotated with src when index is set) into e's registry and
// returns its pinned reference.
func (e *declareEnv) labelledImage(t *testing.T, src string, index bool) string {
	t.Helper()
	img, _ := random.Image(16, 1)
	var a publish.Artifact = img
	if src != "" && !index {
		cfg, _ := img.ConfigFile()
		cfg.Config.Labels = map[string]string{publish.AnnotationSource: src}
		img, _ = mutate.ConfigFile(img, cfg)
		a = img
	}
	if index {
		a = mutate.Annotations(mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img}), map[string]string{publish.AnnotationSource: src}).(v1.ImageIndex)
	}
	repo := e.host + "/acme/app"
	d, err := e.reg.Write(t.Context(), repo, a)
	if err != nil {
		t.Fatal(err)
	}
	return repo + "@" + d
}

// planImage plans e's invoices running image img, with req changed by edit.
func planImage(t *testing.T, e *declareEnv, img string, edit func(*DeclareRequest)) (*DeclarePlan, error) {
	t.Helper()
	e.withCluster(&gitopsCluster{pages: map[string]string{"deployments|billing": invoicesWorkload(img, nil, nil)}})
	req := e.req()
	if edit != nil {
		edit(&req)
	}
	p, _, err := e.svc.PlanDeclare(t.Context(), req)
	return p, err
}

func TestResolveImageLabelFromIndexAnnotation(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, err := planImage(t, e, e.labelledImage(t, "https://github.com/Acme/Invoices.git", true), nil)
	want := &contract.InferredSourceRepository{URL: "https://github.com/Acme/Invoices", Rung: contract.RepositoryRungImageLabel, Folder: ".", FolderRung: contract.FolderRungDefault}
	if err != nil || !reflect.DeepEqual(p.Repository, want) || len(p.RepositoryTried) != 2 {
		t.Fatalf("%v %+v %+v", err, p.Repository, p.RepositoryTried)
	}
}

func TestResolveImageLabelFromConfig(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	p, err := planImage(t, e, e.labelledImage(t, "https://github.com/acme/invoices", false), nil)
	if err != nil || p.Repository == nil || p.Repository.URL != invoicesRepo || p.Repository.Rung != contract.RepositoryRungImageLabel {
		t.Fatalf("%v %+v %+v", err, p.Repository, p.RepositoryTried)
	}
	// A label that is no repository URL, and an image with no label, name why.
	if a, _ := labelRung(repoInputs{Label: "not a url"}); a != nil {
		t.Fatalf("%+v", a)
	}
	if p, _ := planImage(t, e, e.labelledImage(t, "", false), nil); p.Repository != nil ||
		p.RepositoryTried[2].Reason != "the image names no org.opencontainers.image.source label" {
		t.Fatalf("%+v", p.RepositoryTried)
	}
}

// blockingSource reads an image's source only after its context ends.
type blockingSource struct{ publish.Registry }

func (blockingSource) ImageSource(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// TestUnreadableImageIsAReason: an image the registry does not hold, or
// one whose read times out, is a reason: the plan goes on.
func TestUnreadableImageIsAReason(t *testing.T) {
	e := newDeclareEnv(t)
	absent := e.host + "/acme/app@sha256:" + strings.Repeat("0", 64)
	p, err := planImage(t, e, absent, nil)
	if err != nil || p.Repository != nil || !strings.HasPrefix(p.RepositoryTried[2].Reason, "the image was not read: ") {
		t.Fatalf("%v %+v", err, p.RepositoryTried)
	}
	old := imageReadTimeout
	imageReadTimeout = 300 * time.Millisecond
	t.Cleanup(func() { imageReadTimeout = old })
	p, err = planImage(t, e, e.labelledImage(t, invoicesRepo, false), func(r *DeclareRequest) { r.Store = blockingSource{e.reg} })
	if err != nil || p.Repository != nil || p.RepositoryTried[2].Reason != "the image read timed out after 300ms" {
		t.Fatalf("%v %+v", err, p.RepositoryTried)
	}
}

func TestGivenOverridesEveryRung(t *testing.T) {
	t.Parallel()
	base := ciID(invoicesRepo)
	in := repoInputs{Given: givenRepo{URL: "https://gitlab.com/acme/x"}, Baseline: &base, GitOps: []fleet.InferredRepository{{URL: invoicesRepo, Path: "deploy"}},
		Label: invoicesRepo, Map: &repoMapEntry{Repository: invoicesRepo, Folder: "m"}}
	a := resolveRepository(in)
	want := &contract.InferredSourceRepository{URL: "https://gitlab.com/acme/x", Rung: contract.RepositoryRungGiven, Folder: ".", FolderRung: contract.FolderRungDefault}
	if !reflect.DeepEqual(a.Repo, want) || a.Tried != nil {
		t.Fatalf("%+v", a)
	}
	// Through a plan: the flags are normalized, and a value a record cannot
	// hold is refused before anything is read.
	e := newDeclareEnv(t)
	req := e.req()
	req.Repository, req.Folder = "git@GitHub.com:acme/invoices.git", "deploy/"
	p, _ := e.plan(t, req)
	if r := p.Repository; r == nil || r.URL != invoicesRepo || r.Rung != contract.RepositoryRungGiven || r.Folder != "deploy" || r.FolderRung != contract.RepositoryRungGiven {
		t.Fatalf("%+v", r)
	}
	for _, c := range []struct{ repo, folder, want string }{
		{"not a url", "", "--repository"}, {invoicesRepo, "../up", "--folder"},
	} {
		req.Repository, req.Folder = c.repo, c.folder
		if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestFolderFlagOverridesGitOpsPath(t *testing.T) {
	t.Parallel()
	a := resolveRepository(repoInputs{Given: givenRepo{Folder: "mine"}, GitOps: []fleet.InferredRepository{{URL: invoicesRepo, Path: "deploy"}}})
	if a.Repo == nil || a.Repo.Rung != contract.RepositoryRungGitOps || a.Repo.Folder != "mine" || a.Repo.FolderRung != contract.RepositoryRungGiven {
		t.Fatalf("%+v", a.Repo)
	}
}

func TestMapAnswersLast(t *testing.T) {
	t.Parallel()
	m := &repoMapEntry{Repository: invoicesRepo, Folder: "svc/invoices"}
	a := resolveRepository(repoInputs{Map: m})
	want := &contract.InferredSourceRepository{URL: invoicesRepo, Rung: contract.RepositoryRungMap, Folder: "svc/invoices", FolderRung: contract.RepositoryRungMap}
	if !reflect.DeepEqual(a.Repo, want) || len(a.Tried) != 3 {
		t.Fatalf("%+v", a)
	}
	// An earlier rung answers first; the map's folder still names the
	// folder of the same repository, never of another.
	if a := resolveRepository(repoInputs{Label: invoicesRepo, Map: m}); a.Repo.Rung != contract.RepositoryRungImageLabel || a.Repo.Folder != "svc/invoices" {
		t.Fatalf("%+v", a.Repo)
	}
	if a := resolveRepository(repoInputs{Label: "https://github.com/acme/other", Map: m}); a.Repo.Folder != "." {
		t.Fatalf("%+v", a.Repo)
	}
}

// writeMap writes a repository map file and returns its path.
func writeMap(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "repos.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMapReadOnlyWithPR(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.RepoMap = filepath.Join(t.TempDir(), "absent.yaml")
	p, _ := e.plan(t, req)
	if !slices.Contains(p.Notes, mapNotRead) || p.RepositoryTried[3] != (RungNote{contract.RepositoryRungMap, mapNotRead}) {
		t.Fatalf("%+v %+v", p.Notes, p.RepositoryTried)
	}
	req.PR = true
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); err == nil || !strings.HasPrefix(err.Error(), "reading the repository map: ") {
		t.Fatalf("%v", err)
	}
	req.RepoMap = writeMap(t, "invoices: {repository: https://github.com/acme/invoices, folder: deploy}\n")
	p, _ = e.plan(t, req)
	if r := p.Repository; r == nil || r.Rung != contract.RepositoryRungMap || r.Folder != "deploy" || slices.Contains(p.Notes, mapNotRead) {
		t.Fatalf("%+v %+v", r, p.Notes)
	}
}

func TestMapStrictDecode(t *testing.T) {
	t.Parallel()
	for body, want := range map[string]string{
		"invoices: {repository: https://github.com/acme/invoices, from: x}\n": "field from not found",
		"invoices: {repository: ftp://h/x}\n":                                 "invoices.repository \"ftp://h/x\" is not a repository URL",
		"invoices: {repository: https://github.com/acme/i, folder: ../up}\n":  "invoices.folder: ",
		"[1, 2]\n":                   "cannot unmarshal",
		strings.Repeat("#", 1<<20+1): "larger than 1048576 bytes",
	} {
		if _, _, err := readRepoMap(writeMap(t, body), "invoices"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%.40q: %v", body, err)
		}
	}
	if _, _, err := readRepoMap(t.TempDir(), "invoices"); err == nil {
		t.Error("a folder read as a map")
	}
	for _, body := range []string{"", "ledger: {repository: https://github.com/acme/ledger}\n"} {
		if m, why, err := readRepoMap(writeMap(t, body), "invoices"); m != nil || err != nil || why != "the repository map does not name invoices" {
			t.Errorf("%q: %+v %q %v", body, m, why, err)
		}
	}
	m, _, err := readRepoMap(writeMap(t, "invoices: {repository: \"git@github.com:acme/invoices.git\"}\n"), "invoices")
	if err != nil || *m != (repoMapEntry{Repository: invoicesRepo}) {
		t.Fatalf("%+v %v", m, err)
	}
}

// cli is a confirmation command a test reads.
func cli(url string) string { return "pacto declare invoices --pr --repository " + url }

// code is a DeclareError's code, empty for nil.
func code(err error) string {
	var de *DeclareError
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

func TestLabelAnswerNeedsConfirmation(t *testing.T) {
	t.Parallel()
	in := repoInputs{Label: invoicesRepo}
	err := prCheck(in, resolveRepository(in), cli)
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codeRepositoryUnconfirmed || de.Message != "the image label is unsigned: confirm with --repository "+invoicesRepo ||
		de.CLI != cli(invoicesRepo) {
		t.Fatalf("%+v", err)
	}
}

func TestMapConfirmsLabel(t *testing.T) {
	t.Parallel()
	in := repoInputs{Label: invoicesRepo, Map: &repoMapEntry{Repository: invoicesRepo}}
	if err := prCheck(in, resolveRepository(in), cli); err != nil {
		t.Fatal(err)
	}
	in.Map.Repository = "https://github.com/acme/other"
	if err := prCheck(in, resolveRepository(in), cli); code(err) != codeRepositoryUnconfirmed {
		t.Fatalf("a map naming another repository confirmed the label: %v", err)
	}
}

func TestAgentNeverConfirmsLabel(t *testing.T) {
	t.Parallel()
	in := repoInputs{Agent: true, Label: invoicesRepo, Map: &repoMapEntry{Repository: invoicesRepo}}
	if err := prCheck(in, resolveRepository(in), cli); code(err) != codeRepositoryUnconfirmed {
		t.Fatalf("an agent confirmed its own label answer: %v", err)
	}
}

func TestAgentRepoMapNeverConfirms(t *testing.T) {
	t.Parallel()
	in := repoInputs{Agent: true, Map: &repoMapEntry{Repository: invoicesRepo}}
	if err := prCheck(in, resolveRepository(in), cli); code(err) != codeRepositoryUnconfirmed {
		t.Fatalf("an agent's map confirmed itself: %v", err)
	}
	// A person's map answer needs nothing more.
	in.Agent = false
	if err := prCheck(in, resolveRepository(in), cli); err != nil {
		t.Fatal(err)
	}
}

func TestAgentGivenRepositoryNeedsPerson(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Agent, req.PR, req.Repository = true, true, "https://github.com/acme/invoices"
	_, _, err := e.svc.PlanDeclare(t.Context(), req)
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codeRepositoryUnconfirmed || !strings.Contains(de.CLI, "--pr --repository "+invoicesRepo) {
		t.Fatalf("%+v", err)
	}
	// A GitOps source naming the same repository corroborates it.
	req.Observed = &DeclareObserved{Namespace: "billing", Kind: "Deployment", Name: "invoices", Repositories: []fleet.InferredRepository{{URL: invoicesRepo + ".git"}}}
	if p, _, err := e.svc.PlanDeclare(t.Context(), req); err != nil || p.Repository.Rung != contract.RepositoryRungGiven {
		t.Fatalf("%v", err)
	}
	// Without --pr the agent's value is recorded as given.
	req.PR, req.Observed = false, nil
	if p, _, err := e.svc.PlanDeclare(t.Context(), req); err != nil || p.Repository.Rung != contract.RepositoryRungGiven {
		t.Fatalf("%v", err)
	}
}

func TestPRWithoutAnswerRefused(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.PR = true
	_, _, err := e.svc.PlanDeclare(t.Context(), req)
	var de *DeclareError
	if !errors.As(err, &de) || de.Code != codeRepositoryUnknown || !strings.Contains(de.Message, "pass --repository <url> or add the service to --repo-map") ||
		de.CLI != "pacto declare invoices --namespace billing --registry "+e.host+"/acme --pr --repository <url>" {
		t.Fatalf("%+v", err)
	}
}

func TestIntentDigestBindsPRTarget(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	req := e.req()
	req.Repository = invoicesRepo
	without, _ := e.plan(t, req)
	req.PR = true
	with, _ := e.plan(t, req)
	req.Folder = "deploy"
	folder, _ := e.plan(t, req)
	req.Repository = "https://github.com/acme/elsewhere"
	other, _ := e.plan(t, req)
	if without.IntentDigest == with.IntentDigest || with.IntentDigest == folder.IntentDigest || folder.IntentDigest == other.IntentDigest {
		t.Fatalf("%s %s %s %s", without.IntentDigest, with.IntentDigest, folder.IntentDigest, other.IntentDigest)
	}
	// A run reviewed against one repository is refused against another.
	req.IntentDigest = folder.IntentDigest
	if _, err := e.svc.Declare(t.Context(), req); code(err) != codePlanStale {
		t.Fatalf("%v", err)
	}
}
