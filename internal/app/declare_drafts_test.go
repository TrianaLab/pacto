package app

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

func TestDeclareDraftFlagSkipsLogin(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	calls := e.login(personToken("ana@acme.com"))
	req := e.req("ledger")
	req.Draft = true
	res := e.declare(t, req)
	if *calls != 0 || !res.Draft || res.Signed || res.Tag != "draft-1" || res.Plan.Signer.Source != signerNone ||
		!strings.Contains(res.Plan.Signer.Why, "you asked for a draft") {
		t.Fatalf("%d %+v", *calls, res)
	}
	if _, rec := e.pulled(t, "draft-1"); rec.Declaration.State != contract.DeclarationDraft || len(rec.Declaration.Requested) != 1 {
		t.Fatalf("%s", rec.Marshal())
	}
	// A login handed in by the caller does not start for a draft either.
	req.Login = func(context.Context) (string, error) { t.Fatal("the login started"); return "", nil }
	if res := e.declare(t, req); res.Tag != "draft-1" && res.Tag != "draft-2" {
		t.Fatalf("%+v", res)
	}
}

func TestDeclareDraftWithAmbientTokenSigned(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	req := e.req()
	req.Draft = true
	res := e.declare(t, req)
	if !res.Draft || !res.Signed || res.Plan.Signer.Source != signerAmbient {
		t.Fatalf("%+v", res)
	}
	p, _ := e.plan(t, e.req())
	if len(p.Drafts) != 1 || !p.Drafts[0].Signed || p.Drafts[0].Asker.Class != contract.SignerClassCI {
		t.Fatalf("%+v", p.Drafts)
	}
}

// draftsOf writes one unsigned draft requesting ledger for ana, then tags
// it again as draft-2 to draft-n.
func (e *declareEnv) draftsOf(t *testing.T, n int) string {
	t.Helper()
	req := e.req("ledger")
	req.Agent, req.OnBehalfOf = true, "ana"
	res := e.declare(t, req)
	_, digest, _ := strings.Cut(res.Published, "@")
	for i := 2; i <= n; i++ {
		if err := e.reg.Tag(t.Context(), e.host+"/acme/invoices", digest, "draft-"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	return digest
}

func TestPlanListsDraftsNewestFirst(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	digest := e.draftsOf(t, 3)
	e.pushImage(t, e.host+"/acme/invoices", "draft-x") // no draft number: never listed
	p, _ := e.plan(t, e.req())
	tags := []string{}
	for _, d := range p.Drafts {
		tags = append(tags, d.Tag)
	}
	if !slices.Equal(tags, []string{"draft-3", "draft-2", "draft-1"}) || p.DraftsTotal != 3 {
		t.Fatalf("%v %d", tags, p.DraftsTotal)
	}
	want := DraftView{Tag: "draft-3", Digest: digest, State: contract.DeclarationDraft, OnBehalfOf: "ana", Requested: []string{"ledger ^2.3.0"},
		Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}}
	if d := p.Drafts[0]; d.Tag != want.Tag || d.Digest != want.Digest || d.State != want.State || d.OnBehalfOf != want.OnBehalfOf ||
		!slices.Equal(d.Requested, want.Requested) || d.Asker != want.Asker || d.Signed || d.Reason != "" {
		t.Fatalf("%+v", d)
	}
}

func TestPlanDraftsBounded(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.draftsOf(t, 25)
	p, _ := e.plan(t, e.req())
	if len(p.Drafts) != MaxListedDrafts || p.DraftsTotal != 25 || p.Drafts[0].Tag != "draft-25" || p.Drafts[19].Tag != "draft-6" {
		t.Fatalf("%d %d %+v", len(p.Drafts), p.DraftsTotal, p.Drafts[0])
	}
}

func TestPlanUnreadableDraftListed(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.draftsOf(t, 1)
	e.pushImage(t, e.host+"/acme/invoices", "draft-2")
	p, _ := e.plan(t, e.req())
	if len(p.Drafts) != 2 || p.Drafts[0].State != draftUnreadable || !strings.Contains(p.Drafts[0].Reason, "no readable declaration") ||
		p.Drafts[1].State != contract.DeclarationDraft {
		t.Fatalf("%+v", p.Drafts)
	}
}

func TestPlanDraftTagOrSignatureUnreadable(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	e.draftsOf(t, 1)
	pl := &declarePlanner{s: e.svc, req: e.req(), reg: failingHead{e.reg}, plan: &DeclarePlan{Service: "invoices"}}
	if v := pl.draftView(t.Context(), "draft-1", nil, nil); v.State != draftUnreadable || !strings.Contains(v.Reason, "tag could not be read: boom") {
		t.Fatalf("%+v", v)
	}
	pl.reg = e.reg
	if v := pl.draftView(t.Context(), "draft-9", nil, nil); v.State != draftUnreadable || v.Reason != "the tag could not be read" {
		t.Fatalf("%+v", v)
	}
	if v := pl.draftView(t.Context(), "draft-1", nil, errors.New("no root")); v.Signed || !strings.Contains(v.Reason, "could not be checked: no root") {
		t.Fatalf("%+v", v)
	}
	// With no root read by the baseline, the request's root is read.
	if tm, err := pl.trustedRoot(); err != nil || tm == nil {
		t.Fatal(err)
	}
	pl.root = e.ss.Root
	if tm, _ := pl.trustedRoot(); tm != e.ss.Root {
		t.Fatal("the baseline's root was not reused")
	}
}

// failingHead is a registry whose Head fails.
type failingHead struct{ publish.Registry }

func (failingHead) Head(context.Context, string) (*publish.Remote, error) {
	return nil, errors.New("boom")
}

func TestPlanVisibleHidesNamespace(t *testing.T) {
	t.Parallel()
	hidden := func(ns string) bool { return ns != "billing" }
	listedIn := func(e *declareEnv, ns string) bool {
		return slices.ContainsFunc(e.dyn.Actions(), func(a k8stesting.Action) bool {
			return a.GetVerb() == "list" && a.GetNamespace() == ns && a.GetResource() != (schema.GroupVersionResource{Version: "v1", Resource: "configmaps"})
		})
	}
	// The namespace is known: it is never listed.
	e := newDeclareEnv(t)
	req := e.req()
	req.Visible = hidden
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeWorkloadNotFound || listedIn(e, "billing") {
		t.Fatalf("%v %v", err, e.dyn.Actions())
	}
	// Known from the workload argument alone.
	e = newDeclareEnv(t)
	req = e.req()
	req.Service, req.Namespace, req.Visible = "billing/invoices", "", hidden
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeWorkloadNotFound || listedIn(e, "billing") {
		t.Fatalf("%v %v", err, e.dyn.Actions())
	}
	// No namespace known: every namespace is listed and the hidden workload
	// still reads as missing.
	e = newDeclareEnv(t)
	req = e.req()
	req.Namespace, req.Visible = "", hidden
	if _, _, err := e.svc.PlanDeclare(t.Context(), req); refusal(t, err).Code != codeWorkloadNotFound {
		t.Fatal(err)
	}
	// A visible namespace plans as before.
	e = newDeclareEnv(t)
	req = e.req()
	req.Visible = func(string) bool { return true }
	if p, _ := e.plan(t, req); p.Service != "invoices" {
		t.Fatalf("%+v", p)
	}
}

func TestDeclareRequestLoginCountsAsLogin(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	calls := e.login(personToken("bob@acme.com"))
	req := e.req()
	req.Login = func(context.Context) (string, error) { return personToken("ana@acme.com"), nil }
	p, _ := e.plan(t, req)
	if p.State != contract.DeclarationDeclared || p.Signer.Source != signerLogin {
		t.Fatalf("%+v", p)
	}
	res := e.declare(t, req)
	if *calls != 0 || res.Draft || !res.Signed || res.Plan.State != contract.DeclarationDeclared || res.Plan.Signer.Subject != "ana@acme.com" {
		t.Fatalf("%d %+v", *calls, res)
	}
	e = newDeclareEnv(t)
	req = e.req()
	req.Login = func(context.Context) (string, error) { return "", errors.New("expired") }
	if _, err := e.svc.Declare(t.Context(), req); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatal(err)
	}
}

func TestDeclareRequestLoginBeatsAmbientToken(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", personToken("mallory@acme.com"))
	req := e.req()
	req.Login = func(context.Context) (string, error) { return personToken("ana@acme.com"), nil }
	res := e.declare(t, req)
	if res.Draft || res.Plan.Signer.Source != signerLogin || res.Plan.Signer.Subject != "ana@acme.com" {
		t.Fatalf("%+v", res)
	}
	if got := e.signers(t, res.Published); len(got) != 1 || got[0].Subject != "ana@acme.com" {
		t.Fatalf("%+v", got)
	}
}

// countingStore counts the reads that reach it.
type countingStore struct {
	publish.Registry
	tags int
}

func (c *countingStore) Tags(ctx context.Context, repo string) ([]string, error) {
	c.tags++
	return c.Registry.Tags(ctx, repo)
}

func TestDeclareRequestStoreAndTexts(t *testing.T) {
	t.Parallel()
	e := newDeclareEnv(t)
	store := &countingStore{Registry: e.reg}
	req := e.req("ledger")
	req.Store = store
	_, d := e.plan(t, req)
	if store.tags == 0 {
		t.Error("the plan read past the request's store")
	}
	if !strings.Contains(string(d.Contract()), "name: invoices") || !strings.Contains(string(d.Record()), "declaration:") {
		t.Errorf("texts:\n%s\n%s", d.Contract(), d.Record())
	}
}

// TestDraftSignedOnlyByItsAsker: a draft signed by someone other than the
// asker its record names is not listed as signed, and says why.
func TestDraftSignedOnlyByItsAsker(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("SIGSTORE_ID_TOKEN", ciWorkflowToken())
	req := e.req()
	req.Draft = true
	res := e.declare(t, req)
	_, digest, _ := strings.Cut(res.Published, "@")
	pl := &declarePlanner{s: e.svc, req: e.req(), reg: e.reg, plan: &DeclarePlan{Service: "invoices"}}
	ana := contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}
	if signed, why := pl.draftSigned(t.Context(), digest, ana, e.ss.Root, nil); signed || !strings.Contains(why, "does not prove") {
		t.Fatalf("%v %q", signed, why)
	}
}

// TestClusterDraftWithOriginStaysClusterDraft: a cluster draft keeps its
// cluster input, so a service.origin never makes it a created draft: the
// CLI accepts it as before.
func TestClusterDraftWithOriginStaysClusterDraft(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	origin := "service:\n  origin:\n    ref: oci://r.io/acme/paths/go-http@sha256:" + strings.Repeat("a", 64) + "\n    digest: sha256:" + strings.Repeat("a", 64) + "\n"
	files := e.draftFiles(t, "draft-1", func(raw string, _ *contract.InferredRecord) string {
		return strings.Replace(raw, "service:\n", origin, 1)
	})
	e.publishBundle(t, e.host+"/acme/invoices", "draft-2", files, nil)
	b, rec := e.pulled(t, "draft-2")
	if b.Contract.Service.Origin == nil || createdDraft(rec, b.Contract) {
		t.Fatalf("%s", b.RawYAML)
	}
	e.login(personToken("ana@acme.com"))
	if res := e.declare(t, e.fromDraft("draft-2")); res.Tag != firstDeclaration || res.Draft {
		t.Fatalf("%+v", res)
	}
}

// TestClusterDraftAcceptUnchanged: accepting a plain cluster draft with a
// person's login publishes the first declaration exactly as before created
// drafts existed: no draft and no accept URL.
func TestClusterDraftAcceptUnchanged(t *testing.T) {
	e := newDeclareEnv(t)
	e.agentDraft(t)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.fromDraft("draft-1"))
	if res.Tag != firstDeclaration || res.Draft || res.Plan.AcceptURL != "" {
		t.Fatalf("%+v", res)
	}
}
