package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
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
// options.
type mcpDeclarer struct {
	svc  *app.Service
	base app.DeclareRequest
}

// newMCPDeclarer signs drafts against public-good Sigstore and reads the
// owner policy from the target snapshots trust verifies.
func newMCPDeclarer(svc *app.Service, trust string) mcpDeclarer {
	base := app.DeclareRequest{DeliveryTrust: trust}
	base.Signing.FulcioURL, base.Signing.RekorURL = signing.PublicFulcio, signing.PublicRekor
	return mcpDeclarer{svc: svc, base: base}
}

func (m mcpDeclarer) Plan(ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	req, err := m.request(a)
	if err != nil {
		return nil, nil, err
	}
	if req.Dynamic, err = declareDynamic(req); err != nil {
		return nil, nil, err
	}
	p, _, err := m.svc.PlanDeclare(ctx, req)
	return declareAnswer(p, err)
}

func (m mcpDeclarer) Execute(ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	req, err := m.request(a)
	if err != nil {
		return nil, nil, err
	}
	if req.Dynamic, err = declareDynamic(req); err != nil {
		return nil, nil, err
	}
	res, err := m.svc.Declare(ctx, req)
	return declareAnswer(res, err)
}

// request is the agent's declare request for a.
func (m mcpDeclarer) request(a pactomcp.DeclareArgs) (app.DeclareRequest, error) {
	req := m.base
	req.Namespace, req.Dir, req.Registry, req.Accept = a.Namespace, a.Dir, a.Registry, a.Accept
	req.OnBehalfOf, req.IntentDigest, req.MaxItems, req.Agent, req.Draft = a.OnBehalfOf, a.IntentDigest, a.MaxItems, true, a.Draft
	if a.CallWindow != "" {
		w, err := time.ParseDuration(a.CallWindow)
		if err != nil {
			return req, errors.New("call_window is a duration such as 24h: " + err.Error())
		}
		req.CallWindow = w
	}
	// A claim names an external system, never a workload: declareSubject
	// would send it to pacto claim, which is this request.
	if _, ok := a.Claim(); ok {
		req.Service, req.Owner, req.Name = a.Service, a.Owner, a.Name
		return req, nil
	}
	return req, declareSubject(&req, a.Service)
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
	req.Dynamic = who.own.Dynamic
	p, _, err := r.svc.PlanDeclare(ctx, req)
	if p != nil {
		if !r.writes() || a.Registry != r.registry {
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

// claimArgs is a as this server plans it: a claim only of an external
// system the caller's fleet view shows, under the name its visible claim
// already has when a names none, so a claim again never adds a second
// claiming service.
func (r remoteDeclarer) claimArgs(ctx context.Context, a pactomcp.DeclareArgs) (pactomcp.DeclareArgs, *pactomcp.Refused) {
	id, ok := a.Claim()
	if !ok {
		return a, nil
	}
	ev := r.external(ctx, id)
	if ev == nil {
		return a, &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "your view of the fleet lists no external system " + id +
			": claim one it lists, or run pacto claim on your machine", CLI: a.Command("")}
	}
	if c := ev.External.Claim; a.Name == "" && c != nil && !c.Hidden {
		a.Name = string(c.Service)
	}
	return a, nil
}

// redact is p and err as the caller reads them (hide). A claim whose
// repository holds a service, or a draft of an external system, the
// caller's view does not show is one refusal naming none of it: the name
// alone may collide with a hidden service.
func (r remoteDeclarer) redact(ctx context.Context, a pactomcp.DeclareArgs, p *app.DeclarePlan, err error) (*app.DeclarePlan, error) {
	if id, ok := a.Claim(); ok && r.claimHidden(ctx, id, cmp.Or(a.Name, inferredcontract.ServiceName(fleet.ExternalKey(id))), p, err) {
		return nil, &app.DeclareError{Code: string(pactomcp.RefusalNotAllowed), CLI: a.Command(""),
			Message: "the repository for this name holds a service you may not read: claim with the CLI and --name"}
	}
	return r.hide(ctx, p, err)
}

// claimHidden reports whether the repository the claim of id under name
// writes to holds what the caller may not read: a published version, or a
// draft not of an external system, of a service their view does not show,
// or a draft of another external system it does not list. Drafts of an
// external system the caller sees are theirs to read.
func (r remoteDeclarer) claimHidden(ctx context.Context, id, name string, p *app.DeclarePlan, err error) bool {
	var de *app.DeclareError
	mismatch := errors.As(err, &de) && de.Code == string(pactomcp.RefusalExternalMismatch)
	held := p.Baseline != nil || len(p.Skipped) > 0 || p.DraftsTotal > len(p.Drafts) || mismatch || baselineRefusal(err)
	return slices.ContainsFunc(p.Drafts, func(d app.DraftView) bool {
		other, ok := strings.CutPrefix(d.From.InferredFrom, fleet.ExternalSubjectPrefix)
		if ok {
			return other != id && r.external(ctx, other) == nil
		}
		held = true
		return false
	}) || held && !r.shows(ctx, name)
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
	case a.Dir != "":
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "dir names a folder of this server: declare a folder with pacto declare --dir " +
			"on your machine, or pacto_declare over stdio", CLI: a.Command("")}
	case a.Draft && !r.writes():
		return &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server writes no draft: save one with pacto declare --draft " +
			"on your machine, or pacto_declare over stdio", CLI: a.Command("")}
	case !r.reads(a.Registry):
		return &pactomcp.Refused{Code: pactomcp.RefusalNoRegistry, Message: "this server plans declarations only in the registries it reads (" +
			strings.Join(r.repos, ", ") + "): run pacto declare on your machine with your own registry credentials", CLI: a.Command("")}
	}
	return nil
}

// baselineRefusal reports whether err is a refusal built from what the
// published baseline holds: its tags and signers, publish section or
// pactoVersion, or the registry read of it.
func baselineRefusal(err error) bool {
	var de *app.DeclareError
	return errors.As(err, &de) && slices.Contains([]pactomcp.WriteRefusal{pactomcp.RefusalVersionConflict, pactomcp.RefusalBundleRepoMismatch,
		pactomcp.RefusalBaselineNeedsMigration, pactomcp.RefusalRegistryUnreadable}, pactomcp.WriteRefusal(de.Code))
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

// Execute writes the caller's draft when the server has a registry and a
// push credential: into that registry alone, unsigned, on behalf of the
// caller the apiserver reviewed, who must be allowed to update the workload
// (a SelfSubjectAccessReview with their own credentials), through the
// dashboard's writer. Without them it names the pacto declare a person
// runs, replaying every argument the intent digest binds, without --yes.
func (r remoteDeclarer) Execute(ctx context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	if !r.writes() {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server writes no declaration: run the command on your machine; " +
			"it prints the same plan, and adding --yes publishes it signed by you", CLI: a.Command(a.IntentDigest)}, nil
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
	a, refused := r.claimArgs(ctx, a)
	if refused != nil {
		return nil, refused, nil
	}
	req, err := mcpDeclarer{}.request(a)
	if err != nil {
		return nil, nil, err
	}
	req.Draft, req.OnBehalfOf, req.Dynamic = true, who.person.User, who.own.Dynamic
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
	// A claim has no workload to review: the owner rule a person's
	// acceptance runs is its gate.
	if _, claim := a.Claim(); !claim {
		if refused := mayUpdateSelf(ctx, who.own.Dynamic, p.InferredFrom); refused != nil {
			return nil, refused, nil
		}
	}
	req.Store = pushWriter(r.push, p.BundleRepo)
	return mcpWritten(writeServerDraft(ctx, r.svc, req, p, decl, who.person.User, "mcp"))
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
	q := r.view(ctx)
	if q == nil {
		return false
	}
	_, err := q.GetService(service)
	return err == nil
}

// external is the external system id as the caller's fleet view shows it;
// nil when it shows none.
func (r remoteDeclarer) external(ctx context.Context, id string) *fleet.ExternalView {
	q := r.view(ctx)
	if q == nil {
		return nil
	}
	ev, _ := q.GetExternal(id)
	return ev
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
