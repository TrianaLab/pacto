package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// declareDemo is the demo's dashboard with its in-memory declarer, signed in
// as who.
func declareDemo(t *testing.T, who *string) http.Handler {
	t.Helper()
	q := fleet.NewQuery(demoFleet(t))
	state := func(*http.Request) dashboard.AuthState {
		return dashboard.AuthState{SignedIn: true, CSRF: "browser-demo", Method: "oidc", User: *who}
	}
	return demoHandler(dashboard.Options{
		Fleet:   func(context.Context) (*fleet.Query, error) { return q, nil },
		Auth:    &dashboard.Auth{Login: true, State: state},
		Declare: newDemoDeclarer(func(r *http.Request) string { return state(r).User }),
	})
}

// post answers one JSON POST through h into out.
func post(t *testing.T, h http.Handler, path string, in, out any) {
	t.Helper()
	body, _ := json.Marshal(in)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "browser-demo")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
}

var billingKey = string(fleet.NewServiceKey(fleet.K8sCaller("", "shop", "Deployment", "legacy-billing")))

// code is r's refusal code, "" when nothing was refused.
func code(r *dashboard.DeclareRefusal) string {
	if r == nil {
		return ""
	}
	return r.Code
}

// plan asks the demo for legacy-billing's plan with in.
func plan(t *testing.T, h http.Handler, in dashboard.DeclareInput) dashboard.DeclareAnswer {
	t.Helper()
	in.ServiceKey = billingKey
	var a dashboard.DeclareAnswer
	post(t, h, "/api/fleet/declare", in, &a)
	return a
}

// accept carries out in for legacy-billing.
func accept(t *testing.T, h http.Handler, in dashboard.DeclareAcceptInput) dashboard.DeclareAcceptAnswer {
	t.Helper()
	in.ServiceKey = billingKey
	var a dashboard.DeclareAcceptAnswer
	post(t, h, "/api/fleet/declare/accept", in, &a)
	return a
}

// TestDemoDeclarePlanRows: legacy-billing plans version 0.1.0 with no
// deploy artifact, nobody signs, and one payments-service row proposes
// ^2.1.0 that stays unknown until a person accepts it.
func TestDemoDeclarePlanRows(t *testing.T) {
	who := "alice@example.com"
	a := plan(t, declareDemo(t, &who), dashboard.DeclareInput{})
	p := a.Plan
	type view struct {
		version, deploy, mode, reason, command string
		write                                  bool
		drafts                                 int
	}
	got := view{p.Version, p.Deploy, a.Signing.Mode, a.Signing.Reason, a.Command, a.Write.Available, len(a.Drafts)}
	want := view{"0.1.0", "none", "none", demoNoSignIn, "pacto declare legacy-billing --namespace shop --registry demo.pacto.local --intent " + p.IntentDigest, true, 0}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	rows := []dashboard.DeclareRange{{Dependency: "payments-service", Compatibility: "unknown", Proposed: "^2.1.0", Observed: "2.1.0"}}
	if !slices.Equal(p.Ranges, rows) {
		t.Fatalf("ranges %+v", p.Ranges)
	}
}

// TestDemoDeclareTextsParse: the planned pacto.yaml and pacto.inferred.yaml
// are files pacto reads back, the record naming the workload and the person.
func TestDemoDeclareTextsParse(t *testing.T) {
	who := "alice@example.com"
	p := plan(t, declareDemo(t, &who), dashboard.DeclareInput{Accept: []string{"payments-service"}}).Plan
	if _, _, err := contract.ParseDocument([]byte(p.Contract.Text)); err != nil {
		t.Fatalf("pacto.yaml: %v\n%s", err, p.Contract.Text)
	}
	rec, err := contract.ParseInferredRecord([]byte(p.Record.Text))
	if err != nil {
		t.Fatalf("record: %v\n%s", err, p.Record.Text)
	}
	d := rec.Declaration
	got := []string{d.State, d.OnBehalfOf, d.InferredFrom, d.Requested[0].Range}
	if want := []string{contract.DeclarationDraft, who, "k8s:shop/Deployment/legacy-billing", "^2.1.0"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestDemoDeclareAcceptRange: a checked range is declared, moves the intent
// and the command; a range nothing proposes is refused.
func TestDemoDeclareAcceptRange(t *testing.T) {
	who := "alice@example.com"
	h := declareDemo(t, &who)
	before := plan(t, h, dashboard.DeclareInput{})
	a := plan(t, h, dashboard.DeclareInput{Accept: []string{"payments-service"}})
	r := a.Plan.Ranges[0]
	if !r.Accepted || r.Compatibility != "^2.1.0" || a.Plan.IntentDigest == before.Plan.IntentDigest {
		t.Fatalf("%+v", a.Plan)
	}
	if want := "pacto declare legacy-billing --namespace shop --registry demo.pacto.local --accept payments-service --intent " + a.Plan.IntentDigest; a.Command != want {
		t.Fatalf("command %q, want %q", a.Command, want)
	}
	if refused := plan(t, h, dashboard.DeclareInput{Accept: []string{"orders-service"}}); code(refused.Refusal) != "RangeNotProposed" || refused.Plan != nil {
		t.Fatalf("%+v", refused)
	}
}

// saveDraft plans legacy-billing with ranges and saves it as a draft.
func saveDraft(t *testing.T, h http.Handler, ranges []string) dashboard.DeclareAcceptAnswer {
	t.Helper()
	p := plan(t, h, dashboard.DeclareInput{Accept: ranges})
	return accept(t, h, dashboard.DeclareAcceptInput{Accept: ranges, IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
}

// TestDemoDeclareDraftNumbering: Save draft writes draft-1, the same draft
// again keeps draft-1, a changed one writes draft-2, and the next plan
// lists them newest first, unsigned and written for the person.
func TestDemoDeclareDraftNumbering(t *testing.T) {
	who := "alice@example.com"
	h := declareDemo(t, &who)
	first := saveDraft(t, h, []string{"payments-service"})
	if want := (dashboard.DeclareAcceptAnswer{Tag: "draft-1", Draft: true, Published: first.Published,
		Command: "pacto declare legacy-billing --registry demo.pacto.local --from-draft draft-1"}); first != want {
		t.Fatalf("got %+v, want %+v", first, want)
	}
	tags := []string{saveDraft(t, h, []string{"payments-service"}).Tag, saveDraft(t, h, nil).Tag}
	if !slices.Equal(tags, []string{"draft-1", "draft-2"}) {
		t.Fatalf("the same draft again, then a changed one: %v", tags)
	}
	a := plan(t, h, dashboard.DeclareInput{})
	if a.DraftsTotal != 2 || a.Drafts[0].Tag != "draft-2" {
		t.Fatalf("%+v", a.Drafts)
	}
	d := a.Drafts[1]
	want := dashboard.DeclareDraft{Tag: "draft-1", Digest: d.Digest, State: contract.DeclarationDraft, Asker: unsignedAsker, OnBehalfOf: who,
		Requested: []string{"payments-service ^2.1.0"}}
	if d.Tag != want.Tag || d.State != want.State || d.Asker != want.Asker || d.OnBehalfOf != who || d.Signed || !slices.Equal(d.Requested, want.Requested) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
}

// TestDemoDeclareAcceptDraft: accepting a draft loads its own plan with the
// command that accepts it, signing it is refused in the demo, a draft that
// moved is not found and a plan that moved is stale.
func TestDemoDeclareAcceptDraft(t *testing.T) {
	who := "alice@example.com"
	h := declareDemo(t, &who)
	saveDraft(t, h, []string{"payments-service"})
	ref := &dashboard.DraftRef{Tag: "draft-1", Digest: plan(t, h, dashboard.DeclareInput{}).Drafts[0].Digest}
	// A draft's requested range comes back unknown unless the person accepts it.
	d := plan(t, h, dashboard.DeclareInput{FromDraft: ref})
	if d.Plan.Ranges[0].Accepted || len(d.Plan.Unbacked) != 0 || len(d.Plan.Notes) != 2 || !strings.HasSuffix(d.Command, "--from-draft draft-1") {
		t.Fatalf("%+v %s", d.Plan, d.Command)
	}
	kept := plan(t, h, dashboard.DeclareInput{FromDraft: ref, Accept: []string{"payments-service"}})
	if !kept.Plan.Ranges[0].Accepted || !strings.HasSuffix(kept.Command, "--from-draft draft-1 --accept payments-service") {
		t.Fatalf("%+v %s", kept.Plan, kept.Command)
	}
	signed := accept(t, h, dashboard.DeclareAcceptInput{IntentDigest: d.Plan.IntentDigest, Mode: "sign", FromDraft: ref})
	moved := plan(t, h, dashboard.DeclareInput{FromDraft: &dashboard.DraftRef{Tag: "draft-1", Digest: "sha256:" + strings.Repeat("0", 64)}})
	stale := accept(t, h, dashboard.DeclareAcceptInput{IntentDigest: "sha256:" + strings.Repeat("1", 64), Mode: "draft"})
	got := []string{code(signed.Refusal), signed.Tag, code(moved.Refusal), code(stale.Refusal)}
	if want := []string{"NotAllowed", "", "DraftNotFound", "PlanStale"}; !slices.Equal(got, want) || stale.Plan == nil {
		t.Fatalf("got %v, want %v (stale plan %v)", got, want, stale.Plan)
	}
}

// TestDemoDeclarerPlansNoAcceptance: the demo plans no acceptances, so the
// accept offer signs nothing and carrying one out is refused.
func TestDemoDeclarerPlansNoAcceptance(t *testing.T) {
	d := newDemoDeclarer(func(*http.Request) string { return "" })
	if o := d.AcceptOffer(nil, ""); o.Signing.Mode != "none" || o.SignTicket != "" {
		t.Fatalf("%+v", o)
	}
	if a, err := d.AcceptRanges(nil, dashboard.AcceptTarget{}, dashboard.AcceptExecuteInput{}); err != nil || a.Refusal == nil {
		t.Fatalf("%+v %v", a, err)
	}
}
