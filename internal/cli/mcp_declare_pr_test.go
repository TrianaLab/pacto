package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// argoKube points KUBECONFIG at an apiserver whose billing/invoices
// Deployment is tracked by the Argo CD Application invoices in argocd, whose
// source is github.com/acme/invoices at deploy: the stdio repository read's
// GitOps rung answers.
func argoKube(t *testing.T) {
	t.Helper()
	argoKubeAt(t, "reg.io/acme/invoices:1.0.0", "https://github.com/acme/invoices.git")
}

// argoKubeAt is argoKube whose Deployment runs image and whose Application's
// source is repoURL at deploy.
func argoKubeAt(t *testing.T, image, repoURL string) {
	t.Helper()
	list := func(items ...string) string {
		return `{"kind":"List","apiVersion":"v1","metadata":{},"items":[` + strings.Join(items, ",") + `]}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_, _ = w.Write([]byte(`{"kind":"APIVersions","versions":["v1"]}`))
		case "/apis":
			_, _ = w.Write([]byte(`{"kind":"APIGroupList","groups":[{"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],` +
				`"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}},{"name":"argoproj.io","versions":[{"groupVersion":"argoproj.io/v1alpha1",` +
				`"version":"v1alpha1"}],"preferredVersion":{"groupVersion":"argoproj.io/v1alpha1","version":"v1alpha1"}}]}`))
		case "/apis/apps/v1/namespaces/billing/deployments":
			_, _ = w.Write([]byte(list(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"invoices","namespace":"billing",` +
				`"annotations":{"argocd.argoproj.io/tracking-id":"invoices:apps/Deployment:billing/invoices"}},` +
				`"spec":{"template":{"spec":{"containers":[{"name":"invoices","image":"` + image + `"}]}}}}`)))
		case "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications":
			_, _ = w.Write([]byte(list(`{"apiVersion":"argoproj.io/v1alpha1","kind":"Application","metadata":{"name":"invoices","namespace":"argocd"},` +
				`"spec":{"source":{"repoURL":"` + repoURL + `","path":"deploy"}},` +
				`"status":{"resources":[{"group":"apps","kind":"Deployment","namespace":"billing","name":"invoices"}]}}`)))
		default:
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "s") && strings.Contains(r.URL.Path, "/namespaces/") {
				_, _ = w.Write([]byte(list()))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`))
		}
	}))
	t.Cleanup(srv.Close)
	cfg := filepath.Join(t.TempDir(), "kubeconfig")
	mustWriteFile(t, cfg, "apiVersion: v1\nkind: Config\ncurrent-context: own\nclusters:\n- name: own\n  cluster:\n    server: "+srv.URL+
		"\ncontexts:\n- name: own\n  context:\n    cluster: own\n    user: own\nusers:\n- name: own\n  user: {}\n")
	t.Setenv("KUBECONFIG", cfg)
}

// prServer is a stdio pacto_declare server whose pull requests go to gh,
// with no credential helper.
func prServer(gh http.RoundTripper) func() *mcpsdk.Server {
	decl := newMCPDeclarer(app.NewService(nil, nil), "")
	decl.git = deps{gitTransport: gh, gitCredentialFill: noHelper}
	return func() *mcpsdk.Server {
		s, _ := pactomcp.New("v", pactomcp.Authoring{Declare: decl}, pactomcp.Modes{})
		return s
	}
}

func TestMCPDeclarePROverStdioOpens(t *testing.T) {
	w := newDeclareWorld(t)
	argoKube(t)
	prEnv(t, "ghp_secret")
	gh := &gitHub{t: t}
	server := prServer(gh)
	raw := wantPRPlan(t, server, w)
	if len(gh.calls) != 0 {
		t.Fatalf("the plan reached the Git host: %v", gh.calls)
	}
	out := declareTwice(t, server, w.args(map[string]any{"pr": true}))
	if out.Draft == nil || out.Draft.Tag != "draft-1" || out.PullRequest == nil || out.PullRequest.URL != "https://github.com/acme/invoices/pull/7" ||
		out.PullRequest.Login != "ana" || out.PullRequest.Branch != "pacto/declare-invoices-draft-1" || out.PullRequestError != nil {
		t.Fatalf("%+v", out)
	}
	body := gh.pulls[0]["body"].(string)
	if !strings.Contains(body, "- State: draft\n") || !strings.Contains(body, "`ana`") || strings.Contains(raw+body, "ghp_secret") {
		t.Fatalf("%s", body)
	}
	files := gh.committed(0)
	if !strings.Contains(files["deploy/"+contract.InferredRecordFile], "rung: gitops") || files["deploy/pacto.yaml"] == "" {
		t.Fatalf("%v", files)
	}
}

// wantPRPlan plans with pr on server: the GitOps answer and the pull
// request the run would open. It returns the plan's JSON.
func wantPRPlan(t *testing.T, server func() *mcpsdk.Server, w *declareWorld) string {
	t.Helper()
	plan, res := callTool(t, server(), "pacto_declare", w.args(map[string]any{"pr": true}))
	var p struct {
		Repository  *contract.InferredSourceRepository `json:"repository"`
		PullRequest *app.PRPlan                        `json:"pullRequest"`
	}
	raw, _ := json.Marshal(plan.Plan)
	_ = json.Unmarshal(raw, &p)
	if res.IsError || p.Repository == nil || p.Repository.Rung != contract.RepositoryRungGitOps || p.PullRequest == nil ||
		p.PullRequest.Branch != "pacto/declare-invoices-<next draft tag>" || p.PullRequest.TokenSource != "your GITHUB_TOKEN" {
		t.Fatalf("%s", raw)
	}
	return string(raw)
}

// A pull request that fails after the draft was written keeps the draft
// and says how to retry: accept the draft, naming the repository.
func TestMCPDeclarePRFailureKeepsDraft(t *testing.T) {
	w := newDeclareWorld(t)
	argoKube(t)
	prEnv(t, "ghp_secret")
	out, res := declarePRTwice(t, prServer(&gitHub{t: t, fail: "POST /pulls"}), w.args(map[string]any{"pr": true}))
	if !res.IsError || out.Draft == nil || out.Draft.Tag != "draft-1" || out.Refusal != nil || out.PullRequestError == nil ||
		out.PullRequestError.Code != pactomcp.RefusalPullRequestFailed || !strings.HasSuffix(out.PullRequestError.CLI, " --from-draft draft-1 --pr --repository https://github.com/acme/invoices --yes") {
		t.Fatalf("%+v", out)
	}
	w.pulled(t, "draft-1")
}

// declarePRTwice is declareTwice that keeps an error result.
func declarePRTwice(t *testing.T, server func() *mcpsdk.Server, args map[string]any) (pactomcp.WriteResult, *mcpsdk.CallToolResult) {
	t.Helper()
	plan, _ := callTool(t, server(), "pacto_declare", args)
	args["dry_run"], args["intent_digest"], args["on_behalf_of"] = false, plan.IntentDigest, "ana"
	return callTool(t, server(), "pacto_declare", args)
}

// The stdio process holds no Git token: the plan refuses before any write.
func TestMCPDeclarePRWithoutToken(t *testing.T) {
	w := newDeclareWorld(t)
	argoKube(t)
	prEnv(t, "")
	out, res := callTool(t, prServer(noHost{t})(), "pacto_declare", w.args(map[string]any{"pr": true}))
	if !res.IsError || out.Refusal == nil || out.Refusal.Code != pactomcp.RefusalGitTokenMissing || !strings.HasSuffix(out.Refusal.CLI, " --pr") {
		t.Fatalf("%+v", out)
	}
	// A run straight to Execute (pacto_declare always plans first) refuses
	// in the plan it carries out, before the write.
	decl := newMCPDeclarer(app.NewService(nil, nil), "")
	decl.git = deps{gitTransport: noHost{t}, gitCredentialFill: noHelper}
	_, refused, err := decl.Execute(t.Context(), pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme", PR: true, OnBehalfOf: "ana"})
	if err != nil || refused == nil || refused.Code != pactomcp.RefusalGitTokenMissing {
		t.Fatalf("%+v %v", refused, err)
	}
	if tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Tags(t.Context(), w.host+"/acme/invoices"); len(tags) != 0 {
		t.Fatalf("wrote %v", tags)
	}
}

func TestMCPDeclareAgentRepositoryNeedsPerson(t *testing.T) {
	w := newDeclareWorld(t)
	prEnv(t, "ghp_secret")
	out, res := declarePRTwice(t, prServer(noHost{t}), w.args(map[string]any{"pr": true, "repository": "https://github.com/acme/elsewhere"}))
	if !res.IsError || out.Executed || out.Refusal == nil || out.Refusal.Code != pactomcp.RefusalRepositoryUnconfirmed ||
		!strings.Contains(out.Refusal.CLI, "--pr --repository https://github.com/acme/elsewhere") {
		t.Fatalf("%+v", out)
	}
	if tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Tags(t.Context(), w.host+"/acme/invoices"); len(tags) != 0 {
		t.Fatalf("wrote %v", tags)
	}
}

func TestMCPDeclarePlanCarriesRepository(t *testing.T) {
	w := newDeclareWorld(t)
	argoKube(t)
	plan, res := callTool(t, stdioDeclareServer(t), "pacto_declare", w.args(map[string]any{"folder": "svc/invoices"}))
	raw, _ := json.Marshal(plan.Plan)
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	want := contract.InferredSourceRepository{URL: "https://github.com/acme/invoices", Rung: contract.RepositoryRungGitOps, Folder: "svc/invoices",
		FolderRung: contract.RepositoryRungGiven}
	if res.IsError || p.Repository == nil || *p.Repository != want || strings.Contains(string(raw), "pullRequest") {
		t.Fatalf("%s", raw)
	}
}

func TestMCPDeclarePROverHTTPRefused(t *testing.T) {
	trapKube(t)
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://reg.io/acme"}, nil)
	a := pactomcp.DeclareArgs{Service: "invoices", Registry: "reg.io/acme", PR: true}
	raw, refused, err := rd.Plan(remoteCtx(declareCluster()), a)
	if err != nil || raw != nil || refused == nil || refused.Code != pactomcp.RefusalPullRequestNeedsLocalToken ||
		refused.Message != "--pr runs only where your Git token is: run the command on your machine, or pacto_declare over stdio" ||
		refused.CLI != "pacto declare invoices --registry reg.io/acme --pr" {
		t.Fatalf("%+v %v", refused, err)
	}
}

func TestMCPDeclareRepoMapStdioOnly(t *testing.T) {
	trapKube(t)
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://reg.io/acme"}, nil)
	_, refused, _ := rd.Plan(remoteCtx(declareCluster()), pactomcp.DeclareArgs{Service: "invoices", Registry: "reg.io/acme", RepoMap: "/etc/repos.yaml"})
	if refused == nil || refused.Code != pactomcp.RefusalExecuteNeedsStdio || !strings.Contains(refused.Message, "repo_map") {
		t.Fatalf("%+v", refused)
	}
	req, err := newMCPDeclarer(nil, "").request(pactomcp.DeclareArgs{Service: "invoices", PR: true, Repository: "r", Folder: "f", RepoMap: "m.yaml"})
	if err != nil || !req.PR || req.Repository != "r" || req.Folder != "f" || req.RepoMap != "m.yaml" || !req.Agent {
		t.Fatalf("%v %+v", err, req)
	}
}
