package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// stripeWL is the external system stripe as a viewer whose fleet shows
// services and externals (claimFleet) resolves it.
func stripeWL(t *testing.T, services []string, claim *fleet.ExternalClaim) dashboard.DeclareWorkload {
	t.Helper()
	q, err := claimFleet(t, services, map[fleet.ExternalKey]*fleet.ExternalClaim{"stripe": claim}).Src(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return dashboard.DeclareWorkload{Key: "external:stripe", Name: "stripe", View: q}
}

// ownerPolicy makes subject the only default approver of h's owner policy.
func (h *declareHost) ownerPolicy(t *testing.T, subject string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	body := `{"defaultApprovers":[{"name":"platform-admins","issuer":"https://accounts.google.com","subject":"^` + strings.ReplaceAll(subject, ".", `\\.`) + `$"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	h.d.svc.OwnerPolicyPath = p
}

// repoTags are the version and draft tags of repo in h's registry.
func (h *declareHost) repoTags(t *testing.T, repo string) []string {
	t.Helper()
	tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), h.w.host).Tags(t.Context(), h.w.host+"/acme/"+repo)
	return slices.DeleteFunc(tags, func(t string) bool { return strings.HasPrefix(t, "sha256-") || strings.HasPrefix(t, "pacto-sig-") })
}

func (h *declareHost) claimPlan(t *testing.T, w dashboard.DeclareWorkload, in dashboard.DeclareInput) *dashboard.DeclareAnswer {
	t.Helper()
	in.ServiceKey = w.Key
	ans, err := h.d.Plan(h.req(), w, in)
	if err != nil {
		t.Fatal(err)
	}
	return ans
}

var platform = dashboard.ClaimInput{Owner: "team-platform"}

// TestDashboardClaimNoClusterStillPlans: a claim reads no cluster, so a host
// with no cluster, or several, plans it, and nothing is reviewed or read
// with the person's token.
func TestDashboardClaimNoClusterStillPlans(t *testing.T) {
	for name, clusters := range map[string][]string{"no cluster": {"none"}, "two clusters": {"uid-a", "uid-b"}} {
		h := newDeclareHost(t, false, true, clusters...)
		if name == "no cluster" {
			h.d.entries = func(context.Context) []app.K8sEntry { return nil }
		}
		ans := h.claimPlan(t, stripeWL(t, nil, nil), dashboard.DeclareInput{ClaimInput: platform})
		p := ans.Plan
		expect(t, ans.Refusal == nil && p != nil && p.InferredFrom == "external:stripe" && p.BundleRepo == h.w.host+"/acme/stripe" && p.Owner == "team:team-platform",
			"%s: %+v %+v", name, ans.Refusal, p)
		expect(t, len(h.bearers) == 0 && len(h.reviews) == 0, "%s: bearers %v reviews %v", name, h.bearers, h.reviews)
	}
}

// TestDashboardClaimCommandIsClaim: every command a claim answers is pacto
// claim, with the owner, the name and the intent the plan binds.
func TestDashboardClaimCommandIsClaim(t *testing.T) {
	h := newDeclareHost(t, true, true)
	reg := h.w.host + "/acme"
	ans := h.claimPlan(t, stripeWL(t, nil, nil), dashboard.DeclareInput{ClaimInput: dashboard.ClaimInput{Owner: "team-platform", Name: "payments-stripe"}})
	expect(t, ans.Refusal == nil && ans.NoDraft == "" && ans.Command == "pacto claim stripe --owner team-platform --name payments-stripe --registry "+reg+" --intent "+ans.Plan.IntentDigest,
		"%+v %q", ans.Refusal, ans.Command)
	refusal := h.claimPlan(t, stripeWL(t, nil, nil), dashboard.DeclareInput{})
	expect(t, refusal.Refusal != nil && refusal.Refusal.Code == "OwnerRequired" && refusal.Command == "pacto claim stripe --owner <team> --registry "+reg,
		"%+v %q", refusal.Refusal, refusal.Command)
	draft := app.DeclareRequest{Service: "external:stripe", Registry: reg, FromDraft: "draft-1"}
	expect(t, declareCommand(draft, "x") == "pacto claim stripe --registry "+reg+" --from-draft draft-1", "%q", declareCommand(draft, "x"))
}

// TestDashboardClaimSkipsWorkloadReview: signing a claim reviews no workload
// (there is none) and publishes the claimed contract signed by the person,
// with one audit line naming them and the claim.
func TestDashboardClaimSkipsWorkloadReview(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.ownerPolicy(t, "ana@acme.com")
	h.freshLogin(t, personToken())
	w := stripeWL(t, nil, nil)
	p := h.claimPlan(t, w, dashboard.DeclareInput{ClaimInput: platform})
	expect(t, p.Refusal == nil && p.Signing.Mode == "fresh-login" && p.Plan.State == contract.DeclarationDeclared && p.NoDraft == noClaimDraft, "%+v %+v", p.Refusal, p.Plan)
	out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign", ClaimInput: platform})
	expect(t, out.Refusal == nil && out.Tag == "0.1.0" && out.Signed && len(h.reviews) == 0, "%+v reviews %v", out, h.reviews)
	// The one cluster is read with the person's own token, for the signer policy only.
	expect(t, len(h.bearers) > 0 && !slices.ContainsFunc(h.bearers, func(b string) bool { return b != "https:// person-token" }), "bearers %v", h.bearers)
	expect(t, slices.Equal(h.repoTags(t, "stripe"), []string{"0.1.0"}), "tags %v", h.repoTags(t, "stripe"))
}

func TestDashboardClaimAuditNamesPerson(t *testing.T) {
	h := newDeclareHost(t, true, true)
	w := stripeWL(t, nil, nil)
	p := h.claimPlan(t, w, dashboard.DeclareInput{ClaimInput: platform})
	out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft", ClaimInput: platform})
	expect(t, out.Refusal == nil && out.Tag == "draft-1" && out.Command == "pacto claim stripe --registry "+h.w.host+"/acme --from-draft draft-1", "%+v", out)
	line := h.log.String()
	for _, want := range []string{"declaration written", "verb=claim", "actor=ana@acme.com", "service=stripe", "tag=draft-1"} {
		expect(t, strings.Contains(line, want), "audit line %q lacks %q", line, want)
	}
}

// TestDashboardClaimRoutingIsNoRight: the External is routed to the default
// approvers for routing only; a signed-in person who is none of them is
// refused by the owner rule and nothing is pushed.
func TestDashboardClaimRoutingIsNoRight(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.ownerPolicy(t, "admin@acme.com")
	h.freshLogin(t, personToken())
	w := stripeWL(t, nil, nil)
	p := h.claimPlan(t, w, dashboard.DeclareInput{ClaimInput: platform})
	expect(t, p.Plan != nil, "%+v", p.Refusal)
	out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign", ClaimInput: platform})
	expect(t, out.Refusal != nil && out.Refusal.Code == delivery.OwnerSignerNotAllowed && !out.Signed, "%+v", out)
	expect(t, len(h.repoTags(t, "stripe")) == 0, "pushed %v", h.repoTags(t, "stripe"))
}

// TestDashboardClaimSharedOffersSignOnly: a shared dashboard saves no claim
// draft with its own push credential; a local one does.
func TestDashboardClaimSharedOffersSignOnly(t *testing.T) {
	h := newDeclareHost(t, false, true)
	w := stripeWL(t, nil, nil)
	out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "draft", ClaimInput: platform})
	expect(t, out.Refusal != nil && out.Refusal.Code == "DraftNotOffered" && out.Refusal.Command == "pacto claim stripe --owner team-platform --draft", "%+v", out.Refusal)
	expect(t, len(h.repoTags(t, "stripe")) == 0, "pushed %v", h.repoTags(t, "stripe"))
	none := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "draft"})
	expect(t, none.Refusal.Command == "pacto claim stripe --owner <team> --draft", "%+v", none.Refusal)
}

// TestDashboardClaimReclaimWithBaseline: the External's own baseline and
// drafts are its own, so a claim again plans, lists them and accepts one.
func TestDashboardClaimReclaimWithBaseline(t *testing.T) {
	h := newDeclareHost(t, true, true)
	h.ownerPolicy(t, "ana@acme.com")
	w := stripeWL(t, nil, nil)
	p := h.claimPlan(t, w, dashboard.DeclareInput{ClaimInput: platform})
	draft := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft", ClaimInput: platform})
	expect(t, draft.Tag == "draft-1", "%+v", draft)
	// The person signs on a shared host, with a fresh login.
	h.d.id.auth = authn.NewOpen()
	h.freshLogin(t, personToken())
	listed := h.claimPlan(t, w, dashboard.DeclareInput{ClaimInput: platform})
	expect(t, listed.Refusal == nil && len(listed.Drafts) == 1, "%+v %+v", listed.Refusal, listed.Drafts)
	ref := &dashboard.DraftRef{Tag: "draft-1", Digest: listed.Drafts[0].Digest}
	fd := h.claimPlan(t, w, dashboard.DeclareInput{FromDraft: ref})
	expect(t, fd.Refusal == nil && fd.Plan != nil && fd.Command == "pacto claim stripe --registry "+h.w.host+"/acme --from-draft draft-1", "%+v %q", fd.Refusal, fd.Command)
	signed := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: fd.Plan.IntentDigest, Mode: "sign", FromDraft: ref})
	expect(t, signed.Refusal == nil && signed.Tag == "0.1.0", "%+v %+v", signed, signed.Refusal)
	// The published version is the claim's own baseline: a claim again is
	// judged by the signer policy (none here), never refused as another's.
	again := h.claimPlan(t, stripeWL(t, []string{"stripe"}, nil), dashboard.DeclareInput{ClaimInput: dashboard.ClaimInput{Owner: "team-payments"}})
	expect(t, again.Refusal != nil && again.Refusal.Code == "VersionConflict", "%+v", again.Refusal)
	own := &app.DeclarePlan{InferredFrom: "external:stripe", Baseline: &app.DeclareBaseline{From: app.Declared{InferredFrom: "external:stripe"}},
		Drafts: []app.DraftView{{Tag: "draft-2", From: app.Declared{InferredFrom: "external:stripe"}}, {Tag: "draft-3", From: app.Declared{InferredFrom: "external:twilio"}}}}
	expect(t, ownView(own, w, nil) == nil && len(own.Drafts) == 1 && own.Drafts[0].Tag == "draft-2", "%+v", own.Drafts)
	own.Baseline.From.InferredFrom = "k8s:billing/Deployment/stripe"
	expect(t, ownView(own, w, nil) != nil, "a workload's baseline is another's")
	// A draft that is not there is DraftNotFound, never the hidden refusal.
	gone := h.claimPlan(t, w, dashboard.DeclareInput{FromDraft: &dashboard.DraftRef{Tag: "draft-9", Digest: ref.Digest}})
	expect(t, gone.Refusal != nil && gone.Refusal.Code == "DraftNotFound", "%+v", gone.Refusal)
}

// TestDashboardClaimBaselineHiddenServiceRedacted: a claim whose repository
// holds a service the viewer may not read answers one refusal naming none of
// it, from the plan and the accept alike, and writes nothing.
func TestDashboardClaimBaselineHiddenServiceRedacted(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.ownerPolicy(t, "ana@acme.com")
	h.freshLogin(t, personToken())
	shown := stripeWL(t, nil, nil)
	p := h.claimPlan(t, shown, dashboard.DeclareInput{ClaimInput: platform})
	h.accept(t, shown, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign", ClaimInput: platform})
	// stripe 0.1.0 is now published; this view does not show the stripe service.
	hidden := h.claimPlan(t, shown, dashboard.DeclareInput{ClaimInput: platform})
	want := "the repository for this name holds a service you may not read: claim with the CLI and --name"
	expect(t, hidden.Plan == nil && hidden.Refusal != nil && hidden.Refusal.Code == "NotAllowed" && hidden.Refusal.Message == want && len(hidden.Drafts) == 0,
		"%+v %+v", hidden.Refusal, hidden.Plan)
	out := h.accept(t, shown, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", ClaimInput: dashboard.ClaimInput{Owner: "team-other"}})
	expect(t, out.Refusal != nil && out.Refusal.Message == want && !strings.Contains(out.Refusal.Command, "0.1.0"), "%+v", out.Refusal)
	expect(t, slices.Equal(h.repoTags(t, "stripe"), []string{"0.1.0"}), "tags %v", h.repoTags(t, "stripe"))
}

// TestDashboardReclaimUsesClaimedName: with no name, a claim of an External
// the viewer sees claimed targets the claiming service's repository; a
// claim they cannot see keeps the identity's name.
func TestDashboardReclaimUsesClaimedName(t *testing.T) {
	h := newDeclareHost(t, true, true)
	w := stripeWL(t, []string{"payments-stripe"}, &fleet.ExternalClaim{Service: "payments-stripe"})
	ans := h.claimPlan(t, w, dashboard.DeclareInput{ClaimInput: platform})
	expect(t, ans.Refusal == nil && ans.Plan.BundleRepo == h.w.host+"/acme/payments-stripe" && strings.Contains(ans.Command, "--name payments-stripe"), "%+v %q", ans.Refusal, ans.Command)
	hid := h.claimPlan(t, stripeWL(t, nil, &fleet.ExternalClaim{Hidden: true}), dashboard.DeclareInput{ClaimInput: platform})
	expect(t, hid.Refusal == nil && hid.Plan.BundleRepo == h.w.host+"/acme/stripe", "%+v", hid.Refusal)
	expect(t, claimerName(nil, "stripe") == "" && claimerName(w.View, "twilio") == "", "a view that lists no claim names no claimer")
	orphan := stripeWL(t, nil, &fleet.ExternalClaim{Service: "gone"})
	expect(t, claimerName(orphan.View, "stripe") == "", "a claimer the view holds no record of is named nowhere")
}

// TestDashboardClaimOffering: the dashboard claim panel's offering reaches
// the shared claim planner, which writes it as display-only metadata and
// names it in the command; an invalid slug is the OfferingInvalid refusal.
func TestDashboardClaimOffering(t *testing.T) {
	h := newDeclareHost(t, true, true)
	ans := h.claimPlan(t, stripeWL(t, []string{"ledger"}, nil), dashboard.DeclareInput{ClaimInput: dashboard.ClaimInput{Owner: "team-platform", Offering: "postgresql"}})
	expect(t, ans.Refusal == nil && ans.Plan != nil, "%+v", ans.Refusal)
	expect(t, strings.Contains(ans.Command, " --offering postgresql "), "command %q", ans.Command)
	expect(t, strings.Contains(ans.Plan.Contract.Text, fleet.ExternalOfferingMetadataKey+": postgresql") && !strings.Contains(ans.Plan.Contract.Text, "offering: postgresql\n  owner"),
		"contract %q", ans.Plan.Contract.Text)
	// The providers come from the viewer's own view, never a registry read
	// with the dashboard's credentials.
	expect(t, slices.Contains(ans.Plan.Notes, "proposes offering postgresql (no provider yet)"), "notes %v", ans.Plan.Notes)
	bad := h.claimPlan(t, stripeWL(t, nil, nil), dashboard.DeclareInput{ClaimInput: dashboard.ClaimInput{Owner: "team-platform", Offering: "Not A Slug"}})
	expect(t, bad.Refusal != nil && bad.Refusal.Code == "OfferingInvalid" && bad.Plan == nil, "%+v %+v", bad.Refusal, bad.Plan)
}

// TestDashboardClaimOfferingDraftShared: with no view (or a restricted one)
// the offering's providers are unknown, so the claim is a draft another team
// accepts; a shared dashboard refuses to sign it, in the plan and on accept,
// and pushes nothing.
func TestDashboardClaimOfferingDraftShared(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.ownerPolicy(t, "ana@acme.com")
	h.freshLogin(t, personToken())
	w := stripeWL(t, nil, nil)
	w.View = nil
	in := dashboard.ClaimInput{Owner: "team-platform", Offering: "postgresql"}
	p := h.claimPlan(t, w, dashboard.DeclareInput{ClaimInput: in})
	expect(t, p.Refusal != nil && p.Refusal.Code == "DraftNotOffered" && p.Plan == nil && strings.Contains(p.Refusal.Command, "--offering postgresql"), "%+v %+v", p.Refusal, p.Plan)
	req, err := h.d.request(h.req(), w, nil, nil, in)
	expect(t, err == nil, "%v", err)
	req.Login = plannedLogin
	plan, _, err := h.d.svc.PlanDeclare(t.Context(), req)
	expect(t, err == nil && plan.State == contract.DeclarationDraft, "%v %+v", err, plan)
	out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: plan.IntentDigest, Mode: "sign", ClaimInput: in})
	expect(t, out.Refusal != nil && out.Refusal.Code == "DraftNotOffered" && !out.Signed && !out.Draft, "%+v", out)
	expect(t, len(h.repoTags(t, "stripe")) == 0, "pushed %v", h.repoTags(t, "stripe"))
}
