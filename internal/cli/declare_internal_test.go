package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const declareIntent = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// declarePlanFixture is a first declaration of invoices that accepts ledger,
// planned for a person's login.
func declarePlanFixture() *app.DeclarePlan {
	return &app.DeclarePlan{Service: "invoices", Version: "0.1.0", BundleRepo: "ghcr.io/acme/invoices", State: contract.DeclarationDeclared,
		InferredFrom: "k8s:billing/Deployment/invoices", Cluster: "3f1c", Deploy: "none",
		Ranges: []app.DeclaredRange{
			{Dependency: "ledger", Compatibility: "^2.3.0", Proposed: "^2.3.0", Observed: "2.3.1", Accepted: true},
			{Dependency: "audit", Compatibility: "unknown", Proposed: "^1.4.0"},
			{Dependency: "mail", Compatibility: "unknown"},
		},
		Signer:       app.SignerView{Issuer: "https://oauth2.sigstore.dev/auth", Class: "human", Source: "login"},
		PolicyNote:   "no installation signer policy was read (forbidden); readers judge this signature with their own",
		Coverage:     []contract.InferredCoverage{{Source: "pods", State: contract.CoverageComplete}, {Source: "nodes", State: contract.CoveragePartial}},
		Notes:        []string{"your email is recorded in the public Rekor transparency log"},
		IntentDigest: declareIntent}
}

// declareDeps answers plans with plan (or err), and runs with run.
func declareDeps(t *testing.T, got *app.DeclareRequest, plan *app.DeclarePlan, err error,
	run func(app.DeclareRequest) (*app.DeclareResult, error)) deps {
	t.Helper()
	d := defaultDeps()
	d.planDeclare = func(_ *app.Service, _ context.Context, req app.DeclareRequest) (*app.DeclarePlan, error) {
		*got = req
		return plan, err
	}
	d.declare = func(_ *app.Service, _ context.Context, req app.DeclareRequest) (*app.DeclareResult, error) {
		*got = req
		if run == nil {
			t.Fatal("a plan ran the declaration")
		}
		return run(req)
	}
	return d
}

// noKube makes the declaration's kubeconfig client fail, and counts the
// clients built.
func noKube(t *testing.T) *int {
	t.Helper()
	old := initDynamic
	t.Cleanup(func() { initDynamic = old })
	calls := new(int)
	initDynamic = func() (dynamic.Interface, error) { *calls++; return nil, errors.New("no kubeconfig") }
	return calls
}

const planText = `plan: oci://ghcr.io/acme/invoices:0.1.0 (first declaration, contract only, deploy: none)
  dependencies.ledger.compatibility: ^2.3.0 (accepted by you; observed 2.3.1)
  dependencies.audit.compatibility: unknown (proposed ^1.4.0)
  dependencies.mail.compatibility: unknown
state: declared, signed by you, after a Sigstore login at https://oauth2.sigstore.dev/auth
inferredFrom k8s:billing/Deployment/invoices (cluster 3f1c)
note: your email is recorded in the public Rekor transparency log
Cluster read: 1 of 2 sources complete
no installation signer policy was read (forbidden); readers judge this signature with their own
intent: ` + declareIntent + "\n"

func TestDeclareCommandPlansWithoutYes(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	out, _, err := runRoot(t, declareDeps(t, &got, declarePlanFixture(), nil, nil), nil, "declare", "billing/invoices", "--dir", "drafts/invoices",
		"--accept", "ledger", "--accept", "audit", "--namespace", "billing", "--max-items", "50", "--call-window", "2h")
	want := planText + "nothing was written: run again with --yes --intent " + declareIntent + " to carry out this plan\n"
	if err != nil || out != want {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Service != "billing/invoices" || got.Dir != "drafts/invoices" || got.Namespace != "billing" || len(got.Accept) != 2 || got.MaxItems != 50 ||
		got.CallWindow.Hours() != 2 || got.Signing.OIDCIssuer != "https://oauth2.sigstore.dev/auth" || got.Signing.OIDCClient != "sigstore" ||
		got.Signing.FulcioURL != signing.PublicFulcio || got.Signing.RekorURL != signing.PublicRekor || got.Dynamic != nil {
		t.Fatalf("%+v", got)
	}
}

func TestDeclareCommandYesPublishes(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *declarePlanFixture()
		_ = req.OnPlan(&p)
		p.Signer.Subject, p.Signer.Issuer = "ana@acme.com", "https://accounts.google.com"
		return &app.DeclareResult{Plan: p, Executed: true, Signed: true, Tag: "0.1.0", Published: "ghcr.io/acme/invoices@sha256:7c1d"}, nil
	}
	out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "declare", "invoices", "--yes", "--intent", declareIntent,
		"--sign-key", "cosign.key", "--device", "--oidc-issuer-url", "https://id.acme.com", "--oidc-client-id", "pacto",
		"--fulcio-url", "https://fulcio.acme.com", "--timestamp-url", "https://tsa.acme.com", "--rekor-url", "", "--trusted-root", "root.json")
	want := planText + "signed as ana@acme.com via https://accounts.google.com\npublished ghcr.io/acme/invoices@sha256:7c1d (inferredFrom k8s:billing/Deployment/invoices)\n"
	if err != nil || out != want {
		t.Fatalf("%v\n%s", err, out)
	}
	s := got.Signing
	if got.IntentDigest != declareIntent || s.Key != "cosign.key" || !s.Device || s.OIDCIssuer != "https://id.acme.com" || s.OIDCClient != "pacto" ||
		s.FulcioURL != "https://fulcio.acme.com" || s.TimestampURL != "https://tsa.acme.com" || s.RekorURL != "" || s.TrustedRoot != "root.json" {
		t.Fatalf("%+v", s)
	}
}

func TestDeclareCommandPrintsUX(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	draft := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *declarePlanFixture()
		p.Baseline = &app.DeclareBaseline{InferredBaseline: contract.InferredBaseline{Version: "1.4.0"}}
		p.Deploy, p.Version, p.Cluster, p.Coverage, p.PolicyNote, p.Notes = "ghcr.io/acme/invoices-deploy@sha256:aa", "1.5.0", "", nil, "", nil
		p.Ranges = nil
		p.State, p.Signer = contract.DeclarationDraft, app.SignerView{Subject: "ci", Issuer: "https://token.actions.githubusercontent.com", Class: "ci", Source: "ambient", Why: "an ambient token writes a draft"}
		p.Skipped = []app.SkippedTag{{Tag: "2.0.0", Signer: "eve@evil.com", Why: "no publisher rule covers it"}}
		_ = req.OnPlan(&p)
		p.AcceptCLI = "pacto declare invoices --from-draft draft-3"
		return &app.DeclareResult{Plan: p, Executed: true, Draft: true, Tag: "draft-3", Published: "ghcr.io/acme/invoices@sha256:dd"}, nil
	}
	out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, draft), nil, "declare", "invoices", "--yes")
	want := `plan: oci://ghcr.io/acme/invoices:1.5.0 (from 1.4.0, contract only, deploy: ghcr.io/acme/invoices-deploy@sha256:aa)
state: draft, signed by ci via https://token.actions.githubusercontent.com (an ambient token writes a draft)
inferredFrom k8s:billing/Deployment/invoices
skipped 2.0.0, signed by eve@evil.com: no publisher rule covers it
intent: ` + declareIntent + `
wrote draft-3 (ghcr.io/acme/invoices@sha256:dd): a person accepts it with pacto declare invoices --from-draft draft-3
`
	if err != nil || out != want {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range []struct {
		s    app.SignerView
		want string
	}{
		{app.SignerView{Source: "none", Why: "unsigned draft"}, "nobody (an unsigned draft) (unsigned draft)"},
		{app.SignerView{Source: "key"}, "a key that names no person"},
	} {
		if s := signerLine(c.s); s != c.want {
			t.Errorf("%q, want %q", s, c.want)
		}
	}
	unchanged := func(app.DeclareRequest) (*app.DeclareResult, error) {
		return &app.DeclareResult{Plan: app.DeclarePlan{Service: "invoices", Unchanged: "1.4.0"}}, nil
	}
	if out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, unchanged), nil, "declare", "invoices", "--yes"); err != nil ||
		out != "plan: invoices unchanged since 1.4.0: nothing to publish\n" {
		t.Fatalf("%v %q", err, out)
	}
	if out, _, err := runRoot(t, declareDeps(t, &got, &app.DeclarePlan{Service: "invoices", Unchanged: "1.4.0"}, nil, nil), nil, "declare", "invoices"); err != nil ||
		out != "plan: invoices unchanged since 1.4.0: nothing to publish\n" {
		t.Fatalf("%v %q", err, out)
	}
	// A run that stopped before writing prints nothing more than its plan did.
	stopped := func(app.DeclareRequest) (*app.DeclareResult, error) {
		return &app.DeclareResult{Plan: *declarePlanFixture()}, nil
	}
	if out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, stopped), nil, "declare", "invoices", "--yes"); err != nil || out != "" {
		t.Fatalf("%v %q", err, out)
	}
}

func TestDeclareCommandJSON(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	out, _, err := runRoot(t, declareDeps(t, &got, declarePlanFixture(), nil, nil), nil, "declare", "invoices", "--output-format", "json")
	var plan app.DeclarePlan
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil || plan.IntentDigest != declareIntent || len(plan.Ranges) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		if req.OnPlan != nil {
			t.Error("JSON prints the plan before the login")
		}
		return &app.DeclareResult{Plan: *declarePlanFixture(), Executed: true, Draft: true, Tag: "draft-1"}, nil
	}
	out, _, err = runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "declare", "invoices", "--yes", "--output-format", "json")
	var res app.DeclareResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Tag != "draft-1" || res.Plan.Service != "invoices" {
		t.Fatalf("%v\n%s", err, out)
	}
	refused := &app.DeclareError{Code: "RangeNotProposed", Message: "no range was proposed for mail", CLI: "pacto declare invoices"}
	for _, args := range [][]string{{"declare", "invoices"}, {"declare", "invoices", "--yes"}} {
		fail := func(app.DeclareRequest) (*app.DeclareResult, error) { return &app.DeclareResult{}, refused }
		out, _, err = runRoot(t, declareDeps(t, &got, declarePlanFixture(), refused, fail), nil, append(args, "--output-format", "json")...)
		var doc struct {
			Refusal app.DeclareError `json:"refusal"`
			Exit    exitDoc          `json:"exit"`
		}
		if err == nil || json.Unmarshal([]byte(out), &doc) != nil || !reflect.DeepEqual(doc.Refusal, *refused) || doc.Exit.Code != 1 {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
}

func TestDeclareCommandRefusalExitCodes(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	for _, code := range []string{"PlanStale", "IntentRequired", "WorkloadNotFound", "WorkloadAmbiguous", "AlreadyDeclared",
		"RangeNotProposed", "NoRegistry", "RegistryUnreadable", "VersionConflict", "SignerNotAllowed", "SignerUnverified", "DraftNotFound",
		"ExecuteNeedsStdio", "BaselineNeedsMigration", "BundleRepoMismatch"} {
		refused := &app.DeclareError{Code: code, Message: code + " happened", CLI: "pacto declare invoices --intent x"}
		fail := func(app.DeclareRequest) (*app.DeclareResult, error) { return &app.DeclareResult{}, refused }
		for _, args := range [][]string{{"declare", "invoices"}, {"declare", "invoices", "--yes"}} {
			_, _, err := runRoot(t, declareDeps(t, &got, nil, refused, fail), nil, args...)
			var exit *ExitError
			if err == nil || errors.As(err, &exit) || jsonExitOf(err).Code != 1 || err.Error() != code+" happened\nrun: pacto declare invoices --intent x" {
				t.Fatalf("%s %v: %v", code, args, err)
			}
		}
	}
	// A refusal with no command prints its message alone.
	bare := &app.DeclareError{Code: "VersionConflict", Message: "taken"}
	if _, _, err := runRoot(t, declareDeps(t, &got, nil, bare, nil), nil, "declare", "invoices"); err == nil || err.Error() != "taken" {
		t.Fatal(err)
	}
}

// sharedOut is stdout that a fake declaration reads at the moment the login
// would start.
type sharedOut struct{ bytes.Buffer }

func TestDeclareCommandPrintsPlanBeforeLogin(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	out := &sharedOut{}
	var atLogin string
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		_ = req.OnPlan(declarePlanFixture())
		atLogin = out.String()
		req.Signing.Prompt("Opening https://oauth2.sigstore.dev/auth")
		return &app.DeclareResult{Plan: *declarePlanFixture()}, nil
	}
	_, stderr, err := runRoot(t, declareDeps(t, &got, nil, nil, run), out, "declare", "invoices", "--yes")
	if err != nil || atLogin != planText || stderr != "Opening https://oauth2.sigstore.dev/auth\n" {
		t.Fatalf("%v %q %q", err, atLogin, stderr)
	}
}

func TestDeclareCommandRefusesOtherSubjectKinds(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	d := declareDeps(t, &got, declarePlanFixture(), nil, nil)
	for _, arg := range []string{"service:x", "workload:y"} {
		if _, _, err := runRoot(t, d, nil, "declare", arg); err == nil || !strings.Contains(err.Error(), "a workload, an offering:<name> or a golden path (template:<namespace>/<name>)") {
			t.Fatalf("%s: %v", arg, err)
		}
	}
	if _, _, err := runRoot(t, d, nil, "declare", "k8s:billing/Deployment/invoices"); err != nil || got.Service != "billing/Deployment/invoices" {
		t.Fatalf("%v %+v", err, got)
	}
}

// templateCatalog is a catalog that holds springboot-service.
func templateCatalog(t *testing.T) string {
	t.Helper()
	t.Setenv(backstageTokenEnv, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"apiVersion":"scaffolder.backstage.io/v1beta3","kind":"Template",` +
			`"metadata":{"name":"springboot-service","namespace":"default","title":"Spring Boot"}}],"pageInfo":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestDeclareCommandAcceptsTemplateSubject: the subject reaches the plan with
// the template read from --backstage-url, and a kubeconfig that fails only
// drops the signer policy read.
func TestDeclareCommandAcceptsTemplateSubject(t *testing.T) {
	noKube(t)
	url := templateCatalog(t)
	var got app.DeclareRequest
	d := declareDeps(t, &got, declarePlanFixture(), nil, nil)
	if _, _, err := runRoot(t, d, nil, "declare", "template:default/springboot-service", "--registry", "ghcr.io/acme/paths", "--backstage-url", url); err != nil {
		t.Fatal(err)
	}
	if got.TemplateSubject != "template:default/springboot-service" || got.Service != "springboot-service" || got.BackstageURL != url ||
		got.Template == nil || got.Template.Entity != got.TemplateSubject || got.Dynamic != nil {
		t.Fatalf("%+v", got)
	}
	// Gone from the catalog: refused with the code and no plan.
	got = app.DeclareRequest{}
	_, _, err := runRoot(t, d, nil, "declare", "template:default/other", "--registry", "ghcr.io/acme/paths", "--backstage-url", url)
	var de *app.DeclareError
	if !errors.As(err, &de) || de.Code != "TemplateNotFound" || got.TemplateSubject != "" {
		t.Fatalf("%v %+v", err, got)
	}
	// Without a registry nothing is read: the plan refuses it.
	if _, _, err := runRoot(t, d, nil, "declare", "template:default/springboot-service"); err != nil || got.Template != nil || got.TemplateSubject == "" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestDeclareTemplateRefusesWorkloadFlags(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	d := declareDeps(t, &got, declarePlanFixture(), nil, nil)
	for _, flags := range [][]string{{"--dir", "x"}, {"--namespace", "billing"}, {"--accept", "ledger"}, {"--max-items", "3"}, {"--call-window", "1h"},
		{"--pr", "--yes"}, {"--repository", "https://github.com/acme/x"}, {"--folder", "x"}, {"--repo-map", "m.yaml"}, {"--git-host", "github"}} {
		args := append([]string{"declare", "template:default/springboot-service", "--registry", "ghcr.io/acme"}, flags...)
		if _, _, err := runRoot(t, d, nil, args...); err == nil || !strings.Contains(err.Error(), flags[0]+" names a workload") {
			t.Fatalf("%v: %v", flags, err)
		}
	}
	if _, _, err := runRoot(t, d, nil, "declare", "invoices", "--backstage-url", "https://catalog.acme.io"); err == nil ||
		!strings.Contains(err.Error(), "--backstage-url reads a template:<namespace>/<name> subject") {
		t.Fatal(err)
	}
	if got.Service != "" {
		t.Fatalf("planned %+v", got)
	}
}

// TestDeclareTemplateSubjectSameOnEverySurface: the CLI argument,
// pacto_declare.service and the dashboard's body.serviceKey reach
// DeclareRequest.TemplateSubject as typed, with the template's name as
// Service.
func TestDeclareTemplateSubjectSameOnEverySurface(t *testing.T) {
	const subject = "template:default/springboot-service"
	var cli app.DeclareRequest
	err := declareSubject(&cli, subject)
	mcp, mcpErr := mcpDeclarer{}.request(pactomcp.DeclareArgs{Service: subject})
	h := newDeclareHost(t, true, false)
	dash, dashErr := h.d.request(h.req(), springTemplate(true), nil, nil, dashboard.ClaimInput{}, nil)
	for name, req := range map[string]app.DeclareRequest{"cli": cli, "mcp": mcp, "dashboard": dash} {
		if req.TemplateSubject != subject || req.Service != "springboot-service" {
			t.Errorf("%s: %+v", name, req)
		}
	}
	if err != nil || mcpErr != nil || dashErr != nil || dash.Template == nil {
		t.Fatalf("%v %v %v", err, mcpErr, dashErr)
	}
}

func TestDeclareCommandFromDraft(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *declarePlanFixture()
		p.Unbacked = []string{"service.owner"}
		_ = req.OnPlan(&p)
		return &app.DeclareResult{Plan: p}, nil
	}
	out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "declare", "invoices", "--from-draft", "draft-3", "--accept", "audit", "--yes")
	if err != nil || got.FromDraft != "draft-3" || !strings.Contains(out, "not backed by the cluster read or the baseline: service.owner\n") {
		t.Fatalf("%v %+v\n%s", err, got, out)
	}
}

// A plan-only --from-draft run plans the draft: its unbacked changes, or why
// they were not checked, and the digest a later --yes --intent run takes.
func TestDeclareCommandFromDraftPlanOnly(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	p := declarePlanFixture()
	p.Unbacked, p.UnbackedUnchecked = []string{"service.owner team"}, ""
	out, _, err := runRoot(t, declareDeps(t, &got, p, nil, nil), nil, "declare", "invoices", "--from-draft", "draft-3")
	if err != nil || got.FromDraft != "draft-3" || !strings.Contains(out, "\nnot backed by the cluster read or the baseline: service.owner team\n") ||
		strings.Count(out, "not backed") != 1 || !strings.Contains(out, "--yes --intent "+declareIntent) {
		t.Fatalf("%v %+v\n%s", err, got, out)
	}
	p.Unbacked, p.UnbackedUnchecked = nil, "disk full"
	out, _, _ = runRoot(t, declareDeps(t, &got, p, nil, nil), nil, "declare", "invoices", "--from-draft", "draft-3")
	if !strings.Contains(out, "unbacked changes unknown: nothing was checked against the cluster and the baseline (disk full)\n") {
		t.Fatalf("%s", out)
	}
}

// TestDeclareCommandKubeClient: a run with a registry reads the cluster with
// the kubeconfig client; a client that fails refuses a cluster read, and a
// --dir run declares the folder without it.
func TestDeclareCommandKubeClient(t *testing.T) {
	calls := noKube(t)
	var got app.DeclareRequest
	d := declareDeps(t, &got, declarePlanFixture(), nil, nil)
	if _, _, err := runRoot(t, d, nil, "declare", "invoices", "--registry", "ghcr.io/acme"); err == nil || !strings.Contains(err.Error(), "no kubeconfig") || *calls != 1 {
		t.Fatalf("%v %d", err, *calls)
	}
	if _, _, err := runRoot(t, d, nil, "declare", "invoices", "--registry", "ghcr.io/acme", "--dir", "x"); err != nil || got.Dynamic != nil {
		t.Fatalf("%v %+v", err, got)
	}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	initDynamic = func() (dynamic.Interface, error) { return dyn, nil }
	if _, _, err := runRoot(t, d, nil, "declare", "invoices", "--registry", "ghcr.io/acme"); err != nil || got.Dynamic != dyn || got.Registry != "ghcr.io/acme" {
		t.Fatalf("%v %+v", err, got)
	}
}

// TestPlanDeclareDeps: the production plan function refuses a run without a
// registry before it reads anything.
func TestPlanDeclareDeps(t *testing.T) {
	_, err := planDeclare(app.NewService(nil, nil), t.Context(), app.DeclareRequest{Service: "invoices"})
	var de *app.DeclareError
	if !errors.As(err, &de) || de.Code != "NoRegistry" {
		t.Fatal(err)
	}
}

// TestPullVerifyFlags: the --verify-* flags name one signature, or none.
func TestPullVerifyFlags(t *testing.T) {
	for _, c := range []struct {
		args []string
		want *contract.PublishSign
		err  string
	}{
		{nil, nil, ""},
		{[]string{"--verify-key", "cosign.pub"}, &contract.PublishSign{Key: "cosign.pub"}, ""},
		{[]string{"--verify-identity", "ana@acme.com", "--verify-issuer", "https://accounts.google.com"},
			&contract.PublishSign{Keyless: true, Identity: "ana@acme.com", Issuer: "https://accounts.google.com"}, ""},
		{[]string{"--verify-identity", "ana@acme.com"}, nil, "go together"},
		{[]string{"--verify-key", "k", "--verify-identity", "ana@acme.com"}, nil, "checked one way"},
	} {
		cmd := newPullCommand(app.NewService(nil, nil), nil)
		_ = cmd.ParseFlags(c.args)
		got, err := pullVerify(cmd)
		if (err == nil) != (c.err == "") || (err != nil && !strings.Contains(err.Error(), c.err)) || (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%v: %+v %v", c.args, got, err)
		}
	}
	// A refused combination stops the pull before any read.
	if _, _, err := runRoot(t, defaultDeps(), nil, "pull", "oci://ghcr.io/acme/svc:1.0.0", "--verify-identity", "x"); err == nil || !strings.Contains(err.Error(), "go together") {
		t.Fatal(err)
	}
}

func TestDeclareDraftFlag(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	run := func(req app.DeclareRequest) (*app.DeclareResult, error) {
		p := *declarePlanFixture()
		p.State, p.AcceptCLI = contract.DeclarationDraft, "pacto declare invoices --registry ghcr.io/acme --from-draft draft-1"
		return &app.DeclareResult{Plan: p, Executed: true, Draft: true, Tag: "draft-1", Published: "ghcr.io/acme/invoices@sha256:7c1d"}, nil
	}
	out, _, err := runRoot(t, declareDeps(t, &got, nil, nil, run), nil, "declare", "invoices", "--draft", "--yes")
	if err != nil || !got.Draft || !strings.Contains(out, "wrote draft-1 (ghcr.io/acme/invoices@sha256:7c1d): a person accepts it with pacto declare invoices") {
		t.Fatalf("%v %+v\n%s", err, got, out)
	}
}

func TestDeclareDraftAndFromDraftRefused(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	_, _, err := runRoot(t, declareDeps(t, &got, declarePlanFixture(), nil, nil), nil, "declare", "invoices", "--draft", "--from-draft", "draft-3")
	if err == nil || !strings.Contains(err.Error(), "pass only one of them") || got.Service != "" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestDeclarePlanPrintsDrafts(t *testing.T) {
	noKube(t)
	var got app.DeclareRequest
	p := declarePlanFixture()
	p.Drafts = []app.DraftView{
		{Tag: "draft-3", State: contract.DeclarationDraft, OnBehalfOf: "ana", Requested: []string{"audit ^1.4.0"},
			Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}},
		{Tag: "draft-2", State: contract.DeclarationDraft, Signed: true, Asker: contract.InferredSigner{Subject: "ci@acme.com", Class: "ci"}},
		{Tag: "draft-1", State: contract.DeclarationDraft, Reason: "its signature could not be checked: offline"},
		{Tag: "draft-0", State: "unreadable", Reason: "it holds no readable declaration"},
	}
	p.DraftsTotal = 25
	out, _, err := runRoot(t, declareDeps(t, &got, p, nil, nil), nil, "declare", "invoices")
	want := "Drafts (newest 4 of 25):\n" +
		"  draft-3 by an agent for ana, requests audit ^1.4.0 (unsigned)\n" +
		"  draft-2 by ci@acme.com\n" +
		"  draft-1 by an agent (unsigned) (its signature could not be checked: offline)\n" +
		"  draft-0 unreadable: it holds no readable declaration\n" +
		"intent: "
	if err != nil || !strings.Contains(out, want) {
		t.Fatalf("%v\n%s", err, out)
	}
	p.Drafts, p.DraftsTotal = p.Drafts[:1], 1
	out, _, _ = runRoot(t, declareDeps(t, &got, p, nil, nil), nil, "declare", "invoices")
	if !strings.Contains(out, "Drafts:\n  draft-3 by an agent") {
		t.Fatalf("%s", out)
	}
}

// TestDraftLineNamesOnlyAVerifiedAsker: an asker a signature does not prove
// is never printed as the draft's author.
func TestDraftLineNamesOnlyAVerifiedAsker(t *testing.T) {
	ana := contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}
	for _, c := range []struct {
		d    app.DraftView
		want string
	}{
		{app.DraftView{Tag: "draft-4", Asker: ana, Signed: true}, "draft-4 by ana@acme.com"},
		{app.DraftView{Tag: "draft-4", Asker: ana}, "draft-4 by a person (unsigned)"},
		{app.DraftView{Tag: "draft-4", Asker: contract.InferredSigner{Class: contract.SignerClassAgent}, OnBehalfOf: "ana"}, "draft-4 by an agent for ana (unsigned)"},
	} {
		if got := draftLine(c.d); got != c.want {
			t.Errorf("draftLine = %q, want %q", got, c.want)
		}
	}
}
