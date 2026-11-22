package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// trapKube points KUBECONFIG at a cluster that fails the test on any
// request: a plan that read the cluster with the server's own identity
// for the repository would reach it.
func trapKube(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the server's own kubeconfig was read: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	cfg := filepath.Join(t.TempDir(), "kubeconfig")
	mustWriteFile(t, cfg, "apiVersion: v1\nkind: Config\ncurrent-context: own\nclusters:\n- name: own\n  cluster:\n    server: "+srv.URL+
		"\ncontexts:\n- name: own\n  context:\n    cluster: own\n    user: own\nusers:\n- name: own\n  user: {}\n")
	t.Setenv("KUBECONFIG", cfg)
}

// invoicesView is a fleet view showing billing/invoices as an inferred
// workload whose GitOps source names repos; twin adds another invoices.
func invoicesView(twin bool, repos ...fleet.InferredRepository) *pactomcp.FleetMode {
	w := &fleet.InferredWorkload{Namespace: "billing", Kind: "Deployment", Name: "invoices", Repositories: repos,
		Image: &fleet.InferredValue{Value: "reg.io/acme/invoices:1"}}
	snap := &fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{
		"k8s:billing/Deployment/invoices": {Declaration: fleet.DeclarationInferred, Workload: w},
		"declared":                        {Workload: &fleet.InferredWorkload{Namespace: "billing", Kind: "Deployment", Name: "invoices"}},
	}}
	if twin {
		snap.Services["k8s:shop/Deployment/invoices"] = &fleet.ServiceRecord{Declaration: fleet.DeclarationInferred,
			Workload: &fleet.InferredWorkload{Namespace: "shop", Kind: "Deployment", Name: "invoices"}}
	}
	return &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(snap), nil },
		Opts: pactomcp.FleetOptions{View: func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) { return q, nil }}}
}

func TestHTTPMCPPlanNeverCallsNewK8s(t *testing.T) {
	w := newDeclareWorld(t)
	trapKube(t)
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"}, nil)
	rd.fleet = invoicesView(false, fleet.InferredRepository{URL: "https://github.com/acme/invoices", Path: "deploy"})
	raw, refused, err := rd.Plan(remoteCtx(declareCluster()), pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme"})
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	want := &contract.InferredSourceRepository{URL: "https://github.com/acme/invoices", Rung: contract.RepositoryRungGitOps, Folder: "deploy", FolderRung: contract.RepositoryRungGitOps}
	if err != nil || refused != nil || !reflect.DeepEqual(p.Repository, want) {
		t.Fatalf("%v %+v %s", err, refused, raw)
	}
}

func TestRemoteDeclarerObserved(t *testing.T) {
	ctx := context.Background()
	repo := fleet.InferredRepository{URL: "https://github.com/acme/invoices"}
	for name, c := range map[string]struct {
		mode *pactomcp.FleetMode
		req  app.DeclareRequest
		want string
	}{
		"no fleet":          {nil, app.DeclareRequest{Service: "invoices"}, ""},
		"name":              {invoicesView(false, repo), app.DeclareRequest{Service: "invoices"}, "invoices"},
		"ns/name":           {invoicesView(true, repo), app.DeclareRequest{Service: "billing/invoices"}, "invoices"},
		"ns/Kind/name":      {invoicesView(true, repo), app.DeclareRequest{Service: "billing/Deployment/invoices"}, "invoices"},
		"another kind":      {invoicesView(false, repo), app.DeclareRequest{Service: "billing/StatefulSet/invoices"}, ""},
		"two match":         {invoicesView(true, repo), app.DeclareRequest{Service: "invoices"}, ""},
		"namespace flag":    {invoicesView(true, repo), app.DeclareRequest{Service: "invoices", Namespace: "billing"}, "invoices"},
		"another namespace": {invoicesView(false, repo), app.DeclareRequest{Service: "invoices", Namespace: "shop"}, ""},
	} {
		o := remoteDeclarer{fleet: c.mode}.observed(ctx, c.req)
		if o == nil || o.Name != c.want || (c.want != "" && (o.Image != "reg.io/acme/invoices:1" || len(o.Repositories) != 1)) {
			t.Errorf("%s: %+v", name, o)
		}
	}
	// A workload with no image hands in none.
	mode := invoicesView(false)
	q, _ := mode.Src(ctx)
	q.SnapshotForSerialization().Services["k8s:billing/Deployment/invoices"].Workload.Image = nil
	if o := (remoteDeclarer{fleet: mode}).observed(ctx, app.DeclareRequest{Service: "invoices"}); o.Name != "invoices" || o.Image != "" {
		t.Errorf("%+v", o)
	}
}

func TestDashboardPlanNeverCallsNewK8s(t *testing.T) {
	h := newDeclareHost(t, true, true)
	trapKube(t)
	w := wl
	w.Repositories, w.Image = []fleet.InferredRepository{{URL: "git@github.com:acme/invoices.git"}}, "reg.io/acme/invoices:1"
	ans, err := h.d.Plan(h.req(), w, dashboard.DeclareInput{})
	if err != nil || ans.Plan == nil || ans.Plan.Repository == nil || ans.Plan.Repository.URL != "https://github.com/acme/invoices" ||
		!strings.Contains(ans.Plan.Record.Text, "rung: gitops") {
		t.Fatalf("%v %+v", err, ans)
	}
	// What the view hides never answers; why reaches the panel.
	ans, _ = h.d.Plan(h.req(), wl, dashboard.DeclareInput{})
	if ans.Plan.Repository != nil || len(ans.Plan.RepositoryTried) != 4 || ans.Plan.RepositoryTried[1].Reason != "no GitOps source lists this workload" {
		t.Fatalf("%+v", ans.Plan)
	}
}

func TestDeclarePlanPrintsRepositoryLine(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	p := declarePlanFixture()
	p.Repository = &contract.InferredSourceRepository{URL: "https://github.com/acme/invoices", Rung: contract.RepositoryRungSigned,
		Signer: "https://github.com/acme/invoices/.github/workflows/ci.yml@refs/heads/main", Folder: "deploy", FolderRung: contract.RepositoryRungGitOps}
	out, _, err := runRoot(t, declareDeps(t, &got, p, nil, nil), nil, "declare", "invoices")
	want := "inferredFrom k8s:billing/Deployment/invoices (cluster 3f1c)\n" +
		"repository: github.com/acme/invoices (signed provenance, workflow ci.yml@refs/heads/main), folder: deploy/ (GitOps source)\n"
	if err != nil || !strings.Contains(out, want) || strings.Contains(out, "tried") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDeclarePlanPrintsTriedRungs(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	p := declarePlanFixture()
	p.RepositoryTried = []app.RungNote{{Rung: "signed-provenance", Reason: "2 signed repositories"}, {Rung: "gitops", Reason: "no GitOps source lists this workload"}}
	out, _, err := runRoot(t, declareDeps(t, &got, p, nil, nil), nil, "declare", "invoices")
	want := "repository: unknown\n  tried signed-provenance: 2 signed repositories\n  tried gitops: no GitOps source lists this workload\n"
	if err != nil || !strings.Contains(out, want) {
		t.Fatalf("%v\n%s", err, out)
	}
	// A plan that tried nothing (an accepted draft's) prints no repository line.
	p.RepositoryTried = nil
	if out, _, _ = runRoot(t, declareDeps(t, &got, p, nil, nil), nil, "declare", "invoices"); strings.Contains(out, "repository:") {
		t.Fatalf("%s", out)
	}
}

func TestDeclareFlagsPR(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	_, _, err := runRoot(t, declareDeps(t, &got, declarePlanFixture(), nil, nil), nil, "declare", "invoices",
		"--pr", "--repository", "https://github.com/acme/invoices", "--folder", "deploy", "--repo-map", "repos.yaml")
	if err != nil || !got.PR || got.Repository != "https://github.com/acme/invoices" || got.Folder != "deploy" || got.RepoMap != "repos.yaml" {
		t.Fatalf("%v %+v", err, got)
	}
	cmd := newDeclareCommand(app.NewService(nil, nil), nil, defaultDeps())
	for _, f := range []string{"pr", "repository", "folder", "repo-map"} {
		if !strings.Contains(cmd.Flags().FlagUsages(), "--"+f) {
			t.Errorf("--help lists no --%s", f)
		}
	}
}
