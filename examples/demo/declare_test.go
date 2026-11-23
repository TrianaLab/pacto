package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
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

// claimDemo is the demo's dashboard signed in as who, whose declarer hands
// each claim it signs to the fleet the way the tab refolds it: the claimed
// revisions it last got are in *claims.
func claimDemo(t *testing.T, who *string, claims *[]fleet.RawRevision) (http.Handler, *demoDeclarer) {
	t.Helper()
	q := fleet.NewQuery(demoFleet(t))
	state := func(*http.Request) dashboard.AuthState {
		return dashboard.AuthState{SignedIn: true, CSRF: "browser-demo", Method: "oidc", User: *who}
	}
	d := newDemoDeclarer(func(r *http.Request) string { return state(r).User })
	d.claimed = func(revs []fleet.RawRevision) error { *claims = revs; return nil }
	return demoHandler(dashboard.Options{
		Fleet:   func(context.Context) (*fleet.Query, error) { return q, nil },
		Auth:    &dashboard.Auth{Login: true, State: state},
		Declare: d,
	}), d
}

// unownedExternals are the UNOWNED_EXTERNAL items of snap, by entity key.
func unownedExternals(t *testing.T, snap *fleet.FleetSnapshot) []string {
	t.Helper()
	var out []string
	l, err := fleet.NewQuery(snap).Attention(fleet.AttentionFilter{Category: "unowned", Limit: fleet.MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range l.Items {
		if it.Code == "UNOWNED_EXTERNAL" {
			out = append(out, it.Entity.Key)
		}
	}
	return out
}

// TestDemoDeclarerClaimsExternal: a person claims stripe in the tab. The
// plan names the owner, the claimed repository and inferredFrom
// external:stripe, signs through the simulated fresh login and saves no
// draft; an agent signs nothing. Publishing hands the claimed revision to
// the fleet, which then links stripe to its owner and stops routing it.
func TestDemoDeclarerClaimsExternal(t *testing.T) {
	who, agent := "alice@example.com", "system:serviceaccount:agents:pay-agent"
	var claims []fleet.RawRevision
	h, _ := claimDemo(t, &who, &claims)
	const key = "external:stripe"
	ask := func(in dashboard.DeclareInput) dashboard.DeclareAnswer {
		in.ServiceKey = key
		var a dashboard.DeclareAnswer
		post(t, h, "/api/fleet/declare", in, &a)
		return a
	}
	do := func(in dashboard.DeclareAcceptInput) dashboard.DeclareAcceptAnswer {
		in.ServiceKey = key
		var a dashboard.DeclareAcceptAnswer
		post(t, h, "/api/fleet/declare/accept", in, &a)
		return a
	}
	if r := ask(dashboard.DeclareInput{}); code(r.Refusal) != "OwnerRequired" || r.Plan != nil {
		t.Fatalf("no owner: %+v", r)
	}
	team := dashboard.ClaimInput{Owner: "team-platform"}
	a := ask(dashboard.DeclareInput{ClaimInput: team})
	p := a.Plan
	got := []string{p.Service, p.Version, p.BundleRepo, p.State, p.InferredFrom, p.Owner, a.Signing.Mode, a.Signing.Identity, a.Command}
	want := []string{"stripe", "0.1.0", "demo.pacto.local/stripe", contract.DeclarationDeclared, "external:stripe", "team-platform", "fresh-login", who,
		"pacto claim stripe --owner team-platform --registry demo.pacto.local --intent " + p.IntentDigest}
	if !slices.Equal(got, want) || a.SignTicket == "" || a.SignedIn || a.NoDraft == "" || !a.Write.Available {
		t.Fatalf("got %v %+v, want %v", got, a, want)
	}
	c, _, err := contract.ParseDocument([]byte(p.Contract.Text))
	if err != nil || c.Service.External != "stripe" || c.Service.Owner.DisplayString() != "team-platform" {
		t.Fatalf("pacto.yaml %v\n%s", err, p.Contract.Text)
	}
	if rec, err := contract.ParseInferredRecord([]byte(p.Record.Text)); err != nil || rec.Declaration.InferredFrom != "external:stripe" || rec.Declaration.Asker.Subject != who {
		t.Fatalf("record %v\n%s", err, p.Record.Text)
	}
	if back := ask(dashboard.DeclareInput{ClaimInput: team, IntentDigest: p.IntentDigest}); !back.SignedIn {
		t.Fatalf("back from the simulated sign-in: %+v", back)
	}
	if r := do(dashboard.DeclareAcceptInput{ClaimInput: team, IntentDigest: p.IntentDigest, Mode: "draft"}); code(r.Refusal) != "DraftNotOffered" {
		t.Fatalf("draft: %+v", r)
	}
	if r := do(dashboard.DeclareAcceptInput{ClaimInput: team, IntentDigest: "sha256:" + strings.Repeat("1", 64), Mode: "sign"}); code(r.Refusal) != "PlanStale" || r.Plan == nil {
		t.Fatalf("stale: %+v", r)
	}
	who = agent
	if r := ask(dashboard.DeclareInput{ClaimInput: team}); r.Signing.Mode != "none" || r.SignTicket != "" || !strings.Contains(r.Signing.Reason, "agent") {
		t.Fatalf("agent plan: %+v", r)
	}
	if r := do(dashboard.DeclareAcceptInput{ClaimInput: team, IntentDigest: p.IntentDigest, Mode: "sign"}); code(r.Refusal) != "NotAllowed" || claims != nil {
		t.Fatalf("agent sign: %+v", r)
	}
	who = "alice@example.com"
	before := unownedExternals(t, demoFleet(t))
	if !slices.Contains(before, "stripe") {
		t.Fatalf("stripe is not routed before the claim: %v", before)
	}
	done := do(dashboard.DeclareAcceptInput{ClaimInput: team, IntentDigest: p.IntentDigest, Mode: "sign"})
	if done.Refusal != nil || done.Tag != "0.1.0" || !done.Signed || !strings.HasPrefix(done.Published, "demo.pacto.local/stripe@sha256:") || len(claims) != 1 {
		t.Fatalf("sign: %+v (%d claims)", done, len(claims))
	}
	partners, err := NewEmbedSource(os.DirFS("partners"))
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := buildDemoFleet(bundlesFS(t), partners, claims...)
	if err != nil {
		t.Fatal(err)
	}
	cl := snap.Externals["stripe"].Claim
	if cl == nil || cl.Owner != "team-platform" || cl.By != who || cl.Version != "0.1.0" {
		t.Fatalf("claim %+v, limitations %+v", cl, snap.Limitations)
	}
	if after := unownedExternals(t, snap); slices.Contains(after, "stripe") || len(after) != len(before)-1 {
		t.Fatalf("still routed after the claim: %v (before %v)", after, before)
	}
	if again := ask(dashboard.DeclareInput{ClaimInput: team}); again.Plan.Unchanged != "0.1.0" {
		t.Fatalf("the same claim again: %+v", again.Plan)
	}
	other := ask(dashboard.DeclareInput{ClaimInput: dashboard.ClaimInput{Owner: "team-payments"}})
	if other.Plan.Version != "0.2.0" || other.Plan.Unchanged != "" {
		t.Fatalf("a new owner: %+v", other.Plan)
	}
}

// TestDemoClaimNameMatchesInferredContract: the demo names a claim's service
// as the inferred contract of the same external system is named.
func TestDemoClaimNameMatchesInferredContract(t *testing.T) {
	for _, id := range []fleet.ExternalKey{"stripe", "aws/rds/eu-west-1/orders.db", "a/" + fleet.ExternalKey(strings.Repeat("x-", 40)), "-x_y-"} {
		if got, want := claimName(id), inferredcontract.ServiceName(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}

// TestDemoClaimEdges: a claim names its service with --name, accepting a
// plan with no owner or with nothing changed writes nothing, a fleet that
// cannot refold refuses the claim, and a tab with no fleet to refold still
// records it.
func TestDemoClaimEdges(t *testing.T) {
	const who = "alice@example.com"
	in := dashboard.ClaimInput{Owner: "team-platform", Name: "payments-stripe"}
	d := newDemoDeclarer(nil)
	a, _ := d.planClaim(who, "stripe", in, "")
	if a.Plan.Service != "payments-stripe" || a.Plan.BundleRepo != "demo.pacto.local/payments-stripe" ||
		a.Command != "pacto claim stripe --owner team-platform --name payments-stripe --registry demo.pacto.local --intent "+a.Plan.IntentDigest {
		t.Fatalf("%+v %s", a.Plan, a.Command)
	}
	if r, err := d.acceptClaim(who, "stripe", dashboard.DeclareAcceptInput{Mode: "sign"}); err != nil || code(r.Refusal) != "OwnerRequired" {
		t.Fatalf("no owner: %+v %v", r, err)
	}
	ok := dashboard.DeclareAcceptInput{Mode: "sign", IntentDigest: a.Plan.IntentDigest, ClaimInput: in}
	if r, err := d.acceptClaim(who, "stripe", ok); err != nil || r.Tag != "0.1.0" {
		t.Fatalf("no fleet to refold: %+v %v", r, err)
	}
	if r, err := d.acceptClaim(who, "stripe", ok); err != nil || code(r.Refusal) != "AlreadyDeclared" {
		t.Fatalf("nothing changed: %+v %v", r, err)
	}
	broken := newDemoDeclarer(nil)
	broken.claimed = func([]fleet.RawRevision) error { return os.ErrInvalid }
	if _, err := broken.acceptClaim(who, "stripe", ok); err != os.ErrInvalid {
		t.Fatalf("a fleet that cannot refold: %v", err)
	}
}
