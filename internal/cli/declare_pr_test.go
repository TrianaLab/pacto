package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// prEnv sets GITHUB_TOKEN to token (empty for none) and clears every other
// Git host variable.
func prEnv(t *testing.T, token string) {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", token)
	for _, v := range []string{"GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GH_HOST", "GITLAB_TOKEN", "GITLAB_HOST", "BITBUCKET_TOKEN", "GITEA_TOKEN", "GITEA_HOST"} {
		t.Setenv(v, "")
	}
}

// prPlanFixture is declarePlanFixture with its repository found by the
// person's --repository, files in deploy/.
func prPlanFixture() *app.DeclarePlan {
	p := declarePlanFixture()
	p.Repository = &contract.InferredSourceRepository{URL: "https://github.com/acme/invoices", Rung: contract.RepositoryRungGiven, Folder: "deploy",
		FolderRung: contract.RepositoryRungGiven}
	return p
}

// gitHub is a GitHub API fake for acme/invoices that records every
// request and its body; fail answers one "METHOD path" with 403.
type gitHub struct {
	t     *testing.T
	calls []string
	trees []map[string]any
	pulls []map[string]any
	fail  string
}

func (g *gitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/repos/acme/invoices")
	g.calls = append(g.calls, key)
	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	}
	answers := map[string]string{"GET /user": `{"login":"ana"}`, "GET ": `{"default_branch":"main","private":true}`,
		"GET /git/ref/heads/main": `{"object":{"sha":"c0"}}`, "GET /git/commits/c0": `{"tree":{"sha":"t0"}}`, "POST /git/trees": `{"sha":"t1"}`,
		"POST /git/commits": `{"sha":"c1"}`, "POST /git/refs": `{}`, "POST /pulls": `{"html_url":"https://github.com/acme/invoices/pull/7"}`}
	switch key {
	case "POST /git/trees":
		g.trees = append(g.trees, body)
	case "POST /pulls":
		g.pulls = append(g.pulls, body)
	}
	status, out := http.StatusOK, answers[key]
	if key == g.fail {
		status, out = http.StatusForbidden, `{"message":"Resource not accessible by personal access token"}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(out)), Request: r}, nil
}

// committed is the files the n-th tree holds, by path.
func (g *gitHub) committed(n int) map[string]string {
	out := map[string]string{}
	for _, e := range g.trees[n]["tree"].([]any) {
		m := e.(map[string]any)
		out[m["path"].(string)] = m["content"].(string)
	}
	return out
}

// noHost fails the test on any request to the Git host.
type noHost struct{ t *testing.T }

func (h noHost) RoundTrip(r *http.Request) (*http.Response, error) {
	h.t.Errorf("a request reached the Git host: %s %s", r.Method, r.URL)
	return nil, errors.New("no host")
}

// noHelper is a credential helper that holds nothing.
func noHelper(context.Context, string) (string, error) { return "", errors.New("exit status 128") }

func TestPRPlanMakesNoHostRequest(t *testing.T) {
	noKube(t)
	prEnv(t, "ghp_secret")
	var got app.DeclareRequest
	d := declareDeps(t, &got, prPlanFixture(), nil, nil)
	d.gitTransport, d.gitCredentialFill = noHost{t}, noHelper
	out, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr")
	want := "pull request: github https://github.com/acme/invoices, branch pacto/declare-invoices-0.1.0, files deploy/pacto.yaml and deploy/pacto.inferred.yaml, with your GITHUB_TOKEN\n"
	if err != nil || !strings.Contains(out, want) || strings.Contains(out, "ghp_secret") {
		t.Fatalf("%v\n%s", err, out)
	}
	// A draft's branch takes the draft tag the write picks.
	p := prPlanFixture()
	p.State = contract.DeclarationDraft
	d = declareDeps(t, &got, p, nil, nil)
	d.gitTransport = noHost{t}
	if out, _, err = runRoot(t, d, nil, "declare", "invoices", "--pr"); err != nil || !strings.Contains(out, "branch pacto/declare-invoices-<next draft tag>,") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestPRRefusedBeforeWriteWithoutToken(t *testing.T) {
	noKube(t)
	prEnv(t, "")
	gitlab := prPlanFixture()
	gitlab.Repository.URL = "https://gitlab.example/acme/invoices"
	for _, c := range []struct {
		plan *app.DeclarePlan
		code string
	}{{prPlanFixture(), "GitTokenMissing"}, {gitlab, "GitHostUnknown"}} {
		wrote := false
		run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
			if err := req.OnPlan(c.plan); err != nil {
				return &app.DeclareResult{Plan: *c.plan}, err
			}
			wrote = true
			return &app.DeclareResult{}, nil
		}
		var got app.DeclareRequest
		d := declareDeps(t, &got, nil, nil, run)
		d.gitTransport, d.gitCredentialFill = noHost{t}, noHelper
		_, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--yes")
		var de *app.DeclareError
		if wrote || !errors.As(err, &de) || de.Code != c.code {
			t.Fatalf("%s: wrote %v, %v", c.code, wrote, err)
		}
	}
	// Without --yes the same refusal follows the plan.
	var got app.DeclareRequest
	d := declareDeps(t, &got, prPlanFixture(), nil, nil)
	d.gitCredentialFill = noHelper
	out, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr")
	if err == nil || !strings.Contains(err.Error(), "no Git token for github.com") || !strings.Contains(out, "plan: oci://") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDeclareGitHostFlag(t *testing.T) {
	noKube(t)
	prEnv(t, "ghp_secret")
	p := prPlanFixture()
	p.Repository.URL = "https://git.acme.example/acme/invoices"
	var got app.DeclareRequest
	d := declareDeps(t, &got, p, nil, nil)
	d.gitTransport, d.gitCredentialFill = noHost{t}, noHelper
	// --git-host names the API; the GitHub token never goes to another host.
	_, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--git-host", "gitea")
	var de *app.DeclareError
	if !errors.As(err, &de) || de.Code != "GitTokenMissing" || !strings.Contains(de.Message, "set GITEA_TOKEN for that host") {
		t.Fatal(err)
	}
	t.Setenv("GITEA_TOKEN", "gt")
	t.Setenv("GITEA_HOST", "git.acme.example")
	out, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--git-host", "gitea")
	if err != nil || !strings.Contains(out, "pull request: gitea https://git.acme.example/acme/invoices,") || !strings.Contains(out, "with your GITEA_TOKEN") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDeclarePRJSON(t *testing.T) {
	noKube(t)
	prEnv(t, "ghp_secret")
	var got app.DeclareRequest
	d := declareDeps(t, &got, prPlanFixture(), nil, nil)
	out, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--output-format", "json")
	var plan struct {
		PullRequest app.PRPlan `json:"pullRequest"`
	}
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || plan.PullRequest.Branch != "pacto/declare-invoices-0.1.0" || strings.Contains(out, "ghp_secret") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDeclarePRResultJSON(t *testing.T) {
	noKube(t)
	prEnv(t, "ghp_secret")
	var got app.DeclareRequest
	gh := &gitHub{t: t}
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := prPlanFixture()
		if err := req.OnPlan(p); err != nil {
			return nil, err
		}
		return &app.DeclareResult{Plan: *p, Executed: true, Signed: true, Tag: "0.1.0", Published: "ghcr.io/acme/invoices@sha256:aa",
			Files: map[string][]byte{"pacto.yaml": []byte("a"), "pacto.inferred.yaml": []byte("b")}}, nil
	}
	d := declareDeps(t, &got, nil, nil, run)
	d.gitTransport = gh
	out, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--yes", "--output-format", "json")
	var res struct {
		PullRequest app.PullRequestResult `json:"pullRequest"`
	}
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.PullRequest.URL != "https://github.com/acme/invoices/pull/7" ||
		res.PullRequest.Login != "ana" || strings.Contains(out, `"files"`) || strings.Contains(out, "ghp_secret") {
		t.Fatalf("%v\n%s", err, out)
	}
	// A failed pull request after the write is in the document, with the exit.
	gh.fail = "POST /pulls"
	out, _, err = runRoot(t, d, nil, "declare", "invoices", "--pr", "--yes", "--output-format", "json")
	var failed struct {
		Published        string            `json:"published"`
		PullRequestError *app.DeclareError `json:"pullRequestError"`
	}
	if err == nil || json.Unmarshal([]byte(out), &failed) != nil || failed.Published == "" || failed.PullRequestError == nil ||
		failed.PullRequestError.Code != "PullRequestFailed" {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestPRDraftBody(t *testing.T) {
	noKube(t)
	prEnv(t, "")
	gh := &gitHub{t: t}
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := prPlanFixture()
		p.State, p.Signer = contract.DeclarationDraft, app.SignerView{Class: "agent", Source: "none"}
		if err := req.OnPlan(p); err != nil {
			return nil, err
		}
		return &app.DeclareResult{Plan: *p, Executed: true, Draft: true, Tag: "draft-2", Published: "ghcr.io/acme/invoices@sha256:dd",
			Files: map[string][]byte{"pacto.yaml": []byte("a")}}, nil
	}
	var got app.DeclareRequest
	d := declareDeps(t, &got, nil, nil, run)
	d.gitTransport = gh
	d.gitCredentialFill = func(context.Context, string) (string, error) { return "password=hp\n", nil }
	out, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--yes")
	if err != nil || !strings.Contains(out, "opened https://github.com/acme/invoices/pull/7 as ana (your git credential helper)\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	body := gh.pulls[0]["body"].(string)
	if gh.pulls[0]["head"] != "pacto/declare-invoices-draft-2" || !strings.Contains(body, "- State: draft\n") || !strings.Contains(body, "unsigned draft, proposer asserted") {
		t.Fatalf("%+v", gh.pulls[0])
	}
}

func TestPRUnchangedNeedsAToken(t *testing.T) {
	noKube(t)
	prEnv(t, "")
	run := func(app.DeclareRequest) (*app.DeclareResult, error) {
		p := prPlanFixture()
		p.Unchanged = "0.1.0"
		p.Baseline = &app.DeclareBaseline{InferredBaseline: contract.InferredBaseline{Version: "0.1.0", Bundle: "ghcr.io/acme/invoices@sha256:aa"}}
		return &app.DeclareResult{Plan: *p}, nil
	}
	var got app.DeclareRequest
	d := declareDeps(t, &got, nil, nil, run)
	d.gitTransport, d.gitCredentialFill = noHost{t}, noHelper
	if _, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--yes"); err == nil || !strings.Contains(err.Error(), "no Git token") {
		t.Fatal(err)
	}
}

// pulledFiles is pacto.yaml and pacto.inferred.yaml of invoices at tag.
func (e *declareE2E) pulledFiles(t *testing.T, tag string) map[string]string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "invoices")
	if out, err := e.run(t, "pull", "oci://"+e.host+"/acme/invoices:"+tag, "--trusted-root", e.root, "--output", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out := map[string]string{}
	for _, name := range []string{"pacto.yaml", "pacto.inferred.yaml"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		out["deploy/"+name] = string(raw)
	}
	return out
}

func wantCommitted(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for p, w := range want {
		if got[p] != w {
			t.Fatalf("%s committed\n%s\nwant\n%s", p, got[p], w)
		}
	}
}

func TestPRAfterPublishCommitsBundleBytes(t *testing.T) {
	e := newDeclareE2E(t)
	prEnv(t, "ghp_secret")
	gh := &gitHub{t: t}
	e.git = gh
	out := e.declare(t, "--namespace", "billing", "--accept", "ledger", "--pr", "--repository", "https://github.com/acme/invoices", "--folder", "deploy")
	if !strings.Contains(out, "published "+e.host+"/acme/invoices@sha256:") || !strings.Contains(out, "opened https://github.com/acme/invoices/pull/7 as ana (your GITHUB_TOKEN)") ||
		strings.Contains(out, "ghp_secret") {
		t.Fatal(out)
	}
	// Published first: the user and repository reads may come before, no write does.
	if i := strings.Index(strings.Join(gh.calls, ","), "POST"); i < 0 || len(gh.trees) != 1 {
		t.Fatalf("%v", gh.calls)
	}
	wantCommitted(t, gh.committed(0), e.pulledFiles(t, "0.1.0"))
}

func TestPRUnchangedExportsBaselineBytes(t *testing.T) {
	e := newDeclareE2E(t)
	prEnv(t, "ghp_secret")
	gh := &gitHub{t: t}
	e.git = gh
	// The installation lets acme.com people sign, so ana's 0.1.0 counts as the baseline of the rerun.
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": delivery.PolicyConfigMap, "namespace": delivery.PolicyNamespace},
		"data":     map[string]any{delivery.PolicyKey: `{"signers":[{"issuer":"` + e2eIssuer + `","subjects":".*@acme\\.com"}]}`}}}
	if _, err := e.dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace(delivery.PolicyNamespace).
		Create(t.Context(), cm, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	args := []string{"--namespace", "billing", "--pr", "--repository", "https://github.com/acme/invoices", "--folder", "deploy"}
	e.declare(t, append(args, "--accept", "ledger")...)
	out := e.declare(t, args...)
	if !strings.Contains(out, "unchanged since 0.1.0") || !strings.Contains(out, "pull request: github https://github.com/acme/invoices") || len(gh.trees) != 2 {
		t.Fatalf("%d trees\n%s", len(gh.trees), out)
	}
	wantCommitted(t, gh.committed(1), e.pulledFiles(t, "0.1.0"))
	// The baseline's own state and the identity it counted by.
	if body := gh.pulls[1]["body"].(string); !strings.Contains(body, "`0.1.0`") || !strings.Contains(body, "`"+e.host+"/acme/invoices@sha256:") ||
		!strings.Contains(body, "- State: declared\n") || !strings.Contains(body, "- Signed by: `ana@acme.com (") {
		t.Fatal(body)
	}
}

func TestPRFailureKeepsPublished(t *testing.T) {
	e := newDeclareE2E(t)
	prEnv(t, "ghp_secret")
	e.git = &gitHub{t: t, fail: "POST /git/trees"}
	out, err := e.run(t, "declare", "invoices", "--registry", e.host+"/acme", "--fulcio-url", e.ss.Fulcio, "--timestamp-url", e.ss.TSA, "--rekor-url", "",
		"--trusted-root", e.root, "--yes", "--namespace", "billing", "--pr", "--repository", "https://github.com/acme/invoices")
	if err == nil || !strings.HasPrefix(err.Error(), "published "+e.host+"/acme/invoices@sha256:") ||
		!strings.Contains(err.Error(), "; the pull request failed: the host answered POST /repos/acme/invoices/git/trees with 403: Resource not accessible by personal access token; rerun with --pr --yes to retry") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "published "+e.host) {
		t.Fatal(out)
	}
	e.pulledFiles(t, "0.1.0")
}

// TestPRUnchangedBaselineWithoutDeclaration: a baseline no declaration
// published is named published in the pull request, never declared.
func TestPRUnchangedBaselineWithoutDeclaration(t *testing.T) {
	noKube(t)
	prEnv(t, "ghp_secret")
	run := func(app.DeclareRequest) (*app.DeclareResult, error) {
		p := prPlanFixture()
		p.Unchanged = "0.1.0"
		p.Baseline = &app.DeclareBaseline{InferredBaseline: contract.InferredBaseline{Version: "0.1.0", Bundle: "ghcr.io/acme/invoices@sha256:aa"},
			Signer: "https://github.com/acme/invoices/.github/workflows/release.yml@refs/heads/main (https://token.actions.githubusercontent.com)"}
		return &app.DeclareResult{Plan: *p, Files: map[string][]byte{"pacto.yaml": []byte("x")}}, nil
	}
	var got app.DeclareRequest
	d := declareDeps(t, &got, nil, nil, run)
	gh := &gitHub{t: t}
	d.gitTransport, d.gitCredentialFill = gh, noHelper
	if out, _, err := runRoot(t, d, nil, "declare", "invoices", "--pr", "--yes"); err != nil || len(gh.pulls) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	if body := gh.pulls[0]["body"].(string); !strings.Contains(body, "- State: published\n") || !strings.Contains(body, "release.yml@refs/heads/main") {
		t.Fatal(body)
	}
}
