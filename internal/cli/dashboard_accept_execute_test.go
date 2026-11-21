package cli

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// acceptHost is a declareHost whose registry also holds reports 1.6.0,
// owned by ana and proposing ledger's range, running as the Deployment
// reports-api in billing.
type acceptHost struct {
	*declareHost
	repo   string
	target dashboard.AcceptTarget
}

func newAcceptHost(t *testing.T, local bool) *acceptHost {
	t.Helper()
	h := &acceptHost{declareHost: newDeclareHost(t, local, true)}
	h.repo = h.w.host + "/acme/reports"
	(&acceptE2E{ciEnv: &ciEnv{root: t.TempDir(), host: h.w.host}, repo: h.repo}).publishReports(t, "1.6.0")
	h.target = dashboard.AcceptTarget{Key: "reports", Rev: revision("oci://"+h.repo+":1.6.0", nil),
		Runs: []dashboard.DeclareWorkload{{Name: "reports-api", Namespace: "billing", Kind: "Deployment"}}}
	return h
}

// plan is the accept plan the dashboard shows, of fromDraft when set.
func (h *acceptHost) plan(t *testing.T, fromDraft string) dashboard.AcceptPlanAnswer {
	t.Helper()
	ans, err := dashboardAcceptPlan(h.d.svc, nil)(t.Context(), "reports", h.target.Rev, fromDraft)
	if err != nil || ans.Plan == nil {
		t.Fatalf("%v %+v", err, ans)
	}
	return ans
}

func (h *acceptHost) execute(t *testing.T, in dashboard.AcceptExecuteInput) *dashboard.AcceptExecuteAnswer {
	t.Helper()
	in.ServiceKey = h.target.Key
	ans, err := h.d.AcceptRanges(h.req(), h.target, in)
	if err != nil {
		t.Fatal(err)
	}
	return ans
}

// written are the acceptance and acceptance draft tags of reports.
func (h *acceptHost) written(t *testing.T) []string {
	t.Helper()
	r, err := ocireferrer.Open(h.repo, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
	if err != nil {
		t.Fatal(err)
	}
	tags, err := ocireferrer.Tags(t.Context(), r, "pacto-accept")
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

func TestAcceptExecuteNeedsUpdate(t *testing.T) {
	h := newAcceptHost(t, false)
	h.allow = func(_, verb string) (bool, error) { return verb != "update", nil }
	p := h.plan(t, "")
	out := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.Contains(out.Refusal.Message, "accepting ranges for reports needs update") &&
		len(h.written(t)) == 0, "answer %+v", out)
	expect(t, slices.Equal(h.reviews, []string{"/billing/deployments/update/reports-api"}), "reviews %v", h.reviews)
	// Without a known workload the Pacto resource is reviewed; a service
	// running nowhere is refused before anything is asked.
	h.reviews = nil
	h.target.Runs = []dashboard.DeclareWorkload{{Name: "reports", Namespace: "billing", Kind: authn.PactoKind}}
	h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, slices.Equal(h.reviews, []string{"/billing/pactos/update/reports"}), "reviews %v", h.reviews)
	h.reviews, h.target.Runs = nil, nil
	out = h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.Contains(out.Refusal.Message, "running nowhere") && len(h.reviews) == 0,
		"answer %+v reviews %v", out, h.reviews)
	// A failed review refuses with its own code (answers are cached, so
	// another workload).
	h.target.Runs = []dashboard.DeclareWorkload{{Name: "reports-worker", Namespace: "billing", Kind: "Deployment"}}
	h.allow = func(string, string) (bool, error) { return false, errors.New("apiserver down") }
	out = h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "ReviewFailed", "answer %+v", out)
}

func TestAcceptExecuteGetOnlyRefused(t *testing.T) {
	h := newAcceptHost(t, false)
	h.target.Runs = []dashboard.DeclareWorkload{{Name: "reports", Namespace: "billing", Kind: authn.PactoKind}}
	h.allow = func(_, verb string) (bool, error) { return verb == "get" || verb == "list" || verb == "", nil }
	p := h.plan(t, "")
	out := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && len(h.written(t)) == 0, "answer %+v", out)
	expect(t, !slices.ContainsFunc(h.reviews, func(r string) bool { return !strings.Contains(r, "/update/") }), "only update is asked: %v", h.reviews)
}

func TestAcceptExecuteSignedByFreshLogin(t *testing.T) {
	h := newAcceptHost(t, false)
	h.freshLogin(t, personToken())
	p := h.plan(t, "")
	offer := h.d.AcceptOffer(h.req(), p.Plan.IntentDigest)
	expect(t, offer.Signing.Mode == "fresh-login" && offer.Signing.Identity == "ana@acme.com" && offer.Write.Available &&
		offer.SignTicket == "ticket-"+p.Plan.IntentDigest, "offer %+v", offer)
	out := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal == nil && strings.HasPrefix(out.Record, h.repo+"@sha256:") && out.Draft == "", "answer %+v", out)
	// The record attached verifies as signed by ana.
	r, err := ocireferrer.Open(h.repo, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
	if err != nil {
		t.Fatal(err)
	}
	_, dgst, _ := strings.Cut(out.Record, "@")
	desc, err := r.Resolve(t.Context(), dgst)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := content.FetchAll(t.Context(), r, desc)
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m)
	bundle, err := content.FetchAll(t.Context(), r, m.Layers[0])
	if err != nil {
		t.Fatal(err)
	}
	var who delivery.PublisherIdentity
	st, _, err := delivery.OpenAcceptance(bundle, func(b []byte) (err error) { who, _, err = signing.VerifyKeyless(b, h.ss.Root); return err })
	expect(t, err == nil && who.Subject == "ana@acme.com" && st.Predicate.Intent == p.Plan.IntentDigest, "opened %+v %+v %v", st, who, err)
	expect(t, strings.Contains(h.log.String(), "acceptance written") && strings.Contains(h.log.String(), "actor=ana@acme.com"), "audit %s", h.log)
	// The login is used once: signing again needs a fresh one.
	again := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, again.Refusal != nil && again.Refusal.Code == "SignerNotAllowed", "answer %+v", again)
}

func TestAcceptExecuteDraftWhenNoSigner(t *testing.T) {
	h := newAcceptHost(t, false)
	h.st.Method = "token"
	p := h.plan(t, "")
	offer := h.d.AcceptOffer(h.req(), p.Plan.IntentDigest)
	expect(t, offer.Signing.Mode == "none" && offer.SignTicket == "" && offer.Write.Available, "offer %+v", offer)
	out := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.Contains(out.Refusal.Command, "--intent "+p.Plan.IntentDigest), "answer %+v", out)
	out = h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal == nil && out.Draft == h.repo+":pacto-accept-draft-1" && strings.Contains(out.Command, "--draft "+out.Draft), "answer %+v", out)
	// The next plan lists the draft, unsigned and on ana's behalf.
	ds := h.plan(t, "").Plan.Drafts
	expect(t, len(ds) == 1 && ds[0].Tag == "pacto-accept-draft-1" && ds[0].OnBehalfOf == "ana@acme.com" && !ds[0].Signed && ds[0].State == "draft", "drafts %+v", ds)
	// A stale intent writes nothing.
	out = h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: "sha256:old", Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "PlanStale" && len(h.written(t)) == 1, "answer %+v", out)
	// Without a push credential the host writes nothing and says why.
	h.d.push = nil
	offer = h.d.AcceptOffer(h.req(), p.Plan.IntentDigest)
	expect(t, !offer.Write.Available && offer.Write.Reason == noPushCredential, "offer %+v", offer)
}

func TestAcceptExecuteFromDraft(t *testing.T) {
	h := newAcceptHost(t, false)
	h.st.Method = "token"
	p := h.plan(t, "")
	draft := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, draft.Draft != "", "draft %+v", draft)
	// A draft is accepted only with a person's signature.
	out := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft", FromDraft: "pacto-accept-draft-1"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed", "answer %+v", out)
	h.st.Method = "oidc"
	h.freshLogin(t, personToken())
	dp := h.plan(t, "pacto-accept-draft-1")
	expect(t, dp.Plan.Draft == "pacto-accept-draft-1" && dp.Plan.OnBehalfOf == "ana@acme.com" && dp.Plan.Drafts == nil &&
		strings.Contains(dp.Command, "--draft "+h.repo+":pacto-accept-draft-1"), "draft plan %+v %s", dp.Plan, dp.Command)
	out = h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: dp.Plan.IntentDigest, Mode: "sign", FromDraft: "pacto-accept-draft-1"})
	expect(t, out.Refusal == nil && strings.HasPrefix(out.Record, h.repo+"@sha256:"), "answer %+v", out)
	// A draft that is not there is refused, in the plan and on execution.
	missing, err := dashboardAcceptPlan(h.d.svc, nil)(t.Context(), "reports", h.target.Rev, "pacto-accept-draft-9")
	expect(t, err == nil && missing.Refusal != nil && missing.Refusal.Code == "DraftNotFound", "plan %+v %v", missing, err)
}

func TestAcceptExecuteLocalAndUnpublished(t *testing.T) {
	h := newAcceptHost(t, true)
	h.allow = func(string, string) (bool, error) { return false, nil }
	p := h.plan(t, "")
	out := h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Draft != "" && len(h.reviews) == 0, "local %+v reviews %v", out, h.reviews)
	h.target.Rev = revision("/srv/reports", nil)
	out = h.execute(t, dashboard.AcceptExecuteInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotPublished", "answer %+v", out)
	// A registry error is an error, never an empty answer.
	h.target.Rev = revision("oci://127.0.0.1:1/acme/reports:1.0.0", nil)
	if _, err := h.d.AcceptRanges(h.req(), h.target, dashboard.AcceptExecuteInput{IntentDigest: "sha256:x", Mode: "draft"}); err == nil {
		t.Error("an unreachable registry answered")
	}
}

func TestAcceptExecutePushScopedToBundleRepo(t *testing.T) {
	h := newAcceptHost(t, false)
	open := pushRepo(h.d.push, h.repo)
	other, err := open(h.w.host + "/acme/ledger")
	if err != nil {
		t.Fatal(err)
	}
	art := ocireferrer.Build(delivery.AcceptanceDraftArtifactType, delivery.AcceptanceDraftArtifactType, []byte(`{}`), nil, nil)
	err = ocireferrer.PushWriteOnce(t.Context(), other, art, "pacto-accept-draft-1")
	expect(t, errors.Is(err, oci.ErrOutsidePushScope), "a write outside the bundle repository: %v", err)
}

func TestAcceptPlanViewDrafts(t *testing.T) {
	v := acceptPlanView(&app.AcceptPlan{DraftsTotal: 3, Drafts: []app.AcceptDraftView{{Ref: "r:pacto-accept-draft-2", State: "draft",
		Asker: contract.InferredSigner{Subject: "bo@acme.com"}}}})
	expect(t, v.Draft == "" && len(v.Drafts) == 1 && v.Drafts[0].Tag == "pacto-accept-draft-2" && v.Drafts[0].Asker == "bo@acme.com" && v.DraftsTotal == 3, "view %+v", v)
}
