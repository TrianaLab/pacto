package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// httpDeclarer is pacto_declare over HTTP with a registry and a push
// credential: it writes the caller's drafts into w's registry.
func httpDeclarer(w *declareWorld) remoteDeclarer {
	rd := newRemoteDeclarer(app.NewService(nil, nil), []string{"oci://" + w.host + "/acme"}, nil)
	rd.registry, rd.push = w.host+"/acme", oci.NewKeychain(oci.EnvCredentialOptions())
	return rd
}

// selfReviewed is the caller's own cluster, answering every
// SelfSubjectAccessReview with answer and recording what it was asked.
type selfReviewed struct {
	dyn    *dynamicfake.FakeDynamicClient
	asked  []map[string]any
	answer func() (map[string]any, error)
}

func newSelfReviewed(allowed bool) *selfReviewed {
	s := &selfReviewed{dyn: declareCluster(), answer: func() (map[string]any, error) { return map[string]any{"allowed": allowed}, nil }}
	s.dyn.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		attrs, _, _ := unstructured.NestedMap(o.Object, "spec", "resourceAttributes")
		s.asked = append(s.asked, attrs)
		status, err := s.answer()
		if err != nil {
			return true, nil, err
		}
		return true, &unstructured.Unstructured{Object: map[string]any{"status": status}}, nil
	})
	return s
}

// callerCtx is a request reviewed as user, whose own client is dyn, logging
// into log.
func callerCtx(user string, s *selfReviewed, log *bytes.Buffer) context.Context {
	ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(log, nil)))
	return context.WithValue(ctx, reviewedKey{}, reviewed{person: authn.Person{User: user}, own: &app.Delivery{Dynamic: s.dyn}})
}

// planThenWrite plans a in ctx and writes with the plan's digest.
func planThenWrite(t *testing.T, rd remoteDeclarer, ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused) {
	t.Helper()
	raw, refused, err := rd.Plan(ctx, a)
	if err != nil || refused != nil {
		t.Fatalf("%s %+v %v", raw, refused, err)
	}
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	a.IntentDigest = p.IntentDigest
	raw, refused, err = rd.Execute(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	return raw, refused
}

// invoicesArgs are pacto_declare's arguments for billing/invoices in w.
func invoicesArgs(w *declareWorld) pactomcp.DeclareArgs {
	return pactomcp.DeclareArgs{Service: "invoices", Namespace: "billing", Registry: w.host + "/acme", Accept: []string{"ledger"}}
}

func TestMCPHTTPDeclareWritesDraft(t *testing.T) {
	w := newDeclareWorld(t)
	s := newSelfReviewed(true)
	raw, refused := planThenWrite(t, httpDeclarer(w), callerCtx("alice", s, &bytes.Buffer{}), invoicesArgs(w))
	var run struct {
		Executed  bool   `json:"executed"`
		Tag       string `json:"tag"`
		Draft     bool   `json:"draft"`
		Signed    bool   `json:"signed"`
		AcceptCLI string `json:"acceptCLI"`
	}
	_ = json.Unmarshal(raw, &run)
	if refused != nil || !run.Executed || run.Tag != "draft-1" || !run.Draft || run.Signed ||
		run.AcceptCLI != "pacto declare invoices --registry "+w.host+"/acme --from-draft draft-1" {
		t.Fatalf("%s %+v", raw, refused)
	}
	d := w.pulled(t, "draft-1").Declaration
	if d.State != contract.DeclarationDraft || d.Asker != (contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}) || d.OnBehalfOf != "alice" {
		t.Fatalf("%+v", d)
	}
	if *w.kubeconfig != 0 || len(w.dyn.Actions()) != 0 {
		t.Fatalf("the server read the cluster with its own credentials: %d %d", *w.kubeconfig, len(w.dyn.Actions()))
	}
}

func TestMCPHTTPDeclareDraftReviewsCaller(t *testing.T) {
	w := newDeclareWorld(t)
	s := newSelfReviewed(true)
	planThenWrite(t, httpDeclarer(w), callerCtx("alice", s, &bytes.Buffer{}), invoicesArgs(w))
	want := map[string]any{"verb": "update", "group": "apps", "resource": "deployments", "name": "invoices", "namespace": "billing"}
	if len(s.asked) != 1 || len(s.asked[0]) != len(want) {
		t.Fatalf("reviews %v", s.asked)
	}
	for k, v := range want {
		expect(t, s.asked[0][k] == v, "review %v: %s", s.asked[0], k)
	}
	for _, c := range []struct {
		name   string
		answer func() (map[string]any, error)
		code   pactomcp.WriteRefusal
	}{
		{"denied", func() (map[string]any, error) { return map[string]any{"allowed": false}, nil }, pactomcp.RefusalNotAllowed},
		{"failed", func() (map[string]any, error) { return nil, errors.New("apiserver down") }, pactomcp.RefusalReviewFailed},
		{"unevaluated", func() (map[string]any, error) {
			return map[string]any{"allowed": false, "evaluationError": "webhook"}, nil
		}, pactomcp.RefusalReviewFailed},
	} {
		w := newDeclareWorld(t)
		s := newSelfReviewed(true)
		s.answer = c.answer
		raw, refused := planThenWrite(t, httpDeclarer(w), callerCtx("alice", s, &bytes.Buffer{}), invoicesArgs(w))
		expect(t, raw == nil && refused != nil && refused.Code == c.code, "%s: %+v", c.name, refused)
		expect(t, len(tagsOf(t, w)) == 0, "%s: tags %v", c.name, tagsOf(t, w))
	}
}

// tagsOf are the tags of the invoices repository of w.
func tagsOf(t *testing.T, w *declareWorld) []string {
	t.Helper()
	return (&declareHost{w: w}).tags(t)
}

func TestMCPHTTPDraftOnBehalfOfIsCaller(t *testing.T) {
	w := newDeclareWorld(t)
	rd := httpDeclarer(w)
	ctx := callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{})
	a := invoicesArgs(w)
	a.OnBehalfOf = "bob"
	raw, refused := planThenWrite(t, rd, ctx, a)
	expect(t, raw == nil && refused != nil && refused.Code == pactomcp.RefusalNotAllowed && strings.Contains(refused.Message, "alice"), "%+v", refused)
	expect(t, len(tagsOf(t, w)) == 0, "tags %v", tagsOf(t, w))
	a.OnBehalfOf = "alice"
	_, refused = planThenWrite(t, rd, ctx, a)
	expect(t, refused == nil && w.pulled(t, "draft-1").Declaration.OnBehalfOf == "alice", "%+v", refused)
}

func TestMCPHTTPDraftAuditLine(t *testing.T) {
	w := newDeclareWorld(t)
	log := &bytes.Buffer{}
	planThenWrite(t, httpDeclarer(w), callerCtx("alice", newSelfReviewed(true), log), invoicesArgs(w))
	for _, want := range []string{"declaration written", "via=mcp", "actor=alice", "service=invoices", "tag=draft-1", "published=" + w.host + "/acme/invoices@sha256:"} {
		expect(t, strings.Contains(log.String(), want), "audit line %q lacks %q", log, want)
	}
}

func TestMCPHTTPDraftRefusesRepoOfOtherWorkload(t *testing.T) {
	w := newDeclareWorld(t)
	rd := httpDeclarer(w)
	s := newSelfReviewed(true)
	ctx := callerCtx("alice", s, &bytes.Buffer{})
	planThenWrite(t, rd, ctx, invoicesArgs(w))
	if err := s.dyn.Tracker().Add(kubeObject("apps/v1", "Deployment", "dev", "invoices", map[string]any{"spec": map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"app": "invoices"}}, "template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": "invoices"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "invoices", "image": "reg.io/acme/invoices:1.0.0"}}}}}})); err != nil {
		t.Fatal(err)
	}
	a := invoicesArgs(w)
	a.Namespace, a.Accept = "dev", nil
	raw, refused := planThenWrite(t, rd, ctx, a)
	expect(t, raw == nil && refused != nil && refused.Code == pactomcp.RefusalNotAllowed && strings.Contains(refused.Message, "belongs to k8s:billing/Deployment/invoices"),
		"%+v", refused)
	expect(t, slices.Equal(tagsOf(t, w), []string{"draft-1"}), "tags %v", tagsOf(t, w))
}

func TestMCPHTTPDeclareNoRegistryNeedsStdio(t *testing.T) {
	w := newDeclareWorld(t)
	ctx := callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{})
	rd := httpDeclarer(w)
	rd.push = nil
	_, refused := planThenWrite(t, rd, ctx, invoicesArgs(w))
	expect(t, refused != nil && refused.Code == pactomcp.RefusalExecuteNeedsStdio, "no push credential: %+v", refused)
	// With no registry the draft argument is refused before any read.
	_, refused, err := rd.Plan(ctx, pactomcp.DeclareArgs{Service: "invoices", Draft: true, Registry: w.host + "/acme"})
	expect(t, err == nil && refused != nil && refused.Code == pactomcp.RefusalExecuteNeedsStdio, "%+v %v", refused, err)
	// A server that writes takes the draft argument, and writes only into its own registry.
	rd = httpDeclarer(w)
	rd.repos = append(rd.repos, w.host+"/other/x")
	a := invoicesArgs(w)
	a.Draft, a.Registry = true, w.host+"/other"
	_, refused = planThenWrite(t, rd, ctx, a)
	expect(t, refused != nil && refused.Code == pactomcp.RefusalNoRegistry && strings.Contains(refused.Message, w.host+"/acme"), "%+v", refused)
	// A request nobody reviewed writes nothing.
	a.Registry = w.host + "/acme"
	_, _, err = rd.Execute(context.Background(), a)
	expect(t, errors.Is(err, errNobodyReviewed), "%v", err)
	a.CallWindow = "x"
	_, _, err = rd.Execute(ctx, a)
	expect(t, err != nil, "a bad call_window wrote")
	// A plan that changed since its digest is stale; one refused names why.
	a.CallWindow, a.IntentDigest = "", "sha256:old"
	raw, refused, _ := rd.Execute(ctx, a)
	expect(t, raw == nil && refused != nil && refused.Code == pactomcp.RefusalPlanStale, "%+v", refused)
	a.Accept = []string{"nope"}
	_, refused, _ = rd.Execute(ctx, a)
	expect(t, refused != nil && refused.Code == pactomcp.RefusalRangeNotProposed, "%+v", refused)
	expect(t, len(tagsOf(t, w)) == 0, "tags %v", tagsOf(t, w))
}

func TestMCPHTTPDeclareNeverSigns(t *testing.T) {
	w := newDeclareWorld(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken())
	_, refused := planThenWrite(t, httpDeclarer(w), callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{}), invoicesArgs(w))
	d := w.pulled(t, "draft-1").Declaration
	expect(t, refused == nil && d.Asker.Issuer == contract.SignerUnsigned && d.Asker.Subject == "", "%+v %+v", refused, d.Asker)
}

// TestServerDraftWriterSharedByDashboardAndMCP: a draft the dashboard saved
// for ana is the one an agent asks for on her behalf over HTTP: one writer,
// one draft.
func TestServerDraftWriterSharedByDashboardAndMCP(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	raw, refused := planThenWrite(t, httpDeclarer(h.w), callerCtx("ana@acme.com", newSelfReviewed(true), &bytes.Buffer{}), invoicesArgs(h.w))
	expect(t, refused == nil && strings.Contains(string(raw), `"tag":"draft-1"`) && strings.Contains(string(raw), `"executed":false`), "%s %+v", raw, refused)
	expect(t, slices.Equal(h.tags(t), []string{"draft-1"}), "tags %v", h.tags(t))
}

func TestDeclareWriterFlags(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(cfg, []byte(`{"auths":{"reg.io":{"auth":"dTpw"}}}`), 0o600)
	for _, c := range []struct {
		flags  map[string]string
		writes bool
		fails  bool
	}{
		{flags: map[string]string{}},
		{flags: map[string]string{"registry": "reg.io/acme", "declare-registry-config": cfg}, writes: true},
		{flags: map[string]string{"registry": "reg.io/acme"}, fails: true},
		{flags: map[string]string{"declare-registry-config": cfg}, fails: true},
		{flags: map[string]string{"registry": "reg.io/acme", "declare-registry-config": cfg + ".missing"}, fails: true},
	} {
		cmd := newMCPCommand(app.NewService(nil, nil), "v", defaultDeps())
		for k, v := range c.flags {
			_ = cmd.Flags().Set(k, v)
		}
		registry, push, err := declareWriter(cmd)
		expect(t, (err != nil) == c.fails && (c.fails || (registry != "" && push != nil) == c.writes), "%v: %q %v %v", c.flags, registry, push, err)
	}
	// Over stdio the flags are refused.
	cmd := newMCPCommand(app.NewService(nil, nil), "v", defaultDeps())
	_ = cmd.Flags().Set("declare-registry-config", cfg)
	expect(t, errors.Is(checkTransportOnly(cmd, app.NewService(nil, nil), "stdio", false), errDeclareWriterOverStdio), "stdio took the push credential")
	expect(t, checkTransportOnly(cmd, app.NewService(nil, nil), "http", false) == nil, "http refused the push credential")
}

// TestRemoteMCPDeclareWriterMisconfigured: a server given half the draft
// writer does not start.
func TestRemoteMCPDeclareWriterMisconfigured(t *testing.T) {
	d := defaultDeps()
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(context.Background())
	for k, v := range map[string]string{"fleet": "true", "transport": "http", "auth-issuer": "https://idp", "registry": "reg.io/acme"} {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}, issuer: "https://idp"}
	err := remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s)
	expect(t, err != nil && strings.Contains(err.Error(), "go together"), "%v", err)
}

func TestMCPWrittenPassesErrors(t *testing.T) {
	_, refused, err := mcpWritten(nil, errors.New("push failed"))
	expect(t, refused == nil && err != nil && err.Error() == "push failed", "%+v %v", refused, err)
}

// TestMCPHTTPExecuteHidesBaselineRefusal: an execute that never planned,
// for a service the fleet does not show the caller, is refused as Plan
// refuses it: naming no tag, signer or repository of the baseline.
func TestMCPHTTPExecuteHidesBaselineRefusal(t *testing.T) {
	w := newDeclareWorld(t)
	declareFirst(t, w)
	a := invoicesArgs(w)
	a.Accept, a.IntentDigest = nil, "sha256:"+strings.Repeat("0", 64)
	raw, refused, err := httpDeclarer(w).Execute(callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{}), a)
	expect(t, err == nil && raw == nil && refused != nil && refused.Code == pactomcp.RefusalVersionConflict &&
		strings.Contains(refused.Message, "is hidden") && !strings.Contains(refused.Message, "signed by") && !strings.Contains(refused.Message, "0.1.0"),
		"%+v %v", refused, err)
}

// TestMCPHTTPPlanPromisesNoDraftElsewhere: a registry the server reads but
// never writes plans no draft, and the execute refusal names the command.
func TestMCPHTTPPlanPromisesNoDraftElsewhere(t *testing.T) {
	w := newDeclareWorld(t)
	rd := httpDeclarer(w)
	rd.repos = append(rd.repos, w.host+"/other/x")
	a := invoicesArgs(w)
	a.Registry = w.host + "/other"
	ctx := callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{})
	raw, refused, err := rd.Plan(ctx, a)
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	expect(t, err == nil && refused == nil && p.State == "" && p.AcceptCLI == "", "%s %+v %v", raw, refused, err)
	a.IntentDigest = p.IntentDigest
	_, refused, _ = rd.Execute(ctx, a)
	expect(t, refused != nil && refused.Code == pactomcp.RefusalNoRegistry && strings.HasPrefix(refused.CLI, "pacto declare invoices"), "%+v", refused)
}

// TestMCPHTTPRepeatedDraftSameAcceptCLI: a repeated write of a k8s subject
// answers the draft it already wrote with the command the first answer named.
func TestMCPHTTPRepeatedDraftSameAcceptCLI(t *testing.T) {
	w := newDeclareWorld(t)
	rd := httpDeclarer(w)
	ctx := callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{})
	a := invoicesArgs(w)
	a.Service, a.Namespace = "k8s:billing/Deployment/invoices", ""
	var clis []string
	for range 2 {
		raw, refused := planThenWrite(t, rd, ctx, a)
		var run struct {
			AcceptCLI string `json:"acceptCLI"`
		}
		_ = json.Unmarshal(raw, &run)
		expect(t, refused == nil, "%+v", refused)
		clis = append(clis, run.AcceptCLI)
	}
	want := "pacto declare invoices --registry " + w.host + "/acme --from-draft draft-1"
	expect(t, clis[0] == want && clis[1] == want && slices.Equal(tagsOf(t, w), []string{"draft-1"}), "%q tags %v", clis, tagsOf(t, w))
}

// TestMCPHTTPExecuteWithoutPlanPassesTheError: an execute whose plan never
// started (no scratch folder) answers the error, never a refusal.
func TestMCPHTTPExecuteWithoutPlanPassesTheError(t *testing.T) {
	w := newDeclareWorld(t)
	ctx := callerCtx("alice", newSelfReviewed(true), &bytes.Buffer{})
	a := invoicesArgs(w)
	a.IntentDigest = "sha256:" + strings.Repeat("0", 64)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	raw, refused, err := httpDeclarer(w).Execute(ctx, a)
	expect(t, raw == nil && refused == nil && err != nil, "%s %+v %v", raw, refused, err)
}

// withFleet is rd reading the caller's fleet view from mode.
func (r remoteDeclarer) withFleet(mode *pactomcp.FleetMode) remoteDeclarer {
	r.fleet = mode
	return r
}

// TestMCPHTTPClaimOnlyPlans: an HTTP server only plans a caller's claim:
// no review of the caller backs it, and a draft would go with the server's
// own push credential (the shared dashboard's rule). Execute names the pacto
// claim a person runs and pushes nothing.
func TestMCPHTTPClaimOnlyPlans(t *testing.T) {
	w := newDeclareWorld(t)
	s := newSelfReviewed(false)
	rd := httpDeclarer(w).withFleet(claimFleet(t, nil, map[fleet.ExternalKey]*fleet.ExternalClaim{"stripe": nil}))
	a := pactomcp.DeclareArgs{Service: "external:stripe", Owner: "team-platform", Name: "ledger-v2", Registry: w.host + "/acme"}
	ctx := callerCtx("alice", s, &bytes.Buffer{})
	raw, refused, err := rd.Plan(ctx, a)
	var p app.DeclarePlan
	_ = json.Unmarshal(raw, &p)
	expect(t, err == nil && refused == nil && p.State == "" && p.AcceptCLI == "" && p.Signer.Source == "none", "%s %+v %v", raw, refused, err)
	a.IntentDigest = p.IntentDigest
	raw, refused, err = rd.Execute(ctx, a)
	expect(t, err == nil && raw == nil && refused != nil && refused.Code == pactomcp.RefusalExecuteNeedsStdio && refused.CLI == a.Command(p.IntentDigest),
		"%s %+v %v", raw, refused, err)
	_, err = publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), w.host).Pull(t.Context(), w.host+"/acme/ledger-v2:draft-1")
	expect(t, err != nil && len(s.asked) == 0, "a claim pushed a draft: %v", err)
}
