package cli

import (
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
	req.Namespace, req.Dir, req.Registry, req.Accept = a.Namespace, a.Dir, a.Registry, a.Accept
	req.OnBehalfOf, req.IntentDigest, req.MaxItems, req.Agent, req.Draft = a.OnBehalfOf, a.IntentDigest, a.MaxItems, true, a.Draft
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
	req, err := mcpDeclarer{}.request(a)
	if err != nil {
		return nil, nil, err
	}
	who := reviewedFrom(ctx)
	if who.own == nil {
		return nil, nil, errNobodyReviewed
	}
	req.Dynamic, req.Observed = who.own.Dynamic, r.observed(ctx, req)
	p, _, err := r.svc.PlanDeclare(ctx, req)
	if p != nil {
		if !r.writes() || a.Registry != r.registry {
			// This server signs nothing and writes no draft here: who would sign
			// is the person who runs the plan's command, whose login decides the state.
			p.Signer = app.SignerView{Class: contract.SignerClassAgent, Source: "none",
				Why: "this HTTP server writes nothing into " + a.Registry + ": a person runs the plan's pacto declare command"}
			p.State, p.AcceptCLI = "", ""
		}
		p, err = r.hide(ctx, p, err)
		p.Notes = append(p.Notes, "a person carries out this plan on their machine with: "+a.Command(p.IntentDigest))
	}
	return declareAnswer(p, err)
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
	req, err := mcpDeclarer{}.request(a)
	if err != nil {
		return nil, nil, err
	}
	req.Draft, req.OnBehalfOf, req.Dynamic, req.Observed = true, who.person.User, who.own.Dynamic, r.observed(ctx, req)
	p, decl, err := r.svc.PlanDeclare(ctx, req)
	if err != nil {
		// Execute may come without a plan, or the baseline may move since it:
		// a refusal reaches the caller only as Plan would show it.
		if p == nil {
			p = &app.DeclarePlan{Service: req.Service}
		}
		_, err = r.hide(ctx, p, err)
		return declareAnswer[app.DeclarePlan](nil, err)
	}
	if p.IntentDigest != a.IntentDigest {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalPlanStale, Message: "the plan changed since intent " + a.IntentDigest + " was reviewed; it is now " +
			p.IntentDigest + ": review the new plan"}, nil
	}
	if refused := mayUpdateSelf(ctx, who.own.Dynamic, p.InferredFrom); refused != nil {
		return nil, refused, nil
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

// shows is whether the caller's fleet view shows service. With no fleet, or
// one that cannot be read, it does not.
func (r remoteDeclarer) shows(ctx context.Context, service string) bool {
	q, err := r.view(ctx)
	if err != nil {
		return false
	}
	_, err = q.GetService(service)
	return err == nil
}

// view is the caller's restricted fleet view.
func (r remoteDeclarer) view(ctx context.Context) (*fleet.Query, error) {
	if r.fleet == nil {
		return nil, errors.New("no fleet")
	}
	q, err := r.fleet.Src(ctx)
	if err == nil && r.fleet.Opts.View != nil {
		q, err = r.fleet.Opts.View(ctx, nil, q)
	}
	return q, err
}

// observed is the workload req names as the caller's restricted fleet view
// shows it, when exactly one inferred workload matches; else empty. The
// plan never reads the cluster with the server's identity for it.
func (r remoteDeclarer) observed(ctx context.Context, req app.DeclareRequest) *app.DeclareObserved {
	out := &app.DeclareObserved{}
	q, err := r.view(ctx)
	if err != nil {
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
		out.Namespace, out.Kind, out.Name, out.Repositories = found.Namespace, found.Kind, found.Name, found.Repositories
		if found.Image != nil {
			out.Image = found.Image.Value
		}
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
