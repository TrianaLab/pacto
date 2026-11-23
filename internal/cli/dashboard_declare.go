package cli

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// noPushCredential is why a shared dashboard without a push credential of
// its own only plans.
const noPushCredential = "the installation gave the dashboard no push credential: run the command"

// noClaimDraft is why a shared dashboard saves no claim draft: it would go
// with the dashboard's own push credential for any signed-in viewer.
const noClaimDraft = "a shared dashboard claims only with your own signature"

// dashboardDeclarer declares inferred services for the person behind a
// dashboard request: the cluster is read as them (their own token on a shared
// host, the kubeconfig on a local one), a declared version is signed only by
// their fresh login, and every write goes through a push scope that holds the
// service's bundle repository alone, with the push credential (never the
// dashboard's read credentials on a shared host).
type dashboardDeclarer struct {
	svc      *app.Service
	id       *dashboardIdentity
	entries  func(context.Context) []app.K8sEntry
	registry string
	// push is the keychain writes go with; nil on a shared host the
	// installation gave no push credential, which then only plans.
	push gcrauthn.Keychain
	// base holds the Sigstore services a declaration signs against.
	base app.DeclareRequest
	// ticket and take are the signing login's (authn.Signer); set only
	// with one.
	ticket   func(r *http.Request, intent string, accept []string) string
	take     func(r *http.Request, intent string) (string, bool)
	accepted func(r *http.Request, intent string) []string
	live     func(r *http.Request, intent string) bool
	// trust reads the installation signer policy accepted ranges are judged
	// by, as the accept plan reads it.
	trust func(context.Context) (app.Installation, error)
}

// newDashboardDeclarer declares into --registry; nil without one. A local
// dashboard pushes with the person's own credentials, a shared one only with
// --declare-registry-config.
func newDashboardDeclarer(v *viper.Viper, svc *app.Service, id *dashboardIdentity, entries func(context.Context) []app.K8sEntry) (*dashboardDeclarer, error) {
	registry := v.GetString("dashboard.registry")
	if registry == "" {
		return nil, nil
	}
	d := &dashboardDeclarer{svc: svc, id: id, entries: entries, registry: registry}
	if id.auth.Local() {
		d.push = oci.NewKeychain(oci.EnvCredentialOptions())
	}
	if path := v.GetString("dashboard.declare-registry-config"); path != "" && !id.auth.Local() {
		kc, err := loadDockerConfig(path)
		if err != nil {
			return nil, err
		}
		d.push = kc
	}
	s := &d.base.Signing
	s.FulcioURL, s.RekorURL = v.GetString("dashboard.fulcio-url"), v.GetString("dashboard.rekor-url")
	s.TimestampURL, s.TrustedRoot = v.GetString("dashboard.timestamp-url"), v.GetString("dashboard.trusted-root")
	s.OIDCIssuer = v.GetString("dashboard.oidc-issuer-url")
	d.base.DeliveryTrust = v.GetString("dashboard.delivery-trust")
	if l := id.signing.login(); l != nil {
		d.ticket, d.take, d.accepted, d.live = l.Ticket, l.Take, l.Accepted, l.Live
	}
	return d, nil
}

// newDashboardHost is the dashboard's identity and, with --registry, its
// declarer (a nil Declarer without one).
func newDashboardHost(v *viper.Viper, host string, cluster *rest.Config, svc *app.Service, entries func(context.Context) []app.K8sEntry, trust func(context.Context) (app.Installation, error), dp deps) (*dashboardIdentity, dashboard.Declarer, error) {
	id, err := newDashboardIdentity(v, host, cluster, entries, dp)
	if err != nil {
		return nil, nil, err
	}
	d, err := newDashboardDeclarer(v, svc, id, entries)
	if d == nil {
		return id, nil, err
	}
	d.trust = trust
	return id, d, err
}

// CanWrite implements dashboard.Declarer.
func (d *dashboardDeclarer) CanWrite() bool { return d.push != nil }

// refused is a refusal answer.
func refused(code, msg, cmd string) *dashboard.DeclareRefusal {
	return &dashboard.DeclareRefusal{Code: code, Message: msg, Command: cmd}
}

// request is the declare request of w for the person behind r: the cluster
// read as them, the registry and the Sigstore services. A claim of an
// external system reads no cluster; its name defaults to the one the visible
// claim already has, so a claim again never adds a second claiming service.
func (d *dashboardDeclarer) request(r *http.Request, w dashboard.DeclareWorkload, accept []string, from *dashboard.DraftRef, claim dashboard.ClaimInput) (app.DeclareRequest, error) {
	req := d.base
	req.Service, req.Namespace, req.Registry, req.Accept = w.Name, w.Namespace, d.registry, accept
	if from != nil {
		req.FromDraft = from.Tag
	}
	cfg := d.clusterConfig(r.Context(), w.Cluster)
	if id, ok := claimOf(w); ok {
		req.Service, req.Owner, req.Name = fleet.ExternalSubjectPrefix+id, claim.Owner, cmp.Or(claim.Name, claimerName(w.View, id))
		// No workload is read: the one cluster, when there is one, gives
		// the installation signer policy a baseline is judged by.
		if cfg != nil {
			req.Dynamic, _ = d.client(r, cfg)
		}
		return req, nil
	}
	if cfg == nil {
		return req, errors.New("the dashboard reads no cluster that runs " + w.Name)
	}
	dyn, err := d.client(r, cfg)
	req.Dynamic = dyn
	return req, err
}

// client is a cluster client of cfg reading as the person behind r: their
// own token on a shared host, the kubeconfig on a local one.
func (d *dashboardDeclarer) client(r *http.Request, cfg *rest.Config) (dynamic.Interface, error) {
	if !d.id.auth.Local() {
		// The person's own token: the API server authorizes every read.
		cfg = authn.RESTConfig(cfg, d.id.state(r.Context()).Token)
	}
	return d.id.newClient(cfg)
}

// clusterConfig is the config of the cluster whose id is cluster, the only
// one when cluster is empty; nil when the dashboard reads no such cluster.
func (d *dashboardDeclarer) clusterConfig(ctx context.Context, cluster string) *rest.Config {
	var out []app.K8sEntry
	for _, e := range d.entries(ctx) {
		if e.Config != nil && e.Alias == "" && e.Conflict == "" && (cluster == "" || e.UID == cluster) {
			out = append(out, e)
		}
	}
	if len(out) != 1 {
		return nil
	}
	return out[0].Config
}

// mode is how the person behind r can sign here: with a fresh login at the
// identity provider, or not at all and why. A host that cannot write signs
// nothing either.
func (d *dashboardDeclarer) mode(r *http.Request) dashboard.DeclareSigning {
	st := d.id.state(r.Context())
	m := d.id.signing.signingMode(r.Context(), d.id.auth.Local(), st.Method, st.Person.User)
	if m.Mode == signModeFreshLogin && !d.CanWrite() {
		return dashboard.DeclareSigning{Mode: signModeNone, Reason: noPushCredential}
	}
	return m
}

// signedIn reports whether the person behind r holds a live fresh login
// for intent.
func (d *dashboardDeclarer) signedIn(r *http.Request, intent string) bool {
	return intent != "" && d.live != nil && d.live(r, intent)
}

// offer is what the person behind r can do with a plan of w here: sign,
// write, and on a shared host no claim draft.
func (d *dashboardDeclarer) offer(r *http.Request, w dashboard.DeclareWorkload) *dashboard.DeclareAnswer {
	ans := &dashboard.DeclareAnswer{Signing: d.mode(r), Write: dashboard.DeclareWrite{Available: d.CanWrite()}}
	if !ans.Write.Available {
		ans.Write.Reason = noPushCredential
	}
	if _, claim := claimOf(w); claim && !d.id.auth.Local() {
		ans.NoDraft = noClaimDraft
	}
	return ans
}

// Plan implements dashboard.Declarer.
func (d *dashboardDeclarer) Plan(r *http.Request, w dashboard.DeclareWorkload, in dashboard.DeclareInput) (*dashboard.DeclareAnswer, error) {
	ans := d.offer(r, w)
	fresh := ans.Signing.Mode == signModeFreshLogin
	if fresh && d.signedIn(r, in.IntentDigest) {
		// Back from a live signing login: the plan is the one the login was
		// started for, whatever the page asks.
		ans.SignedIn = true
		if acc := d.accepted(r, in.IntentDigest); acc != nil {
			in.Accept = acc
		}
	}
	req, err := d.request(r, w, in.Accept, in.FromDraft, in.ClaimInput)
	if err != nil {
		return nil, err
	}
	if fresh || in.FromDraft != nil {
		// The plan shows the declared version a person's login will sign:
		// the fresh one here, or theirs in the terminal for a draft.
		req.Login = plannedLogin
	} else {
		req.Draft, req.Agent = true, true
	}
	p, decl, err := d.svc.PlanDeclare(r.Context(), req)
	ans.Command = declareCommand(req, "")
	if refusal := claimRedacted(w, req, p, err); refusal != nil {
		ans.Refusal = refusal
		return ans, nil
	}
	if refusal, err := dashRefusal(err); err != nil || refusal != nil {
		ans.Refusal = refusal
		return ans, err
	}
	listed := len(p.Drafts)
	if ans.Refusal = ownView(p, w, in.FromDraft); ans.Refusal != nil {
		return ans, nil
	}
	ans.Plan, ans.Drafts = planView(p, decl), draftViews(p.Drafts)
	ans.DraftsTotal = p.DraftsTotal - (listed - len(p.Drafts))
	ans.Command = declareCommand(req, p.IntentDigest)
	if fresh && p.Unchanged == "" {
		ans.SignTicket = d.ticket(r, p.IntentDigest, in.Accept)
	}
	return ans, nil
}

// ownView keeps p to what w's own bundle repository says about w: that
// repository is keyed by name alone, so another workload with the same name
// (in a namespace the person may not see) may have written it. A baseline or
// an accepted draft of another workload refuses with nothing it holds; its
// other drafts are dropped from the list.
func ownView(p *app.DeclarePlan, w dashboard.DeclareWorkload, from *dashboard.DraftRef) *dashboard.DeclareRefusal {
	self := selfFrom(w)
	switch {
	case from != nil && p.InferredFrom != self:
		return refused("DraftNotFound", "no draft "+from.Tag+" of "+w.Name+" here", "")
	case p.Baseline != nil && foreign(p.Baseline.From, self, p.Cluster):
		return refused("NotAllowed", p.BundleRepo+" holds the declarations of another workload named "+w.Name+", so Pacto shows and writes nothing there", "")
	}
	p.Drafts = slices.DeleteFunc(p.Drafts, func(v app.DraftView) bool { return foreign(v.From, self, p.Cluster) })
	return nil
}

// claimOf is the external system w names, and whether w is one.
func claimOf(w dashboard.DeclareWorkload) (string, bool) {
	id, ok := fleet.ParseExternalSubject(w.Key)
	return string(id), ok
}

// selfFrom is the inferredFrom a declaration of w records: the external
// system a claim names, else the workload.
func selfFrom(w dashboard.DeclareWorkload) string {
	if id, ok := claimOf(w); ok {
		return contract.ExternalInferredFrom(id)
	}
	return fleet.K8sCaller("", w.Namespace, w.Kind, w.Name)
}

// claimRedacted is the one refusal a claim of w answers when the repository
// it writes to holds what the viewer's restricted view hides: it names no
// service, version, inferredFrom or tag. Nil for a workload, or when the
// viewer may read everything there.
func claimRedacted(w dashboard.DeclareWorkload, req app.DeclareRequest, p *app.DeclarePlan, err error) *dashboard.DeclareRefusal {
	id, ok := claimOf(w)
	if !ok || !claimHidden(w.View, id, cmp.Or(req.Name, inferredcontract.ServiceName(fleet.ExternalKey(id))), p, err) {
		return nil
	}
	return refused(string(pactomcp.RefusalNotAllowed), claimHiddenMessage, declareCommand(req, ""))
}

// foreign reports whether f names a workload other than from, or the same
// one in a cluster other than cluster.
func foreign(f app.Declared, from, cluster string) bool {
	return f.InferredFrom != "" && (f.InferredFrom != from || f.Cluster != "" && cluster != "" && f.Cluster != cluster)
}

// plannedLogin stands for the person's fresh login while a plan is made: a
// plan never signs, so it is never called.
func plannedLogin(context.Context) (string, error) { return "", errors.New("a plan never signs") }

// declareCommand is the pacto declare a person runs for req: accepting its
// draft with the ranges it accepts, or the plan replayed with intent.
func declareCommand(req app.DeclareRequest, intent string) string {
	cmd := "pacto declare " + req.Service + " --namespace " + req.Namespace + " --registry " + req.Registry
	if id, ok := fleet.ParseExternalSubject(req.Service); ok {
		cmd = "pacto claim " + string(id)
		if req.Owner != "" || req.FromDraft == "" {
			cmd += " --owner " + cmp.Or(req.Owner, "<team>")
		}
		if req.Name != "" {
			cmd += " --name " + req.Name
		}
		cmd += " --registry " + req.Registry
	}
	if req.FromDraft != "" {
		cmd, intent = cmd+" --from-draft "+req.FromDraft, ""
	}
	for _, a := range req.Accept {
		cmd += " --accept " + a
	}
	if intent != "" {
		cmd += " --intent " + intent
	}
	return cmd
}

// dashRefusal is err as a refusal when pacto declare refused; any other
// error is returned.
func dashRefusal(err error) (*dashboard.DeclareRefusal, error) {
	var de *app.DeclareError
	if errors.As(err, &de) {
		return refused(de.Code, de.Message, de.CLI), nil
	}
	return nil, err
}

// planView is the plan as the dashboard answers it, with its two files.
func planView(p *app.DeclarePlan, decl declarationTexts) *dashboard.DeclarePlan {
	out := &dashboard.DeclarePlan{Service: p.Service, Version: p.Version, BundleRepo: p.BundleRepo, State: p.State, InferredFrom: p.InferredFrom,
		Cluster: p.Cluster, Deploy: p.Deploy, Ranges: []dashboard.DeclareRange{}, Owner: p.Owner, MayAccept: p.MayAccept,
		Signer:     dashboard.DeclareSigner{Subject: p.Signer.Subject, Issuer: p.Signer.Issuer, Class: p.Signer.Class, Source: p.Signer.Source, Why: p.Signer.Why},
		PolicyNote: p.PolicyNote, Coverage: p.Coverage, Hidden: p.Hidden, ClusterAsOf: p.ClusterAsOf, IntentDigest: p.IntentDigest, Unchanged: p.Unchanged, Notes: p.Notes,
		Unbacked: p.Unbacked, UnbackedUnchecked: p.UnbackedUnchecked}
	for _, k := range p.Skipped {
		out.Skipped = append(out.Skipped, dashboard.DeclareSkipped(k))
	}
	if b := p.Baseline; b != nil {
		out.Baseline = &dashboard.DeclareBaseline{Version: b.Version, Bundle: b.Bundle, Signer: b.Signer}
	}
	for _, r := range p.Ranges {
		out.Ranges = append(out.Ranges, dashboard.DeclareRange(r))
	}
	if decl != nil {
		out.Contract, out.Record = dashboard.NewDeclareText(decl.Contract()), dashboard.NewDeclareText(decl.Record())
	}
	return out
}

// declarationTexts is what a plan would publish, as files.
type declarationTexts interface {
	Contract() []byte
	Record() []byte
}

// draftViews are the drafts as the dashboard answers them.
func draftViews(in []app.DraftView) []dashboard.DeclareDraft {
	out := make([]dashboard.DeclareDraft, 0, len(in))
	for _, v := range in {
		out = append(out, dashboard.DeclareDraft{Tag: v.Tag, Digest: v.Digest, State: v.State, Asker: v.Asker, OnBehalfOf: v.OnBehalfOf,
			Requested: v.Requested, Signed: v.Signed, Reason: v.Reason})
	}
	return out
}

// Accept implements dashboard.Declarer: identity, then update on the
// workload, then the plan the person reviewed (its intent digest, which
// covers an accepted draft's digest), then the repository's owner, then the
// write.
func (d *dashboardDeclarer) Accept(r *http.Request, w dashboard.DeclareWorkload, in dashboard.DeclareAcceptInput) (*dashboard.DeclareAcceptAnswer, error) {
	ctx := r.Context()
	if refusal := d.mayAccept(ctx, w, in); refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, nil
	}
	req, err := d.request(r, w, in.Accept, in.FromDraft, in.ClaimInput)
	if err != nil {
		return nil, err
	}
	req.IntentDigest = in.IntentDigest
	if refusal := d.signer(r, &req, in); refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, nil
	}
	p, decl, err := d.svc.PlanDeclare(ctx, req)
	if refusal := claimRedacted(w, req, p, err); refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, nil
	}
	if refusal, err := dashRefusal(err); err != nil || refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, err
	}
	if p.IntentDigest != in.IntentDigest {
		return d.stale(r, w, in, p)
	}
	req.Store = pushWriter(d.push, p.BundleRepo)
	who := d.id.state(ctx).Person.User
	if req.Draft {
		return writeServerDraft(ctx, d.svc, req, p, decl, who, "dashboard")
	}
	if refusal := ownedBy(p); refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, nil
	}
	if refusal := d.login(r, &req); refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, nil
	}
	return runDeclare(ctx, d.svc, req, "dashboard", who)
}

// runDeclare carries out req, which wrote nothing yet, and logs one audit
// line naming who, the service, the tag and the bundle written.
func runDeclare(ctx context.Context, svc *app.Service, req app.DeclareRequest, via, who string) (*dashboard.DeclareAcceptAnswer, error) {
	res, err := svc.Declare(ctx, req)
	if refusal, err := dashRefusal(err); err != nil || refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, err
	}
	auditDeclare(ctx, via, who, req.Service, res.Plan.Service, res.Tag, res.Published)
	return &dashboard.DeclareAcceptAnswer{Published: res.Published, Tag: res.Tag, Draft: res.Draft, Signed: res.Signed, Command: res.Plan.AcceptCLI}, nil
}

// stale is the PlanStale refusal with the fresh plan.
func (d *dashboardDeclarer) stale(r *http.Request, w dashboard.DeclareWorkload, in dashboard.DeclareAcceptInput, p *app.DeclarePlan) (*dashboard.DeclareAcceptAnswer, error) {
	fresh, err := d.Plan(r, w, dashboard.DeclareInput{ServiceKey: in.ServiceKey, Accept: in.Accept, FromDraft: in.FromDraft, ClaimInput: in.ClaimInput})
	return &dashboard.DeclareAcceptAnswer{Plan: fresh, Refusal: refused("PlanStale", "the plan changed since intent "+in.IntentDigest+
		" was reviewed; it is now "+p.IntentDigest+": review the new plan", "")}, err
}

// signer sets how req signs for mode: the person's fresh login, taken once,
// for sign; nobody, on the person's behalf, for draft. A draft is never
// accepted with a draft.
func (d *dashboardDeclarer) signer(r *http.Request, req *app.DeclareRequest, in dashboard.DeclareAcceptInput) *dashboard.DeclareRefusal {
	if in.Mode == "draft" {
		if in.FromDraft != nil {
			return refused("NotAllowed", "a draft is accepted only with a person's signature", "")
		}
		req.Draft, req.Agent, req.OnBehalfOf = true, true, d.id.state(r.Context()).Person.User
		return nil
	}
	m := d.mode(r)
	if m.Mode != signModeFreshLogin {
		return refused("NotAllowed", "you cannot sign here: "+m.Reason, declareCommand(*req, in.IntentDigest))
	}
	req.Login = plannedLogin
	return nil
}

// login takes, once, the person's fresh login for the plan they reviewed:
// only after that plan was checked, so a refused accept keeps the login.
func (d *dashboardDeclarer) login(r *http.Request, req *app.DeclareRequest) *dashboard.DeclareRefusal {
	tok, ok := d.take(r, req.IntentDigest)
	if !ok {
		return refused("SignerNotAllowed", "sign in again to sign: the signing login for this plan is missing, used or expired", "")
	}
	req.Login = func(context.Context) (string, error) { return tok, nil }
	return nil
}

// mayAccept is the per-viewer check before an accept reads the registry: a
// workload needs update on it (mayUpdate). A claim has no workload: a shared
// host only signs one, since a draft would go with the dashboard's own push
// credential for any signed-in viewer, and the owner rule the person's login
// runs before the push is its gate.
func (d *dashboardDeclarer) mayAccept(ctx context.Context, w dashboard.DeclareWorkload, in dashboard.DeclareAcceptInput) *dashboard.DeclareRefusal {
	id, ok := claimOf(w)
	switch {
	case !ok:
		return d.mayUpdate(ctx, w, "declaring "+w.Name)
	case in.Mode == "draft" && !d.id.auth.Local():
		return refused("DraftNotOffered", noClaimDraft, "pacto claim "+id+" --owner "+cmp.Or(in.Owner, "<team>")+" --draft")
	}
	return nil
}

// mayUpdate asks, on a shared host, whether the person may update the
// workload in its own cluster: the rule delivery counts approvals by. A local
// host acts with the person's own kubeconfig, which the API server
// authorizes. what names the write a refusal is about.
func (d *dashboardDeclarer) mayUpdate(ctx context.Context, w dashboard.DeclareWorkload, what string) *dashboard.DeclareRefusal {
	if d.id.auth.Local() {
		return nil
	}
	ok, err := d.id.viewer.MayUpdateWorkload(ctx, d.id.state(ctx).Person, w.Cluster, w.Namespace, w.Kind, w.Name)
	switch {
	case errors.Is(err, authn.ErrClusterNotReviewable):
		return refused("ClusterNotReviewable", "Pacto cannot review you in the cluster that runs "+w.Name+", so it writes nothing there", "")
	case err != nil:
		return refused("ReviewFailed", "asking whether you may update "+w.Kind+" "+w.Namespace+"/"+w.Name+" failed: "+err.Error(), "")
	case !ok:
		return refused("NotAllowed", what+" needs update on "+w.Kind+" "+w.Namespace+"/"+w.Name, "")
	}
	return nil
}

// ownedBy refuses a bundle repository whose baseline or drafts were inferred
// from another workload: two workloads with one name share it. A draft whose
// owner Pacto did not read (unreadable, or past the listed drafts) is no
// proof of ownership, so it refuses too.
func ownedBy(p *app.DeclarePlan) *dashboard.DeclareRefusal {
	froms := []app.Declared{}
	if p.Baseline != nil {
		froms = append(froms, p.Baseline.From)
	}
	for _, dv := range p.Drafts {
		if dv.State == "unreadable" {
			return refused("NotAllowed", "Pacto could not read who "+p.BundleRepo+":"+dv.Tag+" belongs to ("+dv.Reason+"), so it writes nothing there: run the command", "")
		}
		froms = append(froms, dv.From)
	}
	if p.DraftsTotal > len(p.Drafts) {
		return refused("NotAllowed", "Pacto read who the newest "+strconv.Itoa(len(p.Drafts))+" of the "+strconv.Itoa(p.DraftsTotal)+
			" drafts in "+p.BundleRepo+" belong to, not the rest, so it writes nothing there: run the command", "")
	}
	if i := slices.IndexFunc(froms, func(f app.Declared) bool { return foreign(f, p.InferredFrom, p.Cluster) }); i >= 0 {
		return refused("NotAllowed", p.BundleRepo+" belongs to "+froms[i].InferredFrom, "")
	}
	return nil
}

// pushWriter is the registry client a server's writes go through: the push
// keychain, scoped to repo alone.
func pushWriter(push gcrauthn.Keychain, repo string) publish.Registry {
	return publish.NewScopedClient(push, oci.NewPushScope("", repo), nil, oci.EnvInsecureRegistries()...)
}

// auditDeclare logs one declaration write: who, what and where. subject is
// the request's: a claim (external:<identity>) logs claim as its verb.
func auditDeclare(ctx context.Context, via, who, subject, service, tag, published string) {
	verb := "declare"
	if _, ok := fleet.ParseExternalSubject(subject); ok {
		verb = "claim"
	}
	logging.LoggerFromContext(ctx).Info("declaration written", slog.String("via", via), slog.String("verb", verb), slog.String("actor", who),
		slog.String("service", service), slog.String("tag", tag), slog.String("published", published))
}
