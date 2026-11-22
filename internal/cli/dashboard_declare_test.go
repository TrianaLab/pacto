package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// declareHost is a dashboardDeclarer over a declareWorld: one cluster
// (uid-prod) or two (uid-prod, uid-dev), a person, and what the cluster
// clients were built with.
type declareHost struct {
	w       *declareWorld
	d       *dashboardDeclarer
	st      authn.State
	mu      sync.Mutex
	bearers []string
	reviews []string
	allow   func(cluster, verb string) (bool, error)
	ss      *testutil.Sigstore
	log     *bytes.Buffer
}

var wl = dashboard.DeclareWorkload{Key: "k8s%3Abilling%2FDeployment%2Finvoices", Name: "invoices", Namespace: "billing", Kind: "Deployment"}

// newDeclareHost is a shared host (local false) or a local one, with the
// installation push credential (the host's own keychain) when write is set.
func newDeclareHost(t *testing.T, local, write bool, clusters ...string) *declareHost {
	t.Helper()
	w := newDeclareWorld(t)
	h := &declareHost{w: w, ss: testutil.New(t), log: &bytes.Buffer{},
		st:    authn.State{SignedIn: true, Method: "oidc", Token: "person-token", Person: authn.Person{User: "ana@acme.com"}},
		allow: func(string, string) (bool, error) { return true, nil }}
	rootPath := filepath.Join(t.TempDir(), "root.json")
	if err := os.WriteFile(rootPath, h.ss.RootJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if len(clusters) == 0 {
		clusters = []string{""}
	}
	reviews := map[string]authn.Review{}
	var entries []app.K8sEntry
	for _, c := range clusters {
		entries = append(entries, app.K8sEntry{Name: c, Config: &rest.Config{Host: "https://" + c}, UID: c})
		reviews[c] = func(_ context.Context, _ authn.Person, ns string, res authn.ResourceAttributes) (bool, error) {
			h.mu.Lock()
			h.reviews = append(h.reviews, c+"/"+ns+"/"+res.Resource+"/"+res.Verb+"/"+res.Name)
			h.mu.Unlock()
			return h.allow(c, res.Verb)
		}
	}
	id := &dashboardIdentity{state: func(context.Context) authn.State { return h.st },
		viewer: authn.NewViewer(func(context.Context) map[string]authn.Review { return reviews }),
		newClient: func(cfg *rest.Config) (dynamic.Interface, error) {
			h.mu.Lock()
			h.bearers = append(h.bearers, cfg.Host+" "+cfg.BearerToken)
			h.mu.Unlock()
			return w.dyn, nil
		}}
	id.auth = authn.NewOpen()
	if local {
		id.auth = authn.NewLocal()
	}
	h.d = &dashboardDeclarer{svc: app.NewService(nil, nil), id: id, registry: w.host + "/acme",
		entries: func(context.Context) []app.K8sEntry { return entries }}
	if write {
		h.d.push = oci.NewKeychain(oci.EnvCredentialOptions())
	}
	h.d.base.Signing.FulcioURL, h.d.base.Signing.TimestampURL, h.d.base.Signing.TrustedRoot = h.ss.Fulcio, h.ss.TSA, rootPath
	return h
}

// req is a request carrying the host's logger.
func (h *declareHost) req() *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/fleet/declare", nil)
	return r.WithContext(logging.WithLogger(r.Context(), slog.New(slog.NewTextHandler(h.log, nil))))
}

// freshLogin makes the host one whose people sign with a fresh login that
// hands over tok once.
func (h *declareHost) freshLogin(t *testing.T, tok string) {
	t.Helper()
	url, _ := fakeFulcio(t, `{"issuers":[{"issuerUrl":"https://idp.example","audience":"pacto-sign"}]}`)
	s, err := newDashboardSigning(signViper(url), mustOIDC(t))
	if err != nil {
		t.Fatal(err)
	}
	h.d.id.signing = s
	taken := false
	h.d.ticket = func(_ *http.Request, intent string, _ []string) string { return "ticket-" + intent }
	h.d.take = func(*http.Request, string) (string, bool) {
		if taken {
			return "", false
		}
		taken = true
		return tok, true
	}
	h.d.accepted = func(*http.Request, string) []string { return []string{"ledger"} }
	h.d.live = func(*http.Request, string) bool { return !taken }
}

func (h *declareHost) plan(t *testing.T, in dashboard.DeclareInput) *dashboard.DeclareAnswer {
	t.Helper()
	in.ServiceKey = wl.Key
	ans, err := h.d.Plan(h.req(), wl, in)
	if err != nil || ans.Refusal != nil {
		t.Fatalf("%v %+v", err, ans)
	}
	return ans
}

func (h *declareHost) accept(t *testing.T, w dashboard.DeclareWorkload, in dashboard.DeclareAcceptInput) *dashboard.DeclareAcceptAnswer {
	t.Helper()
	in.ServiceKey = w.Key
	ans, err := h.d.Accept(h.req(), w, in)
	if err != nil {
		t.Fatal(err)
	}
	return ans
}

func (h *declareHost) tags(t *testing.T) []string {
	t.Helper()
	tags, _ := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), h.w.host).Tags(t.Context(), h.w.host+"/acme/invoices")
	// Signature tags are no versions and no drafts.
	return slices.DeleteFunc(tags, func(t string) bool { return strings.HasPrefix(t, "sha256-") || strings.HasPrefix(t, "pacto-sig-") })
}

func TestDashboardDeclarePlanShowsFilesAndDrafts(t *testing.T) {
	h := newDeclareHost(t, true, true)
	ans := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	p := ans.Plan
	expect(t, p.Service == "invoices" && p.Version == "0.1.0" && p.State == contract.DeclarationDraft, "plan %+v", p)
	expect(t, strings.Contains(p.Contract.Text, "name: invoices") && strings.Contains(p.Record.Text, "declaration"), "files %+v %+v", p.Contract, p.Record)
	expect(t, ans.Signing.Mode == "none" && ans.Signing.Reason == "run the command in your terminal to sign", "signing %+v", ans.Signing)
	expect(t, ans.Write.Available && ans.SignTicket == "", "write %+v ticket %q", ans.Write, ans.SignTicket)
	expect(t, ans.Command == "pacto declare invoices --namespace billing --registry "+h.w.host+"/acme --accept ledger --intent "+p.IntentDigest, "command %q", ans.Command)
	expect(t, slices.ContainsFunc(p.Ranges, func(r dashboard.DeclareRange) bool { return r.Dependency == "ledger" && r.Accepted }), "ranges %+v", p.Ranges)
}

// The dashboard never opens the pull request: it hands the person the
// command that does, keeping the namespace and registry and replaying no
// intent (a --pr plan binds its target into a new one).
func TestDashboardPlanPRCLI(t *testing.T) {
	h := newDeclareHost(t, true, true)
	ans := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	expect(t, ans.PRCLI == "pacto declare invoices --namespace billing --registry "+h.w.host+"/acme --accept ledger --pr", "prCli %q", ans.PRCLI)
	expect(t, !strings.Contains(ans.PRCLI, "--intent") && !strings.Contains(ans.PRCLI, "--yes"), "prCli %q", ans.PRCLI)
}

func TestDashboardDeclarePlanRefusal(t *testing.T) {
	h := newDeclareHost(t, true, true)
	ans, err := h.d.Plan(h.req(), wl, dashboard.DeclareInput{Accept: []string{"nope"}})
	expect(t, err == nil && ans.Refusal != nil && ans.Refusal.Code == "RangeNotProposed" && ans.Plan == nil, "%v %+v", err, ans)
	expect(t, ans.Command == "pacto declare invoices --namespace billing --registry "+h.w.host+"/acme --accept nope", "command %q", ans.Command)
	other := wl
	other.Cluster = "uid-elsewhere"
	if _, err := h.d.Plan(h.req(), other, dashboard.DeclareInput{}); err == nil {
		t.Error("a workload of a cluster the dashboard does not read planned")
	}
}

func TestDeclarePlanReadsWithPersonToken(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.plan(t, dashboard.DeclareInput{})
	expect(t, len(h.bearers) > 0, "no cluster client built")
	for _, b := range h.bearers {
		expect(t, b == "https:// person-token", "a cluster read went with %q", b)
	}
	// A local host reads with the kubeconfig as it is.
	h = newDeclareHost(t, true, true)
	h.plan(t, dashboard.DeclareInput{})
	expect(t, slices.Equal(slices.Compact(h.bearers), []string{"https:// "}), "local reads %v", h.bearers)
}

func TestDeclareAcceptRouteAbsentWithoutPushCredentialCLI(t *testing.T) {
	h := newDeclareHost(t, false, false)
	ans := h.plan(t, dashboard.DeclareInput{})
	expect(t, !h.d.CanWrite() && !ans.Write.Available && ans.Write.Reason == noPushCredential, "write %+v", ans.Write)
}

func TestDashboardDraftUnsignedOnBehalfOf(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal == nil && out.Tag == "draft-1" && out.Draft && !out.Signed, "answer %+v", out)
	expect(t, strings.HasSuffix(out.Command, "--from-draft draft-1"), "command %q", out.Command)
	rec := h.w.pulled(t, "draft-1")
	d := rec.Declaration
	expect(t, d.State == contract.DeclarationDraft && d.Asker.Subject == "" && d.Asker.Issuer == "none" && d.Asker.Class == "agent" && d.OnBehalfOf == "ana@acme.com",
		"declaration %+v", d)
	expect(t, slices.Equal(h.reviews, []string{"/billing/deployments/update/invoices"}), "reviews %v", h.reviews)
}

func TestDashboardDraftAuditLine(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{})
	h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	line := h.log.String()
	for _, want := range []string{"declaration written", "actor=ana@acme.com", "service=invoices", "tag=draft-1", "published=" + h.w.host + "/acme/invoices@sha256:"} {
		expect(t, strings.Contains(line, want), "audit line %q lacks %q", line, want)
	}
}

func TestServerDraftIdempotent(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	in := dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: p.Plan.IntentDigest, Mode: "draft"}
	first := h.accept(t, wl, in)
	second := h.accept(t, wl, in)
	expect(t, first.Tag == "draft-1" && second.Tag == "draft-1" && second.Published == "", "first %+v second %+v", first, second)
	expect(t, slices.Equal(h.tags(t), []string{"draft-1"}), "tags %v", h.tags(t))
	// Someone else's identical draft is theirs: a new one is written.
	h.st.Person.User = "bob@acme.com"
	third := h.accept(t, wl, in)
	expect(t, third.Tag == "draft-2", "bob's draft %+v", third)
}

func TestDashboardDraftPushScopedToBundleRepo(t *testing.T) {
	h := newDeclareHost(t, false, true)
	reg := pushWriter(h.d.push, h.w.host+"/acme/invoices")
	img, _ := random.Image(16, 1)
	_, err := reg.Write(t.Context(), h.w.host+"/acme/ledger", img)
	expect(t, errors.Is(err, oci.ErrOutsidePushScope), "a write outside the bundle repository: %v", err)
}

// countingKeychain counts what it is asked for.
type countingKeychain struct {
	mu    sync.Mutex
	hosts []string
}

func (k *countingKeychain) Resolve(r gcrauthn.Resource) (gcrauthn.Authenticator, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.hosts = append(k.hosts, r.RegistryStr())
	return gcrauthn.Anonymous, nil
}

func TestDashboardDraftPushesWithPushCredentialOnly(t *testing.T) {
	h := newDeclareHost(t, false, true)
	push := &countingKeychain{}
	h.d.push = push
	p := h.plan(t, dashboard.DeclareInput{})
	expect(t, len(push.hosts) == 0, "the plan read with the push credential: %v", push.hosts)
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Tag == "draft-1" && len(push.hosts) > 0, "the draft %+v went without the push credential (%v)", out, push.hosts)
}

func TestDashboardDeclareRefusesRepoOfOtherWorkload(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{})
	h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	// The same draft names k8s:billing/Deployment/invoices: a plan for
	// another namespace's invoices is refused before anything is written.
	pl := &app.DeclarePlan{InferredFrom: "k8s:dev/Deployment/invoices", BundleRepo: "r/invoices",
		Drafts: []app.DraftView{{Tag: "draft-1", From: app.Declared{InferredFrom: "k8s:billing/Deployment/invoices"}}}}
	r := ownedBy(pl)
	expect(t, r != nil && r.Code == "NotAllowed" && r.Message == "r/invoices belongs to k8s:billing/Deployment/invoices", "refusal %+v", r)
	pl.Drafts = nil
	pl.Baseline = &app.DeclareBaseline{From: app.Declared{InferredFrom: "k8s:dev/Deployment/invoices", Cluster: "uid-prod"}}
	pl.Cluster = "uid-dev"
	expect(t, ownedBy(pl) != nil, "another cluster's invoices owns the repository")
	pl.Cluster = "uid-prod"
	expect(t, ownedBy(pl) == nil, "the same workload owns its repository")
	rec := h.w.pulled(t, "draft-1")
	expect(t, rec.Declaration.InferredFrom == "k8s:billing/Deployment/invoices", "the draft names %q", rec.Declaration.InferredFrom)
}

func TestDeclareAcceptNeedsUpdateOnWorkload(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.allow = func(_, verb string) (bool, error) { return verb != "update", nil }
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: "sha256:x", Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && len(h.tags(t)) == 0, "answer %+v", out)
	// A local host acts with the person's own kubeconfig: no review.
	h = newDeclareHost(t, true, true)
	h.allow = func(string, string) (bool, error) { return false, nil }
	p := h.plan(t, dashboard.DeclareInput{})
	out = h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Tag == "draft-1" && len(h.reviews) == 0, "local %+v reviews %v", out, h.reviews)
}

func TestDeclareAcceptReviewsWorkloadCluster(t *testing.T) {
	h := newDeclareHost(t, false, true, "uid-prod", "uid-dev")
	dev := wl
	dev.Cluster = "uid-dev"
	pd, err := h.d.Plan(h.req(), dev, dashboard.DeclareInput{ServiceKey: dev.Key})
	if err != nil {
		t.Fatal(err)
	}
	h.accept(t, dev, dashboard.DeclareAcceptInput{IntentDigest: pd.Plan.IntentDigest, Mode: "draft"})
	expect(t, slices.Equal(h.reviews, []string{"uid-dev/billing/deployments/update/invoices"}), "reviews %v", h.reviews)
	expect(t, slices.Contains(h.bearers, "https://uid-dev person-token"), "dev read %v", h.bearers)
}

func TestDeclareAcceptReviewFailureRefuses(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.allow = func(string, string) (bool, error) { return false, errors.New("apiserver down") }
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: "sha256:x", Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "ReviewFailed" && strings.Contains(out.Refusal.Message, "apiserver down"), "answer %+v", out)
}

func TestDeclareAcceptNoReviewerRefuses(t *testing.T) {
	h := newDeclareHost(t, false, true, "uid-prod", "uid-dev")
	edge := wl
	edge.Cluster = "uid-edge"
	out := h.accept(t, edge, dashboard.DeclareAcceptInput{IntentDigest: "sha256:x", Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "ClusterNotReviewable" && len(h.tags(t)) == 0, "answer %+v", out)
}

func TestDeclareAcceptTokenSessionCannotSign(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.freshLogin(t, personToken())
	h.st.Method = "token"
	p := h.plan(t, dashboard.DeclareInput{})
	expect(t, p.Signing.Mode == "none" && strings.Contains(p.Signing.Reason, "token sign-in never signs") && p.SignTicket == "", "signing %+v", p.Signing)
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && len(h.tags(t)) == 0, "answer %+v", out)
}

func TestDeclareAcceptSignedByFreshLogin(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.freshLogin(t, personToken())
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	expect(t, p.Signing.Mode == "fresh-login" && p.Signing.Identity == "ana@acme.com" && p.SignTicket == "ticket-"+p.Plan.IntentDigest &&
		p.Plan.State == contract.DeclarationDeclared, "plan %+v %+v", p.Signing, p.Plan)
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal == nil && out.Tag == "0.1.0" && out.Signed && !out.Draft, "answer %+v", out)
	repo, digest, _ := strings.Cut(out.Published, "@")
	got, err := publish.SignersOf(t.Context(), publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), h.w.host), h.ss.Root, false, repo, digest)
	expect(t, err == nil && len(got) == 1 && got[0].Subject == "ana@acme.com", "signers %+v %v", got, err)
}

func TestSignReturnShowsFullPlan(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.freshLogin(t, personToken())
	p := h.plan(t, dashboard.DeclareInput{IntentDigest: "sha256:started", Accept: []string{}})
	expect(t, slices.ContainsFunc(p.Plan.Ranges, func(r dashboard.DeclareRange) bool { return r.Dependency == "ledger" && r.Accepted }),
		"back from the signing login the plan accepts what the ticket carried: %+v", p.Plan.Ranges)
}

func TestDeclareAcceptDraftDigestMismatchStale(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.freshLogin(t, personToken())
	p := h.plan(t, dashboard.DeclareInput{})
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: "sha256:old", Mode: "sign"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "PlanStale" && out.Plan != nil && out.Plan.Plan.IntentDigest == p.Plan.IntentDigest &&
		len(h.tags(t)) == 0, "answer %+v", out)
	// The refused accept kept the signing login: the fresh plan signs.
	out = h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal == nil && out.Tag == "0.1.0", "after PlanStale %+v", out)
}

func TestDashboardDeclareDraftThenAcceptEndToEnd(t *testing.T) {
	h := newDeclareHost(t, false, true)
	saved := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	draft := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: saved.Plan.IntentDigest, Mode: "draft"})
	expect(t, draft.Tag == "draft-1", "draft %+v", draft)
	h.freshLogin(t, personToken())
	listed := h.plan(t, dashboard.DeclareInput{})
	expect(t, len(listed.Drafts) == 1 && listed.Drafts[0].Tag == "draft-1" && listed.Drafts[0].OnBehalfOf == "ana@acme.com", "drafts %+v", listed.Drafts)
	ref := &dashboard.DraftRef{Tag: "draft-1", Digest: listed.Drafts[0].Digest}
	// A draft is never accepted by another draft.
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: "x", Mode: "draft", FromDraft: ref})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed", "draft of a draft %+v", out)
	fromDraft := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}, FromDraft: ref})
	expect(t, strings.HasSuffix(fromDraft.Command, "--from-draft draft-1 --accept ledger") && fromDraft.Plan.IntentDigest != saved.Plan.IntentDigest, "from draft %+v", fromDraft)
	signed := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: fromDraft.Plan.IntentDigest, Mode: "sign", FromDraft: ref})
	expect(t, signed.Refusal == nil && signed.Tag == "0.1.0" && signed.Signed, "accepted %+v", signed)
	expect(t, slices.Equal(slices.Sorted(slices.Values(h.tags(t))), []string{"0.1.0", "draft-1"}), "tags %v", h.tags(t))
}

func TestDeclareAcceptRefusalsReachTheAnswer(t *testing.T) {
	h := newDeclareHost(t, true, true)
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"nope"}, IntentDigest: "x", Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "RangeNotProposed", "answer %+v", out)
	other := wl
	other.Cluster = "uid-elsewhere"
	if _, err := h.d.Accept(h.req(), other, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "draft"}); err == nil {
		t.Error("a workload of a cluster the dashboard does not read was accepted")
	}
}

func TestNewDashboardDeclarer(t *testing.T) {
	v := viper.New()
	local := &dashboardIdentity{auth: authn.NewLocal()}
	d, err := newDashboardDeclarer(v, nil, local, nil)
	expect(t, d == nil && err == nil, "no registry, no declarer: %v", err)
	v.Set("dashboard.registry", "reg.io/acme")
	d, err = newDashboardDeclarer(v, nil, local, nil)
	expect(t, err == nil && d.CanWrite() && d.take == nil, "a local host writes with the person's credentials: %v", err)
	shared := &dashboardIdentity{auth: authn.NewOpen()}
	d, _ = newDashboardDeclarer(v, nil, shared, nil)
	expect(t, !d.CanWrite(), "a shared host without a push credential only plans")
	cfg := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(cfg, []byte(`{"auths":{"reg.io":{"auth":"dTpw"}}}`), 0o600)
	v.Set("dashboard.declare-registry-config", cfg)
	d, err = newDashboardDeclarer(v, nil, shared, nil)
	expect(t, err == nil && d.CanWrite(), "the push credential: %v", err)
	v.Set("dashboard.declare-registry-config", filepath.Join(t.TempDir(), "missing.json"))
	_, err = newDashboardDeclarer(v, nil, shared, nil)
	expect(t, err != nil, "a missing push credential is an error")
	url, _ := fakeFulcio(t, `{}`)
	s, _ := newDashboardSigning(signViper(url), mustOIDC(t))
	signed := &dashboardIdentity{auth: authn.NewOpen(), signing: s}
	v.Set("dashboard.declare-registry-config", "")
	d, _ = newDashboardDeclarer(v, nil, signed, nil)
	expect(t, d.take != nil && d.ticket != nil && d.accepted != nil, "the signing login's ticket and take")
}

func TestDockerConfigKeychain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(path, []byte(`{"auths":{"reg.io":{"auth":"dTpw"},"https://index.docker.io/v1/":{"username":"d","password":"p"}}}`), 0o600)
	kc, err := loadDockerConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(ref string) *gcrauthn.AuthConfig {
		r, _ := name.NewRepository(ref)
		a, _ := kc.Resolve(r)
		c, _ := a.Authorization()
		return c
	}
	expect(t, auth("reg.io/acme/x").Username == "u", "reg.io %+v", auth("reg.io/acme/x"))
	expect(t, auth("docker.io/acme/x").Username == "d", "docker.io %+v", auth("docker.io/acme/x"))
	expect(t, auth("other.io/acme/x").Username == "", "other.io is anonymous")
	_ = os.WriteFile(path, []byte(`not json`), 0o600)
	if _, err := loadDockerConfig(path); err == nil {
		t.Error("a file that is no config.json loaded")
	}
}

func TestPlanViewBaseline(t *testing.T) {
	v := planView(&app.DeclarePlan{Baseline: &app.DeclareBaseline{InferredBaseline: contract.InferredBaseline{Version: "0.1.0", Bundle: "r@sha256:a"}, Signer: "ana"}}, nil)
	expect(t, v.Baseline != nil && *v.Baseline == dashboard.DeclareBaseline{Version: "0.1.0", Bundle: "r@sha256:a", Signer: "ana"} && v.Contract.Text == "", "view %+v", v)
}

func TestDashboardDeclareRefusesRepoOfOtherWorkloadOnAccept(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{})
	h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	if err := h.w.dyn.Tracker().Add(kubeObject("apps/v1", "Deployment", "dev", "invoices", map[string]any{"spec": map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"app": "invoices"}}, "template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": "invoices"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "invoices", "image": "reg.io/acme/invoices:1.0.0"}}}}}})); err != nil {
		t.Fatal(err)
	}
	dev := wl
	dev.Namespace = "dev"
	pd, err := h.d.Plan(h.req(), dev, dashboard.DeclareInput{})
	if err != nil || pd.Plan == nil {
		t.Fatalf("%v %+v", err, pd)
	}
	out := h.accept(t, dev, dashboard.DeclareAcceptInput{IntentDigest: pd.Plan.IntentDigest, Mode: "draft"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.Contains(out.Refusal.Message, "belongs to k8s:billing/Deployment/invoices"),
		"answer %+v", out)
	expect(t, slices.Equal(h.tags(t), []string{"draft-1"}), "tags %v", h.tags(t))
	// A signed declaration is refused the same way, before the login is taken.
	h.freshLogin(t, personToken())
	pd, err = h.d.Plan(h.req(), dev, dashboard.DeclareInput{})
	if err != nil || pd.Plan == nil {
		t.Fatalf("%v %+v", err, pd)
	}
	out = h.accept(t, dev, dashboard.DeclareAcceptInput{IntentDigest: pd.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed", "signed answer %+v", out)
	_, unused := h.d.take(h.req(), pd.Plan.IntentDigest)
	expect(t, unused && slices.Equal(h.tags(t), []string{"draft-1"}), "the login was taken or something was written: %v", h.tags(t))
}

func TestDeclareAcceptFromDraftNeedsAPerson(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{})
	h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	h.freshLogin(t, testutil.CIToken("https://github.com/acme/invoices/.github/workflows/ci.yml@refs/heads/main", "https://token.actions.githubusercontent.com", "acme/invoices", "refs/heads/main"))
	listed := h.plan(t, dashboard.DeclareInput{})
	ref := &dashboard.DraftRef{Tag: "draft-1", Digest: listed.Drafts[0].Digest}
	fd := h.plan(t, dashboard.DeclareInput{FromDraft: ref})
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: fd.Plan.IntentDigest, Mode: "sign", FromDraft: ref})
	expect(t, out.Refusal != nil && out.Refusal.Code == "SignerNotAllowed" && !slices.Contains(h.tags(t), "0.1.0"), "answer %+v", out)
}

func TestSameDraftComparesContract(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{})
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	_, digest, _ := strings.Cut(out.Published, "@")
	req := app.DeclareRequest{Store: pushWriter(h.d.push, h.w.host+"/acme/invoices")}
	pl := &app.DeclarePlan{BundleRepo: h.w.host + "/acme/invoices", Drafts: []app.DraftView{{Tag: "draft-1", Digest: digest, OnBehalfOf: "ana@acme.com"}}}
	expect(t, sameDraft(t.Context(), req, pl, []byte("pactoVersion: \"2.5\"\n"), "ana@acme.com") == "", "another pacto.yaml is another draft")
	pl.Drafts[0].Digest = "sha256:" + strings.Repeat("0", 64)
	expect(t, sameDraft(t.Context(), req, pl, nil, "ana@acme.com") == "", "an unreadable draft is no match")
	_, err := plannedLogin(t.Context())
	expect(t, err != nil, "a plan never signs")
}

func TestSigningModes(t *testing.T) {
	var none *dashboardSigning
	ctx := context.Background()
	expect(t, none.signingMode(ctx, true, "oidc", "ana").Reason == "run the command in your terminal to sign", "TestSigningModeLocalIsNone")
	expect(t, none.signingMode(ctx, false, "oidc", "ana").Mode == "none", "no signing login")
	trusted, _ := fakeFulcio(t, `{"issuers":[{"issuerUrl":"https://idp.example","audience":"pacto-sign"}]}`)
	s, _ := newDashboardSigning(signViper(trusted), mustOIDC(t))
	m := s.signingMode(ctx, false, "oidc", "ana@acme.com")
	expect(t, m.Mode == "fresh-login" && m.Identity == "ana@acme.com", "TestSigningModeFreshLogin %+v", m)
	expect(t, strings.Contains(s.signingMode(ctx, false, "token", "ana").Reason, "token sign-in never signs"), "TestSigningModeTokenSessionNone")
	untrusted, _ := fakeFulcio(t, `{"issuers":[{"issuerUrl":"https://other.example","audience":"pacto-sign"}]}`)
	u, _ := newDashboardSigning(signViper(untrusted), mustOIDC(t))
	m = u.signingMode(ctx, false, "oidc", "ana")
	expect(t, m.Mode == "none" && strings.Contains(m.Reason, "Fulcio does not issue"), "TestSigningModeUntrustedIssuerNone %+v", m)
}

func TestNewDashboardHost(t *testing.T) {
	v := viper.New()
	id, d, err := newDashboardHost(v, "127.0.0.1", nil, nil, nil, nil, defaultDeps())
	expect(t, err == nil && id != nil && d == nil, "no registry: %v %v", d, err)
	v.Set("dashboard.registry", "reg.io/acme")
	_, d, err = newDashboardHost(v, "127.0.0.1", nil, nil, nil, nil, defaultDeps())
	expect(t, err == nil && d != nil && d.CanWrite(), "a local declarer: %v", err)
	_, _, err = newDashboardHost(v, "0.0.0.0", nil, nil, nil, nil, defaultDeps())
	expect(t, err != nil, "an open host without allow-anonymous is refused")
	v.Set("dashboard.allow-anonymous", true)
	v.Set("dashboard.declare-registry-config", filepath.Join(t.TempDir(), "missing.json"))
	_, _, err = newDashboardHost(v, "0.0.0.0", nil, nil, nil, nil, defaultDeps())
	expect(t, err != nil, "a missing push credential stops the dashboard")
}

func TestDeclareAcceptSignNeedsTheSigningLogin(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.freshLogin(t, personToken())
	h.d.take = func(*http.Request, string) (string, bool) { return "", false }
	p := h.plan(t, dashboard.DeclareInput{})
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "SignerNotAllowed" && len(h.tags(t)) == 0, "answer %+v", out)
}

func TestDeclareAcceptSignerMismatchStopsBeforeTag(t *testing.T) {
	h := newDeclareHost(t, false, true)
	// The login token names ana, and the Fulcio issues the certificate for mallory.
	h.freshLogin(t, testutil.TokenWith(map[string]string{"sub": "mallory@acme.com", "email": "ana@acme.com", "email_verified": "true", "iss": "https://accounts.google.com"}))
	p := h.plan(t, dashboard.DeclareInput{})
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign"})
	expect(t, out.Refusal != nil && out.Refusal.Code == "SignerUnverified" && strings.Contains(out.Refusal.Message, "nothing was tagged") && !slices.Contains(h.tags(t), "0.1.0"), "answer %+v tags %v", out, h.tags(t))
}

func TestDeclarePlanFromDraftShowsUnbacked(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{})
	draft1 := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	// A hand-built draft-2: draft-1 plus a dependency range and a publish
	// section nothing backs.
	repo := h.w.host + "/acme/invoices"
	digest := h.pushDraftVariant(t, "draft-2", func(raw []byte) []byte {
		raw, err := contract.SetCompatibility(raw, "ledger", "^9.0.0")
		if err != nil {
			t.Fatal(err)
		}
		return append(raw, "publish:\n  bundle: oci://"+repo+"\n"...)
	})
	h.freshLogin(t, personToken())
	ref := &dashboard.DraftRef{Tag: "draft-2", Digest: digest}
	fd := h.plan(t, dashboard.DeclareInput{FromDraft: ref})
	joined := strings.Join(fd.Plan.Unbacked, "\n")
	expect(t, strings.Contains(joined, "dependencies.compatibility.declared ledger: ^9.0.0") && strings.HasSuffix(joined, "\npublish") &&
		fd.Plan.UnbackedUnchecked == "", "unbacked %q (%s)", fd.Plan.Unbacked, fd.Plan.UnbackedUnchecked)
	_, d1, _ := strings.Cut(draft1.Published, "@")
	fd1 := h.plan(t, dashboard.DeclareInput{FromDraft: &dashboard.DraftRef{Tag: "draft-1", Digest: d1}})
	expect(t, len(fd1.Plan.Unbacked) == 0 && fd.Plan.IntentDigest != fd1.Plan.IntentDigest && fd.Plan.IntentDigest != p.Plan.IntentDigest,
		"the digest covers the draft: %s %s %s", fd.Plan.IntentDigest, fd1.Plan.IntentDigest, p.Plan.IntentDigest)
}

func TestDeclareAcceptFromDraftPublishes(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	draft := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	_, digest, _ := strings.Cut(draft.Published, "@")
	h.freshLogin(t, personToken())
	ref := &dashboard.DraftRef{Tag: "draft-1", Digest: digest}
	fd := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}, FromDraft: ref})
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: fd.Plan.IntentDigest, Mode: "sign", FromDraft: ref})
	expect(t, out.Refusal == nil && out.Tag == "0.1.0" && out.Signed && !out.Draft, "answer %+v", out)
	if rec := h.w.pulled(t, "0.1.0"); rec.Declaration.State != contract.DeclarationDeclared || rec.Declaration.Asker.Subject != "ana@acme.com" {
		t.Fatalf("%s", rec.Marshal())
	}
}

// pushDraftVariant pushes tag as draft-1 with its pacto.yaml edited, and
// returns its digest.
func (h *declareHost) pushDraftVariant(t *testing.T, tag string, edit func([]byte) []byte) string {
	t.Helper()
	repo := h.w.host + "/acme/invoices"
	b, err := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), h.w.host).Pull(t.Context(), repo+":draft-1")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	if err := fs.WalkDir(b.FS, ".", func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		data, err := fs.ReadFile(b.FS, path)
		files[path] = &fstest.MapFile{Data: data, Mode: 0o644}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	raw := edit(files["pacto.yaml"].Data)
	files["pacto.yaml"].Data = raw
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := oci.NewClient(gcrauthn.DefaultKeychain, oci.WithInsecureRegistries(h.w.host)).
		Push(t.Context(), repo+":"+tag, &contract.Bundle{Contract: c, RawYAML: raw, FS: files})
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// TestDeclareAcceptOwnerCheckboxUsesOwnerRule: 513-S4 takes no owner input
// on a declare request; an owner change reaches a declaration only through
// pacto.yaml (a folder, or an accepted draft). Accepting a draft that sets
// the owner runs 513-S4's owner rule, and its refusal reaches the dashboard
// answer word for word, before anything is tagged.
func TestDeclareAcceptOwnerCheckboxUsesOwnerRule(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{})
	h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	digest := h.pushDraftVariant(t, "draft-2", func(raw []byte) []byte {
		return []byte(strings.Replace(string(raw), "service:\n", "service:\n  owner:\n    team: payments\n", 1))
	})
	h.freshLogin(t, personToken())
	ref := &dashboard.DraftRef{Tag: "draft-2", Digest: digest}
	fd := h.plan(t, dashboard.DeclareInput{FromDraft: ref})
	expect(t, fd.Plan.Owner == "team:payments" && slices.ContainsFunc(fd.Plan.Notes, func(n string) bool { return strings.HasPrefix(n, "service.owner changes from") }),
		"the plan shows the owner change: %+v", fd.Plan)
	// What 513-S4's rule answers for this very request, straight from the engine.
	req, err := h.d.request(h.req(), wl, nil, ref)
	if err != nil {
		t.Fatal(err)
	}
	req.IntentDigest, req.Login = fd.Plan.IntentDigest, func(context.Context) (string, error) { return personToken(), nil }
	_, direct := h.d.svc.Declare(t.Context(), req)
	var de *app.DeclareError
	expect(t, errors.As(direct, &de) && de.Code == delivery.OwnerPolicyUnverified, "the engine refuses with an owner code: %v", direct)
	want := dashboard.DeclareRefusal{Code: de.Code, Message: de.Message, Command: de.CLI}
	out := h.accept(t, wl, dashboard.DeclareAcceptInput{IntentDigest: fd.Plan.IntentDigest, Mode: "sign", FromDraft: ref})
	expect(t, out.Refusal != nil && *out.Refusal == want && !out.Signed && !slices.Contains(h.tags(t), "0.1.0"),
		"the owner refusal reaches the answer unchanged: got %+v want %+v tags %v", out.Refusal, want, h.tags(t))
}

// addDevInvoices adds a second Deployment named invoices, in dev.
func (h *declareHost) addDevInvoices(t *testing.T) dashboard.DeclareWorkload {
	t.Helper()
	if err := h.w.dyn.Tracker().Add(kubeObject("apps/v1", "Deployment", "dev", "invoices", map[string]any{"spec": map[string]any{
		"selector": map[string]any{"matchLabels": map[string]any{"app": "invoices"}}, "template": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{"app": "invoices"}},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "invoices", "image": "reg.io/acme/invoices:1.0.0"}}}}}})); err != nil {
		t.Fatal(err)
	}
	dev := wl
	dev.Namespace = "dev"
	return dev
}

// TestDeclarePlanShowsNoOtherWorkloadsDrafts: the plan of dev/invoices lists
// none of billing/invoices' drafts from the repository they share by name,
// and accepting one of them answers as a missing draft, with no texts.
func TestDeclarePlanShowsNoOtherWorkloadsDrafts(t *testing.T) {
	h := newDeclareHost(t, false, true)
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	billing := h.plan(t, dashboard.DeclareInput{})
	dev := h.addDevInvoices(t)
	pd, err := h.d.Plan(h.req(), dev, dashboard.DeclareInput{})
	expect(t, err == nil && pd.Refusal == nil && len(pd.Drafts) == 0 && pd.DraftsTotal == 0, "%v %+v", err, pd)
	h.freshLogin(t, personToken())
	ref := &dashboard.DraftRef{Tag: "draft-1", Digest: billing.Drafts[0].Digest}
	fd, err := h.d.Plan(h.req(), dev, dashboard.DeclareInput{FromDraft: ref})
	expect(t, err == nil && fd.Plan == nil && fd.Refusal != nil && fd.Refusal.Code == "DraftNotFound", "%v %+v", err, fd)
}

// TestOwnedByNeedsEveryDraftsOwner: an unreadable draft or one past the
// listed page is no proof the repository is this workload's.
func TestOwnedByNeedsEveryDraftsOwner(t *testing.T) {
	mine := app.Declared{InferredFrom: "k8s:billing/Deployment/invoices"}
	p := &app.DeclarePlan{BundleRepo: "r/invoices", InferredFrom: mine.InferredFrom, Drafts: []app.DraftView{{Tag: "draft-2", From: mine}}, DraftsTotal: 1}
	expect(t, ownedBy(p) == nil, "an own repository is refused")
	p.DraftsTotal = 21
	expect(t, ownedBy(p) != nil && strings.Contains(ownedBy(p).Message, "newest 1 of the 21"), "an unread draft was taken as proof: %+v", ownedBy(p))
	p.DraftsTotal = 2
	p.Drafts = append(p.Drafts, app.DraftView{Tag: "draft-1", State: "unreadable", Reason: "boom"})
	expect(t, ownedBy(p) != nil && strings.Contains(ownedBy(p).Message, "draft-1"), "an unreadable draft was taken as proof: %+v", ownedBy(p))
}

// TestDeclarePlanFromDraftWhereNobodySigns: a host that cannot sign plans
// accepting a draft and names the from-draft command the person runs.
func TestDeclarePlanFromDraftWhereNobodySigns(t *testing.T) {
	h := newDeclareHost(t, true, true)
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	h.accept(t, wl, dashboard.DeclareAcceptInput{Accept: []string{"ledger"}, IntentDigest: p.Plan.IntentDigest, Mode: "draft"})
	listed := h.plan(t, dashboard.DeclareInput{})
	fd := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}, FromDraft: &dashboard.DraftRef{Tag: "draft-1", Digest: listed.Drafts[0].Digest}})
	expect(t, fd.Signing.Mode == "none" && fd.Plan != nil && strings.HasSuffix(fd.Command, "--from-draft draft-1 --accept ledger") &&
		!strings.Contains(fd.Command, "--intent"), "%+v", fd)
}

// TestDeclareSigningNeedsPushCredential: a shared host the installation gave
// no push credential signs nothing, whatever Fulcio trusts.
func TestDeclareSigningNeedsPushCredential(t *testing.T) {
	h := newDeclareHost(t, false, false)
	h.freshLogin(t, personToken())
	ans := h.plan(t, dashboard.DeclareInput{})
	expect(t, ans.Signing.Mode == "none" && ans.Signing.Reason == noPushCredential && ans.SignTicket == "", "%+v", ans)
}

// TestDeclarePlanSaysWhetherTheLoginIsLive: back from a signing login, the
// answer says whether that login is still there to sign with.
func TestDeclarePlanSaysWhetherTheLoginIsLive(t *testing.T) {
	h := newDeclareHost(t, false, true)
	h.freshLogin(t, personToken())
	p := h.plan(t, dashboard.DeclareInput{Accept: []string{"ledger"}})
	back := h.plan(t, dashboard.DeclareInput{IntentDigest: p.Plan.IntentDigest})
	expect(t, back.SignedIn && back.Plan.IntentDigest == p.Plan.IntentDigest, "%+v", back)
	h.d.take(h.req(), p.Plan.IntentDigest)
	back = h.plan(t, dashboard.DeclareInput{IntentDigest: p.Plan.IntentDigest})
	expect(t, !back.SignedIn, "a used login is live: %+v", back)
	expect(t, h.d.AcceptOffer(h.req(), p.Plan.IntentDigest).SignedIn == false, "a used login is offered")
}

func TestPlanViewCompleteness(t *testing.T) {
	v := planView(&app.DeclarePlan{Hidden: &contract.InferredHidden{Services: 2}, ClusterAsOf: "2026-11-20T10:00:00Z",
		Skipped: []app.SkippedTag{{Tag: "1.0.0", Signer: "bob", Why: "unsigned"}}}, nil)
	expect(t, v.Hidden != nil && v.Hidden.Services == 2 && v.ClusterAsOf == "2026-11-20T10:00:00Z" && len(v.Skipped) == 1 && v.Skipped[0].Tag == "1.0.0", "%+v", v)
}
