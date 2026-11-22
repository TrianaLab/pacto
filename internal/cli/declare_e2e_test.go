package cli

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const e2eIssuer = "https://accounts.google.com"

// declareE2E is a declare world (an in-process registry, the kubeconfig
// cluster, no ambient token) where invoices also calls audit, a private
// Sigstore whose trusted root is on disk, and a person whose browser login
// answers as ana.
type declareE2E struct {
	*declareWorld
	ss     *testutil.Sigstore
	root   string
	logins int
	// git carries --pr's requests to a Git host fake.
	git http.RoundTripper
}

func newDeclareE2E(t *testing.T) *declareE2E {
	t.Helper()
	e := &declareE2E{declareWorld: newDeclareWorld(t), ss: testutil.New(t)}
	e.root = filepath.Join(t.TempDir(), "trusted_root.json")
	if err := os.WriteFile(e.root, e.ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	e.addAudit(t)
	return e
}

// addAudit adds audit, described by a Pacto at 1.4.0, and invoices' calls to
// it: the read proposes a range nobody accepts.
func (e *declareE2E) addAudit(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	gvr := func(g, v, r string) schema.GroupVersionResource {
		return schema.GroupVersionResource{Group: g, Version: v, Resource: r}
	}
	objs := map[schema.GroupVersionResource]*unstructured.Unstructured{
		gvr("", "v1", "services"): kubeObject("v1", "Service", "billing", "audit", map[string]any{"spec": map[string]any{"clusterIP": "10.0.0.70",
			"clusterIPs": []any{"10.0.0.70"}, "selector": map[string]any{"app": "audit"}, "ports": []any{map[string]any{"port": int64(80), "targetPort": int64(8080)}}}}),
		gvr("pacto.trianalab.io", "v1alpha1", "pactos"): kubeObject("pacto.trianalab.io/v1alpha1", "Pacto", "billing", "audit", map[string]any{
			"metadata": map[string]any{"generation": int64(1)},
			"spec":     map[string]any{"contractRef": map[string]any{"oci": "oci://reg.io/acme/contracts/audit:1.4.0"}, "target": map[string]any{"serviceName": "audit"}},
			"status": map[string]any{"observedGeneration": int64(1), "contract": map[string]any{"serviceName": "audit", "version": "1.4.0",
				"resolvedRef": "oci://reg.io/acme/contracts/audit@sha256:bb"}}}),
	}
	for r, o := range objs {
		if _, err := e.dyn.Resource(r).Namespace("billing").Create(ctx, o, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	nro := e.dyn.Resource(gvr("pacto.trianalab.io", "v1alpha1", "noderuntimeobservations")).Namespace("pacto-system")
	node, err := nro.Get(ctx, "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pods, _, _ := unstructured.NestedSlice(node.Object, "status", "pods")
	pod := pods[0].(map[string]any)
	pod["connections"] = append(pod["connections"].([]any), map[string]any{"address": "10.0.0.70", "port": int64(80), "successes": int64(2),
		"lastSuccessAt": time.Now().UTC().Format(time.RFC3339)})
	_ = unstructured.SetNestedSlice(node.Object, pods, "status", "pods")
	if _, err := nro.Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

// run runs the root command with a registry client that reaches the
// in-process registry and ana's browser login, and returns stdout.
func (e *declareE2E) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	d := defaultDeps()
	d.gitTransport = e.git
	d.freshLogin = func(_ context.Context, c authn.FreshConfig) (string, error) {
		e.logins++
		c.Prompt("https://idp/auth?prompt=login")
		return testutil.TokenWith(map[string]string{"sub": "ana@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": e2eIssuer}), nil
	}
	svc := app.NewService(oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.WithInsecureRegistries(e.host)), nil)
	root := newRootCommand(svc, VersionInfo{Version: "dev"}, d)
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	err := Execute(context.Background(), root, args)
	return out.String(), err
}

// declare runs pacto declare invoices with the private Sigstore's flags.
func (e *declareE2E) declare(t *testing.T, args ...string) string {
	t.Helper()
	base := []string{"declare", "invoices", "--registry", e.host + "/acme", "--fulcio-url", e.ss.Fulcio,
		"--timestamp-url", e.ss.TSA, "--rekor-url", "", "--trusted-root", e.root, "--yes"}
	out, err := e.run(t, append(base, args...)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return out
}

// pull pulls invoices at tag, checked as signed by ana, and returns the
// contract and the record it holds.
func (e *declareE2E) pull(t *testing.T, tag string) (*contract.Contract, *contract.InferredRecord) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "invoices")
	if out, err := e.run(t, "pull", "oci://"+e.host+"/acme/invoices:"+tag, "--verify-identity", "ana@acme.com", "--verify-issuer", e2eIssuer,
		"--trusted-root", e.root, "--output", dir); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	rec, err := contract.ReadInferredRecord(os.DirFS(dir))
	if err != nil || rec == nil || rec.Declaration == nil {
		t.Fatalf("%v %v", err, rec)
	}
	return c, rec
}

// compat is each dependency's declared compatibility.
func compat(c *contract.Contract) map[string]string {
	out := map[string]string{}
	for _, d := range c.Dependencies {
		out[d.Name] = d.Compatibility
	}
	return out
}

func TestDeclareEndToEndPerson(t *testing.T) {
	e := newDeclareE2E(t)
	out := e.declare(t, "--namespace", "billing", "--accept", "ledger")
	if e.logins != 1 || !strings.Contains(out, "published "+e.host+"/acme/invoices@sha256:") || !strings.Contains(out, "signed as ana@acme.com via "+e2eIssuer) {
		t.Fatalf("%d logins\n%s", e.logins, out)
	}
	c, rec := e.pull(t, "0.1.0")
	if got := compat(c); c.Service.Version != "0.1.0" || got["ledger"] != "^2.3.0" || got["audit"] != "unknown" {
		t.Fatalf("%s %v", c.Service.Version, got)
	}
	wantPersonDeclaration(t, rec)
	// Signed by ana, so a pull that expects anyone else gets nothing.
	if out, err := e.run(t, "pull", "oci://"+e.host+"/acme/invoices:0.1.0", "--verify-identity", "eve@acme.com", "--verify-issuer", e2eIssuer,
		"--trusted-root", e.root, "--output", t.TempDir()); err == nil || !strings.Contains(err.Error(), "nothing was pulled") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// wantPersonDeclaration: rec is ana's declaration of the billing workload,
// accepting ledger alone, with the cluster read it came from.
func wantPersonDeclaration(t *testing.T, rec *contract.InferredRecord) {
	t.Helper()
	d := rec.Declaration
	want := contract.InferredSigner{Subject: "ana@acme.com", Issuer: e2eIssuer, Class: contract.SignerClassHuman}
	if d.State != contract.DeclarationDeclared || d.Asker != want || d.InferredFrom != "k8s:billing/Deployment/invoices" || d.Cluster == "" ||
		len(d.Accepted) != 1 || d.Accepted[0].Field != "dependencies[ledger].compatibility" || d.Accepted[0].Range != "^2.3.0" || rec.Inputs.Cluster == nil {
		t.Fatalf("%s", rec.Marshal())
	}
}

func TestDeclareEndToEndDraftAccepted(t *testing.T) {
	e := newDeclareE2E(t)
	const wf = "acme/invoices/.github/workflows/declare.yml@refs/heads/main"
	t.Setenv("SIGSTORE_ID_TOKEN", testutil.TokenWith(map[string]string{"sub": "https://github.com/" + wf, "iss": "https://token.actions.githubusercontent.com",
		"job_workflow_ref": wf, "repository": "https://github.com/acme/invoices", "ref": "refs/heads/main"}))
	out := e.declare(t, "--namespace", "billing", "--accept", "ledger")
	if e.logins != 0 || !strings.Contains(out, "wrote draft-1 (") || !strings.Contains(out, "--from-draft draft-1") {
		t.Fatalf("%d logins\n%s", e.logins, out)
	}
	reads := *e.kubeconfig
	t.Setenv("SIGSTORE_ID_TOKEN", "")
	e.declare(t, "--from-draft", "draft-1", "--accept", "ledger")
	if e.logins != 1 || *e.kubeconfig != reads+1 {
		t.Fatalf("%d logins, %d cluster reads", e.logins, *e.kubeconfig-reads)
	}
	c, rec := e.pull(t, "0.1.0")
	d := rec.Declaration
	if d.State != contract.DeclarationDeclared || d.Asker.Subject != "ana@acme.com" || d.Draft == nil || d.Draft.Tag != "draft-1" ||
		d.Draft.Asker.Class != contract.SignerClassCI || rec.Inputs.Cluster == nil || compat(c)["ledger"] != "^2.3.0" || compat(c)["audit"] != "unknown" {
		t.Fatalf("%s", rec.Marshal())
	}
}
