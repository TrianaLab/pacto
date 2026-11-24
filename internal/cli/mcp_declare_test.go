package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// kubeObject is an object of kind in ns, with fields merged into it.
func kubeObject(apiVersion, kind, ns, name string, fields map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": name, "namespace": ns}}
	for k, v := range fields {
		if m, ok := v.(map[string]any); ok && k == "metadata" {
			for mk, mv := range m {
				o["metadata"].(map[string]any)[mk] = mv
			}
			continue
		}
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

// declareCluster is a cluster where invoices (namespace billing) calls
// ledger, which a Pacto describes at 2.3.1, so the cluster read proposes a
// range for it.
func declareCluster() *dynamicfake.FakeDynamicClient {
	gvr := func(g, v, r string) schema.GroupVersionResource {
		return schema.GroupVersionResource{Group: g, Version: v, Resource: r}
	}
	lists := map[schema.GroupVersionResource]string{
		gvr("apps", "v1", "deployments"): "DeploymentList", gvr("apps", "v1", "statefulsets"): "StatefulSetList", gvr("apps", "v1", "daemonsets"): "DaemonSetList",
		gvr("batch", "v1", "cronjobs"): "CronJobList", gvr("", "v1", "services"): "ServiceList", gvr("", "v1", "pods"): "PodList",
		gvr("apps", "v1", "replicasets"): "ReplicaSetList", gvr("batch", "v1", "jobs"): "JobList", gvr("discovery.k8s.io", "v1", "endpointslices"): "EndpointSliceList",
		gvr("pacto.trianalab.io", "v1alpha1", "pactos"): "PactoList", gvr("pacto.trianalab.io", "v1alpha1", "noderuntimeobservations"): "NodeRuntimeObservationList",
		gvr("", "v1", "configmaps"): "ConfigMapList",
	}
	now := time.Now().UTC().Format(time.RFC3339)
	owner := func(kind, name string) map[string]any {
		return map[string]any{"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": kind, "name": name, "uid": "x", "controller": true}}}
	}
	podMeta := owner("ReplicaSet", "invoices-7f")
	podMeta["uid"], podMeta["labels"] = "u-invoices", map[string]any{"app": "invoices"}
	svc := func(name, ip string) *unstructured.Unstructured {
		return kubeObject("v1", "Service", "billing", name, map[string]any{"spec": map[string]any{"clusterIP": ip, "clusterIPs": []any{ip},
			"selector": map[string]any{"app": name}, "ports": []any{map[string]any{"port": int64(80), "targetPort": int64(8080)}}}})
	}
	objs := []runtime.Object{
		kubeObject("apps/v1", "Deployment", "billing", "invoices", map[string]any{"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "invoices"}}, "template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "invoices"}},
				"spec": map[string]any{"containers": []any{map[string]any{"name": "invoices", "image": "reg.io/acme/invoices:1.0.0",
					"ports": []any{map[string]any{"containerPort": int64(8080)}}}}}}}}),
		kubeObject("apps/v1", "ReplicaSet", "billing", "invoices-7f", map[string]any{"metadata": owner("Deployment", "invoices")}),
		kubeObject("v1", "Pod", "billing", "invoices-7f-a", map[string]any{"metadata": podMeta}),
		svc("invoices", "10.0.0.30"), svc("ledger", "10.0.0.60"),
		kubeObject("pacto.trianalab.io/v1alpha1", "Pacto", "billing", "ledger", map[string]any{
			"metadata": map[string]any{"generation": int64(1)},
			"spec":     map[string]any{"contractRef": map[string]any{"oci": "oci://reg.io/acme/contracts/ledger:2.3.1"}, "target": map[string]any{"serviceName": "ledger"}},
			"status": map[string]any{"observedGeneration": int64(1), "contract": map[string]any{"serviceName": "ledger", "version": "2.3.1",
				"resolvedRef": "oci://reg.io/acme/contracts/ledger@sha256:aa"}}}),
		kubeObject("v1", "Namespace", "", "kube-system", map[string]any{"metadata": map[string]any{"uid": "3f1c-uid"}}),
		kubeObject("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1", "pods": []any{map[string]any{"uid": "u-invoices", "connections": []any{
				map[string]any{"address": "10.0.0.60", "port": int64(80), "successes": int64(2), "lastSuccessAt": now}}}}}}),
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
}

// declareWorld is a registry, a cluster the kubeconfig reaches and no
// ambient token.
type declareWorld struct {
	host string
	dyn  *dynamicfake.FakeDynamicClient
	// kubeconfig counts the kubeconfig clients built.
	kubeconfig *int
}

func newDeclareWorld(t *testing.T) *declareWorld {
	t.Helper()
	for _, env := range []string{"SIGSTORE_ID_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"} {
		t.Setenv(env, "")
	}
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	w := &declareWorld{host: strings.TrimPrefix(srv.URL, "http://"), dyn: declareCluster(), kubeconfig: new(int)}
	t.Setenv("PACTO_INSECURE_REGISTRIES", w.host)
	// The repository read builds its own client from the kubeconfig: none.
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	old := initDynamic
	t.Cleanup(func() { initDynamic = old })
	initDynamic = func() (dynamic.Interface, error) { *w.kubeconfig++; return w.dyn, nil }
	return w
}

func (w *declareWorld) args(extra map[string]any) map[string]any {
	a := map[string]any{"service": "invoices", "namespace": "billing", "registry": w.host + "/acme", "accept": []string{"ledger"}}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

// pulled is the record of the invoices bundle tagged tag.
func (w *declareWorld) pulled(t *testing.T, tag string) *contract.InferredRecord {
	t.Helper()
	b, err := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Pull(t.Context(), w.host+"/acme/invoices:"+tag)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil || rec.Declaration == nil {
		t.Fatalf("%v %v", err, rec)
	}
	return rec
}

// callTool calls name on server with args and decodes the write result.
func callTool(t *testing.T, server *mcpsdk.Server, name string, args map[string]any) (pactomcp.WriteResult, *mcpsdk.CallToolResult) {
	t.Helper()
	ct, st := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out pactomcp.WriteResult
	_ = json.Unmarshal([]byte(res.Content[0].(*mcpsdk.TextContent).Text), &out)
	return out, res
}

// stdioDeclareServer is `pacto mcp` over stdio, built by the command layer.
func stdioDeclareServer(t *testing.T) *mcpsdk.Server {
	t.Helper()
	d := defaultDeps()
	d.clusterConfig = func() (*rest.Config, error) { return nil, errors.New("no kubeconfig") }
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", d)
	cmd.SetContext(context.Background())
	server, err := buildMCPServer(cmd, svc, "v", nil, d)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// declareTwice plans with args, then writes with the plan's digest.
func declareTwice(t *testing.T, server func() *mcpsdk.Server, args map[string]any) pactomcp.WriteResult {
	t.Helper()
	plan, res := callTool(t, server(), "pacto_declare", args)
	if res.IsError || plan.IntentDigest == "" {
		t.Fatalf("%+v %s", plan, res.Content[0].(*mcpsdk.TextContent).Text)
	}
	args["dry_run"], args["intent_digest"], args["on_behalf_of"] = false, plan.IntentDigest, "ana"
	out, res := callTool(t, server(), "pacto_declare", args)
	if res.IsError {
		t.Fatalf("%s", res.Content[0].(*mcpsdk.TextContent).Text)
	}
	return out
}

func personToken() string {
	return testutil.TokenWith(map[string]string{"sub": "ana@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": "https://accounts.google.com"})
}

func TestMCPDeclareAlwaysDrafts(t *testing.T) {
	w := newDeclareWorld(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken())
	out := declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.args(nil))
	if !out.Executed || out.Draft == nil || out.Draft.Tag != "draft-1" || out.Draft.Signed ||
		out.AcceptCLI != "pacto declare invoices --registry "+w.host+"/acme --from-draft draft-1" {
		t.Fatalf("%+v", out)
	}
	rec := w.pulled(t, "draft-1")
	if d := rec.Declaration; d.State != contract.DeclarationDraft || d.Asker != (contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}) ||
		d.OnBehalfOf != "ana" || len(d.Requested) != 1 {
		t.Fatalf("%s", rec.Marshal())
	}
	if *w.kubeconfig == 0 {
		t.Fatal("stdio did not read the cluster with the kubeconfig")
	}
}

func TestMCPDeclareUnsignedDraftSaysSo(t *testing.T) {
	w := newDeclareWorld(t)
	plan, _ := callTool(t, stdioDeclareServer(t), "pacto_declare", w.args(nil))
	var p app.DeclarePlan
	raw, _ := json.Marshal(plan.Plan)
	if err := json.Unmarshal(raw, &p); err != nil || p.State != contract.DeclarationDraft || p.Signer.Source != "none" ||
		!strings.Contains(p.Signer.Why, "unsigned draft") {
		t.Fatalf("%v %+v", err, p)
	}
}

func TestMCPDeclareCITokenSignsDraft(t *testing.T) {
	w := newDeclareWorld(t)
	ss := testutil.New(t)
	rootPath := filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(rootPath, ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	const wf = "acme/invoices/.github/workflows/release.yml@refs/heads/main"
	t.Setenv("SIGSTORE_ID_TOKEN", testutil.TokenWith(map[string]string{"sub": "https://github.com/" + wf, "iss": "https://token.actions.githubusercontent.com",
		"job_workflow_ref": wf, "repository": "https://github.com/acme/invoices", "ref": "refs/heads/main"}))
	decl := newMCPDeclarer(app.NewService(nil, nil), "", "", "")
	decl.base.Signing.FulcioURL, decl.base.Signing.TimestampURL, decl.base.Signing.RekorURL, decl.base.Signing.TrustedRoot = ss.Fulcio, ss.TSA, "", rootPath
	server := func() *mcpsdk.Server {
		s, _ := pactomcp.New("v", pactomcp.Authoring{Declare: decl}, pactomcp.Modes{})
		return s
	}
	out := declareTwice(t, server, w.args(nil))
	if out.Draft == nil || !out.Draft.Signed || out.Draft.Tag != "draft-1" {
		t.Fatalf("%+v", out)
	}
	if d := w.pulled(t, "draft-1").Declaration; d.Asker.Class != contract.SignerClassCI || d.State != contract.DeclarationDraft {
		t.Fatalf("%+v", d)
	}
}

func TestMCPDeclareRequestErrors(t *testing.T) {
	newDeclareWorld(t)
	decl := newMCPDeclarer(app.NewService(nil, nil), "", "", "")
	ctx := context.Background()
	for _, a := range []pactomcp.DeclareArgs{{Service: "invoices", CallWindow: "a day"}, {Service: "service:pay"}} {
		if _, _, err := decl.Plan(ctx, a); err == nil {
			t.Errorf("%+v planned", a)
		}
		if _, _, err := decl.Execute(ctx, a); err == nil {
			t.Errorf("%+v ran", a)
		}
	}
	// A kubeconfig that fails fails the call.
	initDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	a := pactomcp.DeclareArgs{Service: "invoices", Registry: "r.io/acme", CallWindow: "1h"}
	if _, _, err := decl.Plan(ctx, a); err == nil || !strings.Contains(err.Error(), "no kubeconfig") {
		t.Fatal(err)
	}
	if _, _, err := decl.Execute(ctx, a); err == nil {
		t.Fatal("ran without a cluster")
	}
	// A refusal with no plan carries no plan.
	raw, refused, err := decl.Plan(ctx, pactomcp.DeclareArgs{Service: "invoices"})
	if err != nil || raw != nil || refused == nil || refused.Code != pactomcp.RefusalNoRegistry || refused.CLI == "" {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	if _, _, err := declareAnswer[app.DeclarePlan](nil, errors.New("boom")); err == nil {
		t.Fatal("an error that is no refusal was swallowed")
	}
}

// remoteCtx is a request reviewed as alice, whose own client is dyn.
func remoteCtx(dyn dynamic.Interface) context.Context {
	return context.WithValue(context.Background(), reviewedKey{}, reviewed{person: authn.Person{User: "alice"}, own: &app.Delivery{Dynamic: dyn}})
}

func TestMCPDeclareHTTPReadsWithCallerToken(t *testing.T) {
	w := newDeclareWorld(t)
	caller := declareCluster()
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme/ledger:2.3.1"}, nil)
	raw, refused, err := rd.Plan(remoteCtx(caller), pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme"})
	if err != nil || refused != nil || !strings.Contains(string(raw), "intentDigest") || !strings.Contains(string(raw), "this HTTP server writes nothing") {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	if *w.kubeconfig != 0 || len(caller.Actions()) == 0 || len(w.dyn.Actions()) != 0 {
		t.Fatalf("kubeconfig clients %d, caller actions %d, server actions %d", *w.kubeconfig, len(caller.Actions()), len(w.dyn.Actions()))
	}
	// A request nobody reviewed reads nothing.
	if _, _, err := rd.Plan(context.Background(), pactomcp.DeclareArgs{Service: "invoices", Registry: w.host + "/acme"}); !errors.Is(err, errNobodyReviewed) {
		t.Fatal(err)
	}
	if _, _, err := rd.Plan(remoteCtx(caller), pactomcp.DeclareArgs{Service: "invoices", Registry: w.host + "/acme", CallWindow: "x"}); err == nil {
		t.Fatal("a bad call_window planned")
	}
}

func TestMCPDeclareHTTPRefusesDir(t *testing.T) {
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/ledger"}, nil)
	_, refused, err := rd.Plan(remoteCtx(nil), pactomcp.DeclareArgs{Service: "invoices", Dir: "/srv", Registry: "ghcr.io/acme"})
	if err != nil || refused == nil || refused.Code != pactomcp.RefusalExecuteNeedsStdio || !strings.Contains(refused.CLI, "--dir /srv") {
		t.Fatalf("%+v %v", refused, err)
	}
}

// TestMCPDeclareHTTPRefusesDraftArgument: the HTTP server writes no draft,
// so it refuses draft as it refuses dir, before any read.
func TestMCPDeclareHTTPRefusesDraftArgument(t *testing.T) {
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/ledger"}, nil)
	_, refused, err := rd.Plan(remoteCtx(nil), pactomcp.DeclareArgs{Service: "invoices", Draft: true, Registry: "ghcr.io/acme"})
	if err != nil || refused == nil || refused.Code != pactomcp.RefusalExecuteNeedsStdio || !strings.Contains(refused.Message, "pacto declare --draft") {
		t.Fatalf("%+v %v", refused, err)
	}
}

func TestMCPDeclareStdioPassesDraft(t *testing.T) {
	req, err := newMCPDeclarer(nil, "", "", "").request(pactomcp.DeclareArgs{Service: "invoices", Draft: true})
	if err != nil || !req.Draft || !req.Agent {
		t.Fatalf("%+v %v", req, err)
	}
}

func TestMCPDeclareHTTPRefusesForeignRegistry(t *testing.T) {
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/ledger:1.0.0", "ghcr.io/acme/orders@sha256:aa"}, nil)
	for reg, ok := range map[string]bool{"ghcr.io/acme": true, "ghcr.io/acme/orders": true, "ghcr.io/acme/ledger/sub": true,
		"ghcr.io/evil": false, "ghcr.io/acmefoo": false, "": false,
		// A bare host or a namespace above the one holding a repository is no installation registry.
		"ghcr.io": false} {
		if rd.reads(reg) != ok {
			t.Errorf("reads(%q) = %v", reg, !ok)
		}
	}
	deep := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/team/ledger:1.0.0"}, nil)
	if deep.reads("ghcr.io/acme") || !deep.reads("ghcr.io/acme/team") {
		t.Error("only the namespace directly holding a repository reads")
	}
	_, refused, err := rd.Plan(remoteCtx(nil), pactomcp.DeclareArgs{Service: "invoices", Registry: "ghcr.io/evil"})
	if err != nil || refused == nil || refused.Code != pactomcp.RefusalNoRegistry || !strings.Contains(refused.Message, "ghcr.io/acme/ledger, ghcr.io/acme/orders") {
		t.Fatalf("%+v %v", refused, err)
	}
}

// staticFleetMode is a fleet of one invoices service read from a bundle
// folder, narrowed by view.
func staticFleetMode(t *testing.T, view pactomcp.FleetView, srcErr error) *pactomcp.FleetMode {
	t.Helper()
	root := t.TempDir()
	writeCLIBundle(t, filepath.Join(root, "invoices"), "invoices", "1.0.0")
	snap, err := app.NewService(nil, nil).Fleet(context.Background(), app.FleetOptions{LocalRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	return &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(snap), srcErr }, Opts: pactomcp.FleetOptions{View: view}}
}

func TestMCPDeclareHTTPRestrictsBaseline(t *testing.T) {
	plan := &app.DeclarePlan{Service: "invoices", Version: "1.1.0", Deploy: "r/deploy@sha256:dd", IntentDigest: "sha256:aa",
		Baseline: &app.DeclareBaseline{InferredBaseline: contract.InferredBaseline{Version: "1.0.0", Bundle: "r/invoices@sha256:bb"}},
		Ranges:   []app.DeclaredRange{{Dependency: "ledger", Compatibility: "^2.3.0"}}, Publish: publish.Plan{Version: "1.1.0"}}
	hidden := baselineHidden(plan)
	if hidden.Baseline != nil || hidden.Deploy != "" || len(hidden.Ranges) != 0 || hidden.Publish.Version != "" || hidden.Version != "1.1.0" ||
		hidden.IntentDigest != "sha256:aa" || !strings.Contains(hidden.Notes[0], "hidden") {
		t.Fatalf("%+v", hidden)
	}
	hide := func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) {
		return fleet.NewQuery(&fleet.FleetSnapshot{}), nil
	}
	for name, c := range map[string]struct {
		mode *pactomcp.FleetMode
		want bool
	}{
		"no fleet":   {nil, false},
		"shown":      {staticFleetMode(t, nil, nil), true},
		"hidden":     {staticFleetMode(t, hide, nil), false},
		"unreadable": {staticFleetMode(t, nil, errors.New("down")), false},
		"view refuses": {staticFleetMode(t, func(context.Context, *auth.TokenInfo, *fleet.Query) (*fleet.Query, error) {
			return nil, errors.New("no")
		}, nil), false},
	} {
		if got := (remoteDeclarer{fleet: c.mode}).shows(context.Background(), "invoices"); got != c.want {
			t.Errorf("%s: shows = %v", name, got)
		}
	}
}

// TestMCPDeclareHTTPRestrictsBaselineEndToEnd: a caller the fleet does not
// show invoices to plans its next version with no baseline content.
func TestMCPDeclareHTTPRestrictsBaselineEndToEnd(t *testing.T) {
	w := newDeclareWorld(t)
	ctx := remoteCtx(declareCluster())
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"}, nil)
	a := pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme"}
	// A tag no publisher signed: the plan lists it as skipped and refuses.
	declareFirst(t, w)
	raw, refused, err := rd.Plan(ctx, a)
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	if err != nil || refused == nil || len(p.Skipped) != 0 || !strings.Contains(strings.Join(p.Notes, " "), "hidden") {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	// The refusal names nothing the hidden baseline holds: no tag, signer or repository.
	if refused.Code != pactomcp.RefusalVersionConflict || !strings.Contains(refused.Message, "the published baseline of invoices is hidden") ||
		strings.Contains(refused.Message, "0.1.0") || strings.Contains(refused.Message, "signed by") || strings.Contains(refused.Message, w.host) {
		t.Fatalf("%+v", refused)
	}
	// This server writes no draft: the plan names no draft state or accept command, only the command a person runs.
	if p.State != "" || p.AcceptCLI != "" || !strings.Contains(strings.Join(p.Notes, " "), "with: pacto declare invoices --namespace billing --registry "+w.host+"/acme") {
		t.Fatalf("%s", raw)
	}
	if hiddenRefusal("invoices", nil) != nil {
		t.Fatal("no refusal stays none")
	}
}

// TestMCPDeclareHTTPHidesDraftsEndToEnd: a repository holding only drafts
// is a baseline too, so a caller the fleet does not show invoices to sees
// none of its drafts, askers or onBehalfOf names.
func TestMCPDeclareHTTPHidesDraftsEndToEnd(t *testing.T) {
	w := newDeclareWorld(t)
	declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.args(nil))
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"}, nil)
	raw, _, err := rd.Plan(remoteCtx(declareCluster()), pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme"})
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	if err != nil || len(p.Drafts) != 0 || p.DraftsTotal != 0 || strings.Contains(string(raw), "ana") ||
		!strings.Contains(strings.Join(p.Notes, " "), "the published baseline of invoices is hidden") {
		t.Fatalf("%s %v", raw, err)
	}
}

// declareFirst writes draft-1 and tags its bundle 0.1.0, an unsigned version.
func declareFirst(t *testing.T, w *declareWorld) {
	t.Helper()
	out := declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.args(nil))
	reg := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host)
	_, digest, _ := strings.Cut(out.Draft.Published, "@")
	if err := reg.Tag(t.Context(), w.host+"/acme/invoices", digest, "0.1.0"); err != nil {
		t.Fatal(err)
	}
}

func TestMCPDeclareOverHTTPWithoutRegistryNeverWrites(t *testing.T) {
	w := newDeclareWorld(t)
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"}, nil)
	ctx := remoteCtx(declareCluster())
	a := pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme", Accept: []string{"ledger"}}
	raw, _, _ := rd.Plan(ctx, a)
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	a.IntentDigest = p.IntentDigest
	raw, refused, err := rd.Execute(ctx, a)
	if err != nil || raw != nil || refused == nil || refused.Code != pactomcp.RefusalExecuteNeedsStdio ||
		refused.CLI != "pacto declare invoices --namespace billing --registry "+w.host+"/acme --accept ledger --intent "+p.IntentDigest {
		t.Fatalf("%+v %v", refused, err)
	}
	tags, err := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Tags(t.Context(), w.host+"/acme/invoices")
	if len(tags) != 0 {
		t.Fatalf("the HTTP plan wrote %v (%v)", tags, err)
	}
}

func TestExecuteNeedsStdioCLIReplaysToSameDigest(t *testing.T) {
	w := newDeclareWorld(t)
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"}, nil)
	ctx := remoteCtx(declareCluster())
	a := pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme", Accept: []string{"ledger"}}
	raw, _, _ := rd.Plan(ctx, a)
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	a.IntentDigest = p.IntentDigest
	_, refused, _ := rd.Execute(ctx, a)
	args := strings.Fields(strings.TrimPrefix(refused.CLI, "pacto "))
	out, _, err := runRoot(t, defaultDeps(), nil, append(args, "--output-format", "json")...)
	var person app.DeclarePlan
	if err != nil || json.Unmarshal([]byte(out), &person) != nil || person.IntentDigest != p.IntentDigest || person.State != contract.DeclarationDeclared {
		t.Fatalf("%v\n%s\nagent %s", err, out, p.IntentDigest)
	}
}

// TestDeclareAsAgentAndAsPerson: the same workload declared through
// pacto_declare writes a draft, and pacto declare plans the declared
// version a person's login signs; both plans share one intent digest. (The
// person's signed publish under an injected login is internal/app's
// TestDeclareLoginWritesDeclared: the login is the app service's own seam.)
func TestDeclareAsAgentAndAsPerson(t *testing.T) {
	w := newDeclareWorld(t)
	agent := declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.args(nil))
	var agentPlan app.DeclarePlan
	raw, _ := json.Marshal(agent.Plan)
	_ = json.Unmarshal(raw, &agentPlan)
	if agent.Draft == nil || agentPlan.State != contract.DeclarationDraft {
		t.Fatalf("%+v", agent)
	}
	out, _, err := runRoot(t, defaultDeps(), nil, "declare", "invoices", "--namespace", "billing", "--registry", w.host+"/acme", "--accept", "ledger", "--output-format", "json")
	var person app.DeclarePlan
	if err != nil || json.Unmarshal([]byte(out), &person) != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if person.State != contract.DeclarationDeclared || person.Signer.Source != "login" || person.IntentDigest != agent.IntentDigest ||
		!slices.ContainsFunc(person.Ranges, func(r app.DeclaredRange) bool { return r.Dependency == "ledger" && r.Accepted }) {
		t.Fatalf("person %+v\nagent digest %s", person, agent.IntentDigest)
	}
}

func TestMCPHelpNamesDeclare(t *testing.T) {
	long := newMCPCommand(app.NewService(nil, nil), "v", defaultDeps()).Long
	if !strings.Contains(long, "pacto_schema and pacto_declare") || strings.Contains(long, "the only contract tool is pacto_schema") {
		t.Fatal(long)
	}
}

// TestRemoteMCPOffersDeclare: an HTTP server that reviews each caller
// offers the planning pacto_declare, and refuses a write.
func TestRemoteMCPOffersDeclare(t *testing.T) {
	w := newRemoteFleet(t)
	rec := post(t, w.h, "good", listTools)
	expect(t, strings.Contains(rec.Body.String(), `"pacto_declare"`), "tools: %s", rec.Body)
	rec = post(t, w.h, "good", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_declare","arguments":{"service":"invoices","registry":"ghcr.io/evil"}}}`)
	expect(t, strings.Contains(rec.Body.String(), "NoRegistry"), "foreign registry: %s", rec.Body)
}

// claimArgs are pacto_declare's arguments claiming stripe for team-platform in w.
func (w *declareWorld) claimArgs(extra map[string]any) map[string]any {
	a := map[string]any{"service": "external:stripe", "owner": "team-platform", "registry": w.host + "/acme"}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

// pulledClaim is the record of the bundle tagged tag in repository repo of w.
func (w *declareWorld) pulledClaim(t *testing.T, repo, tag string) *contract.Bundle {
	t.Helper()
	b, err := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Pull(t.Context(), w.host+"/acme/"+repo+":"+tag)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMCPDeclareExternalPlan(t *testing.T) {
	w := newDeclareWorld(t)
	plan, res := callTool(t, stdioDeclareServer(t), "pacto_declare", w.claimArgs(nil))
	var p app.DeclarePlan
	raw, _ := json.Marshal(plan.Plan)
	if err := json.Unmarshal(raw, &p); err != nil || res.IsError || plan.IntentDigest == "" || p.InferredFrom != "external:stripe" ||
		p.BundleRepo != w.host+"/acme/stripe" || p.State != contract.DeclarationDraft || p.Owner != "team:team-platform" ||
		plan.AcceptCLI != "pacto claim stripe --registry "+w.host+"/acme --from-draft <tag>" {
		t.Fatalf("%v %+v %s", err, plan, raw)
	}
}

func TestMCPDeclareExternalWritesDraft(t *testing.T) {
	w := newDeclareWorld(t)
	out := declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.claimArgs(nil))
	if !out.Executed || out.Draft == nil || out.Draft.Tag != "draft-1" || out.Draft.Signed ||
		out.AcceptCLI != "pacto claim stripe --registry "+w.host+"/acme --from-draft draft-1" {
		t.Fatalf("%+v", out)
	}
	b := w.pulledClaim(t, "stripe", "draft-1")
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec.Declaration == nil {
		t.Fatalf("%v %+v", err, rec)
	}
	if d := rec.Declaration; d.State != contract.DeclarationDraft || d.OnBehalfOf != "ana" || d.InferredFrom != "external:stripe" ||
		d.Asker.Class != contract.SignerClassAgent || b.Contract.Service.External != "stripe" || b.Contract.Service.Owner.Team != "team-platform" {
		t.Fatalf("%s", rec.Marshal())
	}
}

// TestMCPDeclareExternalNeverSigned: a person's ambient token never signs
// an agent's claim, which stays an unsigned draft.
func TestMCPDeclareExternalNeverSigned(t *testing.T) {
	w := newDeclareWorld(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken())
	out := declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.claimArgs(nil))
	if out.Draft == nil || out.Draft.Signed || out.Draft.Tag != "draft-1" {
		t.Fatalf("%+v", out)
	}
	tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Tags(t.Context(), w.host+"/acme/stripe")
	if slices.Contains(tags, "0.1.0") {
		t.Fatalf("an agent's claim published a declared version: %v", tags)
	}
}

func TestMCPDeclareExternalRequiresOwner(t *testing.T) {
	w := newDeclareWorld(t)
	a := w.claimArgs(nil)
	delete(a, "owner")
	out, res := callTool(t, stdioDeclareServer(t), "pacto_declare", a)
	if !res.IsError || out.Refusal == nil || out.Refusal.Code != pactomcp.RefusalOwnerRequired ||
		out.Refusal.CLI != "pacto claim stripe --owner <team> --registry "+w.host+"/acme" {
		t.Fatalf("%+v", out)
	}
}

func TestMCPDeclareExternalBypassesWorkloadSubject(t *testing.T) {
	req, err := newMCPDeclarer(nil, "", "", "").request(pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Name: "payments-stripe"})
	if err != nil || req.Service != "external:stripe" || req.Owner != "team-platform" || req.Name != "payments-stripe" || !req.Agent {
		t.Fatalf("%+v %v", req, err)
	}
	if _, err := newMCPDeclarer(nil, "", "", "").request(pactomcp.DeclareArgs{Service: "service:x"}); err == nil || !strings.Contains(err.Error(), "service:") {
		t.Fatal(err)
	}
	// A claim whose kubeconfig fails plans with the signer policy unread.
	initDynamicFails(t)
	if dyn, err := declareDynamic(req); dyn != nil || err != nil {
		t.Fatalf("%v %v", dyn, err)
	}
}

// initDynamicFails makes every kubeconfig client fail.
func initDynamicFails(t *testing.T) {
	old := initDynamic
	t.Cleanup(func() { initDynamic = old })
	initDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
}

func TestMCPDeclareExternalName(t *testing.T) {
	w := newDeclareWorld(t)
	plan, res := callTool(t, stdioDeclareServer(t), "pacto_declare", w.claimArgs(map[string]any{"name": "payments-stripe", "dry_run": false, "on_behalf_of": "ana"}))
	var p app.DeclarePlan
	raw, _ := json.Marshal(plan.Plan)
	_ = json.Unmarshal(raw, &p)
	if !res.IsError || p.BundleRepo != w.host+"/acme/payments-stripe" || p.Service != "payments-stripe" ||
		plan.Refusal.CLI != "pacto claim stripe --owner team-platform --name payments-stripe --registry "+w.host+"/acme" {
		t.Fatalf("%+v %s", plan, raw)
	}
}

// claimFleet is a fleet of services and of external systems, each claimed
// as given (nil: unclaimed), narrowed to nothing hidden.
func claimFleet(t *testing.T, services []string, externals map[fleet.ExternalKey]*fleet.ExternalClaim) *pactomcp.FleetMode {
	t.Helper()
	root := t.TempDir()
	for _, s := range services {
		writeCLIBundle(t, filepath.Join(root, s), s, "1.0.0")
	}
	snap, err := app.NewService(nil, nil).Fleet(context.Background(), app.FleetOptions{LocalRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	snap.Externals = map[fleet.ExternalKey]*fleet.ExternalRecord{}
	for k, c := range externals {
		snap.Externals[k] = &fleet.ExternalRecord{Key: k, Claim: c}
	}
	return &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(snap), nil }}
}

func TestRemoteDeclareExternalOutsideReposRefused(t *testing.T) {
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/ledger"}, claimFleet(t, nil, map[fleet.ExternalKey]*fleet.ExternalClaim{"stripe": nil}))
	_, refused, err := rd.Plan(remoteCtx(nil), pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Registry: "ghcr.io/evil"})
	if err != nil || refused == nil || refused.Code != pactomcp.RefusalNoRegistry || refused.CLI != "pacto claim stripe --owner team-platform --registry ghcr.io/evil" ||
		!strings.Contains(refused.Message, "run pacto claim on your machine") {
		t.Fatalf("%+v %v", refused, err)
	}
	_, refused, _ = rd.Plan(remoteCtx(nil), pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Registry: "ghcr.io/acme", Draft: true})
	if refused == nil || !strings.Contains(refused.Message, "save one with pacto claim --draft") {
		t.Fatalf("%+v", refused)
	}
}

func TestRemoteDeclareExternalHiddenRefused(t *testing.T) {
	a := pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Registry: "ghcr.io/acme"}
	for name, mode := range map[string]*pactomcp.FleetMode{"no fleet": nil, "not listed": claimFleet(t, nil, nil)} {
		rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://ghcr.io/acme/ledger"}, mode)
		for _, run := range []func(context.Context, pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error){rd.Plan, httpWriting(rd).Execute} {
			raw, refused, err := run(remoteCtx(nil), a)
			if err != nil || raw != nil || refused == nil || refused.Code != pactomcp.RefusalNotAllowed || !strings.Contains(refused.Message, "lists no external system stripe") {
				t.Errorf("%s: %s %+v %v", name, raw, refused, err)
			}
		}
	}
}

// httpWriting is rd writing drafts into ghcr.io/acme.
func httpWriting(rd remoteDeclarer) remoteDeclarer {
	rd.registry, rd.push = "ghcr.io/acme", oci.NewKeychain(oci.EnvCredentialOptions())
	return rd
}

// TestRemoteDeclareExternalHiddenBaselineRedacted: a claim whose repository
// holds a service the caller may not read, or a draft of an external system
// their view does not list, is one refusal naming none of it.
func TestRemoteDeclareExternalHiddenBaselineRedacted(t *testing.T) {
	w := newDeclareWorld(t)
	draft := declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.claimArgs(nil)) // stripe/draft-1 on ana's behalf
	check := func(name string, mode *pactomcp.FleetMode, a pactomcp.DeclareArgs, hidden bool) {
		t.Helper()
		checkClaimRedacted(t, w, name, mode, a, hidden)
	}
	stripe := pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Registry: w.host + "/acme"}
	listed := map[fleet.ExternalKey]*fleet.ExternalClaim{"stripe": nil}
	// Drafts of an external system the caller sees are theirs to read.
	check("drafts of a listed external", claimFleet(t, nil, listed), stripe, false)
	// aws/rds/eu-west-1/stripe names the same repository; its draft is of stripe, which this view lists not.
	rds := pactomcp.DeclareArgs{Service: "external:aws/rds/eu-west-1/stripe", Owner: "team-data", Registry: w.host + "/acme"}
	check("other external hidden", claimFleet(t, []string{"stripe"}, map[fleet.ExternalKey]*fleet.ExternalClaim{"aws/rds/eu-west-1/stripe": nil}), rds, true)
	// A published version belongs to the stripe service.
	reg := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host)
	_, digest, _ := strings.Cut(draft.Draft.Published, "@")
	if err := reg.Tag(t.Context(), w.host+"/acme/stripe", digest, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	check("service hidden", claimFleet(t, nil, listed), stripe, true)
	check("service shown", claimFleet(t, []string{"stripe"}, listed), stripe, false)
	// A workload's draft belongs to its service, which this view shows not.
	declareTwice(t, func() *mcpsdk.Server { return stdioDeclareServer(t) }, w.args(nil))
	inv := pactomcp.DeclareArgs{Service: "external:aws/sqs/eu-west-1/invoices", Owner: "team-data", Registry: w.host + "/acme"}
	check("workload draft hidden", claimFleet(t, nil, map[fleet.ExternalKey]*fleet.ExternalClaim{"aws/sqs/eu-west-1/invoices": nil}), inv, true)
}

// checkClaimRedacted plans a over HTTP with the caller's view mode: a
// hidden repository is the one generic refusal, from Plan and Execute alike.
func checkClaimRedacted(t *testing.T, w *declareWorld, name string, mode *pactomcp.FleetMode, a pactomcp.DeclareArgs, hidden bool) {
	t.Helper()
	raw, refused, err := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"}, mode).Plan(remoteCtx(declareCluster()), a)
	if !hidden {
		expect(t, err == nil && raw != nil && (refused == nil || refused.Code != pactomcp.RefusalNotAllowed), "%s: %s %+v %v", name, raw, refused, err)
		return
	}
	if err != nil || raw != nil || refused == nil || refused.Code != pactomcp.RefusalNotAllowed || refused.CLI != a.Command("") ||
		refused.Message != "the repository for this name holds a service you may not read: claim with the CLI and --name" {
		t.Errorf("%s: %s %+v %v", name, raw, refused, err)
	}
	checkClaimExecuteRefused(t, w, name, mode, a)
}

// checkClaimExecuteRefused: Execute never writes a claim over HTTP, so it
// reads nothing to redact and names no repository.
func checkClaimExecuteRefused(t *testing.T, w *declareWorld, name string, mode *pactomcp.FleetMode, a pactomcp.DeclareArgs) {
	t.Helper()
	a.IntentDigest = "sha256:" + strings.Repeat("0", 64)
	raw, refused, err := httpDeclarer(w).withFleet(mode).Execute(callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{}), a)
	if err != nil || raw != nil || refused == nil || refused.Code != pactomcp.RefusalExecuteNeedsStdio || strings.Contains(refused.Message, "repository") {
		t.Errorf("%s execute: %s %+v %v", name, raw, refused, err)
	}
}

// TestRemoteDeclareExternalReclaimUsesClaimedName: with no name, a claim of
// an external system the caller sees claimed targets the claiming service.
func TestRemoteDeclareExternalReclaimUsesClaimedName(t *testing.T) {
	w := newDeclareWorld(t)
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"},
		claimFleet(t, []string{"payments-stripe"}, map[fleet.ExternalKey]*fleet.ExternalClaim{"stripe": {Service: "payments-stripe"}}))
	raw, refused, err := rd.Plan(remoteCtx(declareCluster()), pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Registry: w.host + "/acme"})
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	if err != nil || refused != nil || p.BundleRepo != w.host+"/acme/payments-stripe" ||
		!strings.Contains(strings.Join(p.Notes, " "), "pacto claim stripe --owner team-platform --name payments-stripe") {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	if !strings.Contains(p.Signer.Why, "a person runs the plan's pacto claim command") {
		t.Fatalf("%+v", p.Signer)
	}
	// A claim the caller cannot see keeps the identity's name.
	rd.fleet = claimFleet(t, nil, map[fleet.ExternalKey]*fleet.ExternalClaim{"stripe": {Hidden: true}})
	raw, _, _ = rd.Plan(remoteCtx(declareCluster()), pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Registry: w.host + "/acme"})
	_ = json.Unmarshal(raw, &p)
	if p.BundleRepo != w.host+"/acme/stripe" {
		t.Fatalf("%s", raw)
	}
}
