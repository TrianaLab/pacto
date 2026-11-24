package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// mcpDeclarer is pacto_declare over stdio: it reads the cluster with this
// process's kubeconfig and writes drafts with its registry credentials.
// Every MCP caller is an agent, so a run never writes a declared record, and
// a person's ambient token is never used to sign one. base holds the signing
// options; git carries a pr run's Git host requests and token helper (the
// zero value reaches the host and runs git credential fill).
type mcpDeclarer struct {
	svc  *app.Service
	base app.DeclareRequest
	git  deps
}

// newMCPDeclarer signs drafts against public-good Sigstore, reads the
// owner policy from the target snapshots trust verifies, verifies an
// inferred offering with the installation key the detection detected
// advertises and reads a template:<namespace>/<name> subject from the
// Backstage catalog at backstageURL (the server's own --backstage-url).
func newMCPDeclarer(svc *app.Service, trust, detected, backstageURL string) mcpDeclarer {
	base := app.DeclareRequest{DeliveryTrust: trust, Detected: detected, BackstageURL: backstageURL}
	base.Signing.FulcioURL, base.Signing.RekorURL = signing.PublicFulcio, signing.PublicRekor
	return mcpDeclarer{svc: svc, base: base}
}

func (m mcpDeclarer) Plan(ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	req, err := m.request(a)
	if err != nil {
		return nil, nil, err
	}
	if err := m.template(ctx, &req); err != nil {
		return declareAnswer[app.DeclarePlan](nil, err)
	}
	if req.Dynamic, err = declareDynamic(req); err != nil {
		return nil, nil, err
	}
	p, _, err := m.svc.PlanDeclare(ctx, req)
	if err == nil && req.PR {
		err = m.planPR(ctx, req, p)
	}
	if p == nil {
		return declareAnswer[app.DeclarePlan](nil, err)
	}
	return declareAnswer(&mcpPlan{p, p.PullRequest}, err)
}

// mcpPlan is a stdio plan as pacto_declare answers it: with the pull
// request pr would open, which the dashboard's plan never carries.
type mcpPlan struct {
	*app.DeclarePlan
	PullRequest *app.PRPlan `json:"pullRequest,omitempty"`
}

func (m mcpDeclarer) Execute(ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	req, err := m.request(a)
	if err != nil {
		return nil, nil, err
	}
	if err := m.template(ctx, &req); err != nil {
		return declareAnswer[app.DeclarePlan](nil, err)
	}
	if req.Dynamic, err = declareDynamic(req); err != nil {
		return nil, nil, err
	}
	if req.PR {
		res, err := m.declarePR(ctx, req)
		return declareAnswer(res, err)
	}
	res, err := m.svc.Declare(ctx, req)
	return declareAnswer(res, err)
}

// request is the agent's declare request for a.
func (m mcpDeclarer) request(a pactomcp.DeclareArgs) (app.DeclareRequest, error) {
	req := m.base
	req.Registry, req.OnBehalfOf, req.IntentDigest, req.Agent, req.Draft = a.Registry, a.OnBehalfOf, a.IntentDigest, true, a.Draft
	// A claim names an external system, never a workload: declareSubject
	// would send it to pacto claim, which is this request. It takes none of
	// a workload's arguments (ArgsRefused), and never an agent's folder.
	if _, ok := a.Claim(); ok {
		req.Service, req.Owner, req.Name, req.Offering = a.Service, a.Owner, a.Name, a.Offering
		return req, nil
	}
	// An offering acceptance reads its contribution, never a workload.
	if _, ok := a.AcceptedOffering(); ok {
		req.Service, req.Owner, req.Version, req.From = a.Service, a.Owner, a.Version, a.From
		return req, nil
	}
	req.Namespace, req.Dir, req.Accept, req.MaxItems = a.Namespace, a.Dir, a.Accept, a.MaxItems
	req.PR, req.Repository, req.Folder, req.RepoMap = a.PR, a.Repository, a.Folder, a.RepoMap
	if a.CallWindow != "" {
		w, err := time.ParseDuration(a.CallWindow)
		if err != nil {
			return req, errors.New("call_window is a duration such as 24h: " + err.Error())
		}
		req.CallWindow = w
	}
	return req, declareSubject(&req, a.Service)
}

// template reads the golden path a template:<namespace>/<name> request
// names from the server's own catalog, with the reader token of its
// environment; any other request is left as it is.
func (m mcpDeclarer) template(ctx context.Context, req *app.DeclareRequest) error {
	if req.TemplateSubject == "" {
		return nil
	}
	t, err := m.svc.InferredTemplate(ctx, req.BackstageURL, os.Getenv(backstageTokenEnv), req.TemplateSubject)
	req.Template = t
	return err
}

// declareAnswer is a plan or a run as pacto_declare's JSON, a declare
// refusal as its code, message and command.
func declareAnswer[T any](v *T, err error) (json.RawMessage, *pactomcp.Refused, error) {
	var de *app.DeclareError
	if err != nil && !errors.As(err, &de) {
		return nil, nil, err
	}
	var raw json.RawMessage
	if v != nil {
		raw, _ = json.Marshal(v) // ponytail: plans and results always marshal
	}
	if de != nil {
		return raw, &pactomcp.Refused{Code: pactomcp.WriteRefusal(de.Code), Message: de.Message, CLI: de.CLI}, nil
	}
	return raw, nil, nil
}

// remoteDeclarer is pacto_declare over HTTP: it plans with the caller's own
// exchanged cluster credentials, only against the registries the
// installation reads (its --oci references) or the one it writes to, and
// shows baseline content only to a caller the fleet view shows the service
// to. It writes only with a registry and a push credential of its own: an
// unsigned draft on the reviewed caller's behalf.
type remoteDeclarer struct {
	svc   *app.Service
	repos []string
	fleet *pactomcp.FleetMode
	// registry and push are where and with what the server writes a
	// caller's draft (--registry, --declare-registry-config); without both
	// it writes nothing.
	registry string
	push     gcrauthn.Keychain
	// detected is the detection (--detected) whose installation key
	// verifies an inferred offering, read with the caller's own client.
	detected string
	// installNS is the namespace Pacto is installed in: accepting a golden
	// path a catalog holds needs update pactos there (Decision 17).
	installNS string
}

// writes is whether the server writes drafts.
func (r remoteDeclarer) writes() bool { return r.registry != "" && r.push != nil }

func newRemoteDeclarer(svc *app.Service, ociRefs []string, fleet *pactomcp.FleetMode) remoteDeclarer {
	repos := make([]string, len(ociRefs))
	for i, ref := range ociRefs {
		repos[i] = repoOf(ref)
	}
	return remoteDeclarer{svc: svc, repos: repos, fleet: fleet}
}

// repoOf is the repository of an oci:// reference, without tag or digest.
func repoOf(ref string) string {
	ref, _, _ = strings.Cut(contract.TrimOCIScheme(ref), "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

func (r remoteDeclarer) Plan(ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	if refused := r.refused(a); refused != nil {
		return nil, refused, nil
	}
	if refused := r.offeringArgs(ctx, a); refused != nil {
		return nil, refused, nil
	}
	a, refused := r.claimArgs(ctx, a)
	if refused != nil {
		return nil, refused, nil
	}
	req, err := mcpDeclarer{}.request(a)
	if err != nil {
		return nil, nil, err
	}
	who := reviewedFrom(ctx)
	if who.own == nil {
		return nil, nil, errNobodyReviewed
	}
	if refused := r.template(ctx, who.own.Dynamic, &req); refused != nil {
		return nil, refused, nil
	}
	req.Dynamic, req.Observed = who.own.Dynamic, r.observed(ctx, req)
	r.offering(&req)
	if _, claim := a.Claim(); claim {
		// An offering's providers are read from the caller's own view, never
		// with this server's registry credentials.
		req.Fleet = r.view(ctx).SnapshotForSerialization()
	}
	p, _, err := r.svc.PlanDeclare(ctx, req)
	if p != nil {
		_, claim := a.Claim()
		_, offer := a.AcceptedOffering()
		if claim || offer || !r.writes() || a.Registry != r.registry {
			// This server signs nothing and writes no draft here: who would sign
			// is the person who runs the plan's command, whose login decides the state.
			p.Signer = app.SignerView{Class: contract.SignerClassAgent, Source: "none",
				Why: "this HTTP server writes nothing into " + a.Registry + ": a person runs the plan's " + a.Verb() + " command"}
			p.State, p.AcceptCLI = "", ""
		}
		if p, err = r.redact(ctx, a, p, err); p != nil {
			p.Notes = append(p.Notes, "a person carries out this plan on their machine with: "+a.Command(p.IntentDigest))
		}
	}
	return declareAnswer(p, err)
}

// offering sets, on an offering acceptance, the detection that verifies
// its contribution.
func (r remoteDeclarer) offering(req *app.DeclareRequest) {
	if strings.HasPrefix(req.Service, fleet.OfferingSubjectPrefix) {
		req.Detected = r.detected
	}
}

// offeringArgs refuses an offering acceptance whose from names no
// contribution the caller's fleet view lists, with one refusal that names
// nothing of it.
func (r remoteDeclarer) offeringArgs(ctx context.Context, a pactomcp.DeclareArgs) *pactomcp.Refused {
	if _, ok := a.AcceptedOffering(); !ok || listsContribution(r.view(ctx), a.From) {
		return nil
	}
	return &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "your view of the fleet lists no inferred offering at " + a.From +
		": accept the inferredOffering.ref of a row pacto_fleet_entities lists with kind offering and declaration inferred, or run pacto declare on your machine", CLI: a.Command("")}
}

// listsContribution reports whether view q (nil: one that shows nothing)
// holds a contributed revision read from ref, by its ref or its digest.
func listsContribution(q *fleet.Query, ref string) bool {
	if q == nil || ref == "" {
		return false
	}
	ref = contract.TrimOCIScheme(ref)
	snap := q.SnapshotForSerialization()
	for _, s := range snap.Services {
		if s.Contribution == nil {
			continue
		}
		for _, rk := range s.Revisions {
			rev := snap.Revisions[rk]
			if rev != nil && rev.ResolvedRef != "" && (contract.TrimOCIScheme(rev.ResolvedRef) == ref || repoOf(rev.ResolvedRef)+"@"+rev.Digest == ref) {
				return true
			}
		}
	}
	return false
}

// claimArgs is a as this server plans it: a claim only of an external
// system the caller's fleet view shows, under the name its visible claim
// already has when a names none, so a claim again never adds a second
// claiming service.
func (r remoteDeclarer) claimArgs(ctx context.Context, a pactomcp.DeclareArgs) (pactomcp.DeclareArgs, *pactomcp.Refused) {
	id, ok := a.Claim()
	if !ok {
		return a, nil
	}
	q := r.view(ctx)
	if !lists(q, id) {
		return a, &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "your view of the fleet lists no external system " + id +
			": claim one it lists, or run pacto claim on your machine", CLI: a.Command("")}
	}
	a.Name = cmp.Or(a.Name, claimerName(q, id))
	return a, nil
}

// redact is p and err as the caller reads them (hide). A claim whose
// repository holds a service, or a draft of an external system, the
// caller's view does not show is one refusal naming none of it: the name
// alone may collide with a hidden service.
func (r remoteDeclarer) redact(ctx context.Context, a pactomcp.DeclareArgs, p *app.DeclarePlan, err error) (*app.DeclarePlan, error) {
	if id, ok := a.Claim(); ok && claimHidden(r.view(ctx), id, cmp.Or(a.Name, inferredcontract.ServiceName(fleet.ExternalKey(id))), p, err) {
		return nil, &app.DeclareError{Code: string(pactomcp.RefusalNotAllowed), CLI: a.Command(""), Message: claimHiddenMessage}
	}
	if offer, ok := a.AcceptedOffering(); ok && offeringHidden(r.view(ctx), a.Registry, offer, p, err) {
		return nil, &app.DeclareError{Code: string(pactomcp.RefusalNotAllowed), CLI: a.Command(""), Message: offeringHiddenMessage}
	}
	return r.hide(ctx, p, err)
}

// claimHiddenMessage is the one refusal of a claim whose repository holds
// what the caller may not read: the name alone may collide with a hidden
// service, so it names none of it.
const claimHiddenMessage = "the repository for this name holds a service you may not read: claim with the CLI and --name"

// claimHidden reports whether the repository the claim of id under name
// writes to holds what view q (nil: one that shows nothing) hides: a
// published version, or a draft not of an external system, of a service q
// does not show, or a draft of another external system q does not list.
// Drafts of an external system the caller sees are theirs to read.
func claimHidden(q *fleet.Query, id, name string, p *app.DeclarePlan, err error) bool {
	if p == nil {
		p = &app.DeclarePlan{}
	}
	var de *app.DeclareError
	mismatch := errors.As(err, &de) && de.Code == string(pactomcp.RefusalExternalMismatch)
	held := p.Baseline != nil || len(p.Skipped) > 0 || p.DraftsTotal > len(p.Drafts) || mismatch || baselineRefusal(err)
	return slices.ContainsFunc(p.Drafts, func(d app.DraftView) bool {
		other, ok := contract.InferredFromExternal(d.From.InferredFrom)
		if ok {
			return other != id && !lists(q, other)
		}
		held = true
		return false
	}) || held && !showsService(q, name)
}

// offeringHiddenMessage is the one refusal of an offering acceptance whose
// repository holds what the caller may not read.
const offeringHiddenMessage = "the repository for this offering holds a service you may not read: accept it into another registry path, or with the CLI"

// offeringHidden reports whether the repository an acceptance of offer into
// registry writes to holds what view q (nil: one that shows nothing) does
// not show at that repository: a published version, a draft, or a refusal
// that names what it holds. The contributed service may share the name, so
// the repository's own service key decides, never the bare name.
func offeringHidden(q *fleet.Query, registry, offer string, p *app.DeclarePlan, err error) bool {
	if p == nil {
		p = &app.DeclarePlan{}
	}
	var de *app.DeclareError
	mismatch := errors.As(err, &de) && de.Code == string(pactomcp.RefusalOfferingMismatch)
	held := p.Baseline != nil || len(p.Skipped) > 0 || p.DraftsTotal > 0 || mismatch || baselineRefusal(err)
	return held && !showsService(q, string(fleet.NewServiceKeyDomain(strings.TrimSuffix(registry, "/"), offer)))
}

// lists reports whether view q lists the external system id.
func lists(q *fleet.Query, id string) bool {
	if q == nil {
		return false
	}
	_, err := q.GetExternal(id)
	return err == nil
}

// showsService reports whether view q shows service.
func showsService(q *fleet.Query, service string) bool {
	if q == nil {
		return false
	}
	_, err := q.GetService(service)
	return err == nil
}

// claimerName is the name of the service view q shows claiming the external
// system id; "" when it is unclaimed or q hides its claimer.
func claimerName(q *fleet.Query, id string) string {
	if q == nil {
		return ""
	}
	ev, err := q.GetExternal(id)
	if err != nil || ev.External.Claim == nil {
		return ""
	}
	if s := q.SnapshotForSerialization().Services[ev.External.Claim.Service]; s != nil {
		return s.Name
	}
	return ""
}

// hide is p and err as a caller reads them: for a caller the fleet view
// does not show the service to, with nothing its baseline, drafts or owner
// hold.
func (r remoteDeclarer) hide(ctx context.Context, p *app.DeclarePlan, err error) (*app.DeclarePlan, error) {
	baseline := p.Baseline != nil || len(p.Skipped) > 0 || p.DraftsTotal > 0 || baselineRefusal(err)
	if (baseline || len(p.MayAccept) > 0 || ownerRefusal(err)) && !r.shows(ctx, p.Service) {
		if baseline {
			p, err = baselineHidden(p), hiddenRefusal(p.Service, err)
		}
		p, err = ownerHidden(p), hiddenOwnerRefusal(err)
	}
	return p, err
}

// refused refuses, before any read, a plan this server cannot carry out: a
// folder of its own, a draft when it writes none, a registry it neither
// reads nor writes.
func (r remoteDeclarer) refused(a pactomcp.DeclareArgs) *pactomcp.Refused {
	switch {
	case a.PR:
		return &pactomcp.Refused{Code: pactomcp.RefusalPullRequestNeedsLocalToken, Message: "--pr runs only where your Git token is: " +
			"run the command on your machine, or pacto_declare over stdio", CLI: a.RefusalCLI(pactomcp.RefusalPullRequestNeedsLocalToken)}
	case a.RepoMap != "":
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "repo_map names a file of this server: pass it with pacto declare --repo-map " +
			"on your machine, or pacto_declare over stdio", CLI: a.Command("")}
	case a.Dir != "":
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "dir names a folder of this server: declare a folder with pacto declare --dir " +
			"on your machine, or pacto_declare over stdio", CLI: a.Command("")}
	case a.Draft && !r.writes():
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server writes no draft: save one with " + a.Verb() + " --draft " +
			"on your machine, or pacto_declare over stdio", CLI: a.Command("")}
	case !r.reads(a.Registry):
		return &pactomcp.Refused{Code: pactomcp.RefusalNoRegistry, Message: "this server plans declarations only in the registries it reads (" +
			strings.Join(r.repos, ", ") + "): run " + a.Verb() + " on your machine with your own registry credentials", CLI: a.Command("")}
	}
	return nil
}

// baselineRefusal reports whether err is a refusal built from what the
// published baseline holds: its tags and signers, publish section or
// pactoVersion, the registry read of it, or a golden path's repository
// already holding versions.
func baselineRefusal(err error) bool {
	var de *app.DeclareError
	return errors.As(err, &de) && slices.Contains([]pactomcp.WriteRefusal{pactomcp.RefusalVersionConflict, pactomcp.RefusalBundleRepoMismatch,
		pactomcp.RefusalBaselineNeedsMigration, pactomcp.RefusalRegistryUnreadable, pactomcp.RefusalTemplateNameTaken}, pactomcp.WriteRefusal(de.Code))
}

// hiddenRefusal is err for a caller who may not see service: a baseline
// refusal keeps its code and command, and names nothing the baseline holds.
func hiddenRefusal(service string, err error) error {
	var de *app.DeclareError
	if !baselineRefusal(err) || !errors.As(err, &de) {
		return err
	}
	return &app.DeclareError{Code: de.Code, CLI: de.CLI,
		Message: "the published baseline of " + service + " is hidden: you may not read it in the fleet; run pacto declare on your machine"}
}

// ownerRefusal reports whether err is a refusal of the owner rule.
func ownerRefusal(err error) bool {
	var de *app.DeclareError
	return errors.As(err, &de) && slices.Contains(delivery.OwnerRightCodes, de.Code)
}

// ownerHidden is p for a caller who may not see its service: who may set
// its owner, and every note that names an owner, dropped.
func ownerHidden(p *app.DeclarePlan) *app.DeclarePlan {
	p.MayAccept = nil
	p.Notes = slices.DeleteFunc(p.Notes, func(n string) bool { return strings.Contains(n, "service.owner") })
	return p
}

// hiddenOwnerRefusal is err for a caller who may not see the service: an
// owner refusal names no team, DRI, file, commit or repository.
func hiddenOwnerRefusal(err error) error {
	var de *app.DeclareError
	if !ownerRefusal(err) || !errors.As(err, &de) {
		return err
	}
	return &app.DeclareError{Code: delivery.OwnerSignerNotAllowed, Message: "you may not set the owner of this service", CLI: de.CLI}
}

// plansOnly is the refusal of an Execute this server only plans: a claim
// and an offering acceptance (whose drafts would go with the server's own
// push credential for any caller), and any declaration on a server that
// writes none. Nil when the server may write the draft.
func (r remoteDeclarer) plansOnly(ctx context.Context, a pactomcp.DeclareArgs) *pactomcp.Refused {
	if _, claim := a.Claim(); claim {
		a, refused := r.claimArgs(ctx, a)
		if refused != nil {
			return refused
		}
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server only plans a claim: a draft would go with its own push " +
			"credential for any caller; run the command on your machine, where adding --yes publishes the claim signed by you", CLI: a.Command(a.IntentDigest)}
	}
	if _, offer := a.AcceptedOffering(); offer {
		if refused := r.offeringArgs(ctx, a); refused != nil {
			return refused
		}
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server only plans an offering acceptance: a draft would go " +
			"with its own push credential for any caller; run the command on your machine, where adding --yes publishes it signed by you", CLI: a.Command(a.IntentDigest)}
	}
	if !r.writes() {
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server writes no declaration: run the command on your machine; " +
			"it prints the same plan, and adding --yes publishes it signed by you", CLI: a.Command(a.IntentDigest)}
	}
	return nil
}

// Execute writes the caller's draft when the server has a registry and a
// push credential: into that registry alone, unsigned, on behalf of the
// caller the apiserver reviewed, who must be allowed to update the workload
// (a SelfSubjectAccessReview with their own credentials), through the
// dashboard's writer. Without them it names the pacto declare a person
// runs, replaying every argument the intent digest binds, without --yes.
// A claim is never written here: no review of the caller backs it, and the
// draft would go with the server's own push credential (the shared
// dashboard's rule), so it names the pacto claim a person runs. Nor is an
// offering acceptance: no review of the caller on its source object backs
// it either, so it names the pacto declare a person runs.
func (r remoteDeclarer) Execute(ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	if refused := r.plansOnly(ctx, a); refused != nil {
		return nil, refused, nil
	}
	who := reviewedFrom(ctx)
	switch {
	case a.Registry != r.registry:
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalNoRegistry, Message: "this server writes drafts only into " + r.registry + ": plan again with that registry",
			CLI: a.Command(a.IntentDigest)}, nil
	case who.own == nil:
		return nil, nil, errNobodyReviewed
	case a.OnBehalfOf != "" && a.OnBehalfOf != who.person.User:
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "this server writes a draft only on behalf of the caller it reviewed, " +
			who.person.User + ", never " + a.OnBehalfOf}, nil
	}
	req, err := mcpDeclarer{}.request(a)
	if err != nil {
		return nil, nil, err
	}
	if refused := r.template(ctx, who.own.Dynamic, &req); refused != nil {
		return nil, refused, nil
	}
	req.Draft, req.OnBehalfOf, req.Dynamic, req.Observed = true, who.person.User, who.own.Dynamic, r.observed(ctx, req)
	r.offering(&req)
	p, decl, err := r.svc.PlanDeclare(ctx, req)
	if p == nil {
		p = &app.DeclarePlan{Service: req.Service}
	}
	// Execute may come without a plan, or the baseline may move since it:
	// a refusal reaches the caller only as Plan would show it.
	if _, err = r.redact(ctx, a, p, err); err != nil {
		return declareAnswer[app.DeclarePlan](nil, err)
	}
	if p.IntentDigest != a.IntentDigest {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalPlanStale, Message: "the plan changed since intent " + a.IntentDigest + " was reviewed; it is now " +
			p.IntentDigest + ": review the new plan"}, nil
	}
	if req.Template == nil {
		// A golden path's right was asked before the plan.
		if refused := mayUpdateSelf(ctx, who.own.Dynamic, p.InferredFrom); refused != nil {
			return nil, refused, nil
		}
	}
	req.Store = pushWriter(r.push, p.BundleRepo)
	return mcpWritten(writeServerDraft(ctx, r.svc, req, p, decl, who.person.User, "mcp"))
}

// template is, for a template:<namespace>/<name> request, the golden path
// the caller's fleet view lists, set on req after the caller passed
// Decision 17 (update pactos in the installation namespace, asked with
// their own client dyn); a refusal when they may not, or when the view lists
// it from a catalog the installation did not configure or under a name a
// golden path or service already holds, or when the view cannot tell
// whether the catalog holds it. A template a complete view does not list is
// left unset: the planner refuses it as not found.
func (r remoteDeclarer) template(ctx context.Context, dyn dynamic.Interface, req *app.DeclareRequest) *pactomcp.Refused {
	if req.TemplateSubject == "" {
		return nil
	}
	if refused := mayUpdatePactos(ctx, dyn, cmp.Or(r.installNS, delivery.PolicyNamespace)); refused != nil {
		return refused
	}
	if q := r.view(ctx); q != nil {
		t, known := q.InferredTemplate(req.TemplateSubject)
		if t == nil && !known {
			return &pactomcp.Refused{Code: pactomcp.RefusalTemplateCatalogUnavailable,
				Message: "the installation's catalog could not be read whole, so Pacto cannot tell whether it holds " + req.TemplateSubject + ": try again once pacto_fleet_entities reads it complete"}
		}
		req.Template = t
	}
	if req.Template == nil {
		return nil
	}
	if refusal := templateRefusal(req.Template, req.TemplateSubject); refusal != nil {
		return &pactomcp.Refused{Code: pactomcp.WriteRefusal(refusal.Code), Message: refusal.Message}
	}
	return nil
}

// mayUpdatePactos refuses a caller the apiserver does not allow to update
// pactos in ns: a SelfSubjectAccessReview with the caller's own client dyn.
// A review that failed or could not be evaluated refuses too.
func mayUpdatePactos(ctx context.Context, dyn dynamic.Interface, ns string) *pactomcp.Refused {
	res := authn.PactoResource
	review := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": map[string]any{"namespace": ns, "verb": "update", "group": res.Group, "resource": res.Resource}}}}
	out, err := dyn.Resource(gvrSelfAccess).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return &pactomcp.Refused{Code: pactomcp.RefusalReviewFailed, Message: "asking whether you may update pactos in " + ns + " failed: " + err.Error()}
	}
	allowed, _, _ := unstructured.NestedBool(out.Object, "status", "allowed")
	evalErr, _, _ := unstructured.NestedString(out.Object, "status", "evaluationError")
	switch {
	case !allowed && evalErr != "":
		return &pactomcp.Refused{Code: pactomcp.RefusalReviewFailed, Message: "asking whether you may update pactos in " + ns + " could not be evaluated: " + evalErr}
	case !allowed:
		return &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "accepting a golden path needs update pactos in " + ns}
	}
	return nil
}

// mayUpdateSelf refuses a caller the apiserver does not allow to update the
// workload from names (k8s:ns/Kind/name): a SelfSubjectAccessReview with the
// caller's own client dyn, the rule delivery counts approvals by. A review
// that failed or could not be evaluated refuses too.
func mayUpdateSelf(ctx context.Context, dyn dynamic.Interface, from string) *pactomcp.Refused {
	_, ns, kind, name, _ := fleet.ParseK8sCaller(from)
	access := (&app.Delivery{Dynamic: dyn, Now: time.Now}).WorkloadAccess(ctx, ns, delivery.WorkloadRef{Kind: kind, Name: name})
	switch {
	case access.Unverified != "":
		return &pactomcp.Refused{Code: pactomcp.RefusalReviewFailed, Message: "asking whether you may " + access.Phrase() + " failed: " + access.Unverified}
	case !access.Allowed:
		return &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "declaring " + name + " needs " + access.Phrase()}
	}
	return nil
}

// mcpWritten is a server draft write as pacto_declare's run.
func mcpWritten(ans *dashboard.DeclareAcceptAnswer, err error) (json.RawMessage, *pactomcp.Refused, error) {
	if err != nil {
		return nil, nil, err
	}
	if r := ans.Refusal; r != nil {
		return nil, &pactomcp.Refused{Code: pactomcp.WriteRefusal(r.Code), Message: r.Message, CLI: r.Command}, nil
	}
	raw, _ := json.Marshal(map[string]any{"executed": ans.Published != "", "tag": ans.Tag, "published": ans.Published, "draft": ans.Draft,
		"signed": ans.Signed, "acceptCLI": ans.Command}) // ponytail: a map of strings and bools always marshals
	return raw, nil, nil
}

// reads is whether registry is the one the server writes to, a repository
// the installation reads, one under it, or the namespace directly holding it
// (where a new service's bundle goes). A bare host, or a namespace above
// that, is no installation registry: the server would read repositories it
// was never given with its own credentials.
func (r remoteDeclarer) reads(registry string) bool {
	if r.writes() && registry == r.registry {
		return true
	}
	for _, repo := range r.repos {
		if strings.Contains(registry, "/") && (repo == registry || path.Dir(repo) == registry || strings.HasPrefix(registry, repo+"/")) {
			return true
		}
	}
	return false
}

// view is the caller's fleet view; nil with no fleet, or one that cannot be
// read.
func (r remoteDeclarer) view(ctx context.Context) *fleet.Query {
	if r.fleet == nil {
		return nil
	}
	q, err := r.fleet.Src(ctx)
	if err == nil && r.fleet.Opts.View != nil {
		q, err = r.fleet.Opts.View(ctx, nil, q)
	}
	if err != nil {
		return nil
	}
	return q
}

// shows is whether the caller's fleet view shows service.
func (r remoteDeclarer) shows(ctx context.Context, service string) bool {
	return showsService(r.view(ctx), service)
}

// observed is the workload req names as the caller's restricted fleet view
// shows it, when exactly one inferred workload matches; else empty. The
// plan never reads the cluster with the server's identity for it.
func (r remoteDeclarer) observed(ctx context.Context, req app.DeclareRequest) *app.DeclareObserved {
	out := &app.DeclareObserved{}
	q := r.view(ctx)
	if q == nil {
		return out
	}
	ns, kind, name := req.Namespace, "", req.Service
	switch parts := strings.Split(req.Service, "/"); len(parts) {
	case 2:
		ns, name = parts[0], parts[1]
	case 3:
		ns, kind, name = parts[0], parts[1], parts[2]
	}
	var found *fleet.InferredWorkload
	for _, s := range q.SnapshotForSerialization().Services {
		w := s.Workload
		if !s.Inferred() || w == nil || w.Name != name || ns != "" && w.Namespace != ns || kind != "" && w.Kind != kind {
			continue
		}
		if found != nil {
			return out
		}
		found = w
	}
	if found != nil {
		out.Cluster, out.Namespace, out.Kind, out.Name = found.Cluster, found.Namespace, found.Kind, found.Name
		out.Repositories, out.RepositoryUnknown = found.Repositories, found.RepositoriesIncomplete()
	}
	return out
}

// baselineHidden is p for a caller who may not see its service: the
// version and what this run read with the caller's own credentials, with no
// baseline contract, digests, publish plan or ranges.
func baselineHidden(p *app.DeclarePlan) *app.DeclarePlan {
	return &app.DeclarePlan{Service: p.Service, Version: p.Version, BundleRepo: p.BundleRepo, State: p.State, InferredFrom: p.InferredFrom,
		Cluster: p.Cluster, Signer: p.Signer, PolicyNote: p.PolicyNote, Coverage: p.Coverage, Hidden: p.Hidden, ClusterAsOf: p.ClusterAsOf, IntentDigest: p.IntentDigest,
		AcceptCLI: p.AcceptCLI, Unchanged: p.Unchanged, Ranges: []app.DeclaredRange{},
		Notes: []string{"the published baseline of " + p.Service + " is hidden: you may not read it in the fleet"}}
}
