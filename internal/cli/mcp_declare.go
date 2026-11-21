package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
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
// installation reads (its --oci references), shows baseline content only to
// a caller the fleet view shows the service to, and never writes: the
// server has no registry write path of its own.
type remoteDeclarer struct {
	svc   *app.Service
	repos []string
	fleet *pactomcp.FleetMode
}

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
	if a.Dir != "" {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "dir names a folder of this server: declare a folder with pacto declare --dir " +
			"on your machine, or pacto_declare over stdio", CLI: a.Command("")}, nil
	}
	if a.Draft {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server writes no draft: save one with pacto declare --draft " +
			"on your machine, or pacto_declare over stdio", CLI: a.Command("")}, nil
	}
	if !r.reads(a.Registry) {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalNoRegistry, Message: "this server plans declarations only in the registries it reads (" +
			strings.Join(r.repos, ", ") + "): run pacto declare on your machine with your own registry credentials", CLI: a.Command("")}, nil
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
		// This server signs nothing and writes no draft: who would sign is
		// the person who runs the plan's command, whose login decides the state.
		p.Signer = app.SignerView{Class: contract.SignerClassAgent, Source: "none",
			Why: "this HTTP server writes nothing: a person runs the plan's pacto declare command"}
		p.State, p.AcceptCLI = "", ""
		baseline := p.Baseline != nil || len(p.Skipped) > 0 || p.DraftsTotal > 0 || baselineRefusal(err)
		if (baseline || len(p.MayAccept) > 0 || ownerRefusal(err)) && !r.shows(ctx, p.Service) {
			if baseline {
				p, err = baselineHidden(p), hiddenRefusal(p.Service, err)
			}
			p, err = ownerHidden(p), hiddenOwnerRefusal(err)
		}
		p.Notes = append(p.Notes, "a person carries out this plan on their machine with: "+a.Command(p.IntentDigest))
	}
	return declareAnswer(p, err)
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

// Execute never writes: it names the pacto declare a person runs, replaying
// every argument the intent digest binds, without --yes.
func (r remoteDeclarer) Execute(_ context.Context, a pactomcp.DeclareArgs) (json.RawMessage, *pactomcp.Refused, error) {
	return nil, &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server writes no declaration: run the command on your machine; " +
		"it prints the same plan, and adding --yes publishes it signed by you", CLI: a.Command(a.IntentDigest)}, nil
}

// reads is whether registry is a repository the installation reads, one
// under it, or the namespace directly holding it (where a new service's
// bundle goes). A bare host, or a namespace above that, is no installation
// registry: the server would read repositories it was never given with its
// own credentials.
func (r remoteDeclarer) reads(registry string) bool {
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
	if r.fleet == nil {
		return false
	}
	q, err := r.fleet.Src(ctx)
	if err == nil && r.fleet.Opts.View != nil {
		q, err = r.fleet.Opts.View(ctx, nil, q)
	}
	if err != nil {
		return false
	}
	_, err = q.GetService(service)
	return err == nil
}

// baselineHidden is p for a caller who may not see its service: the
// version and what this run read with the caller's own credentials, with no
// baseline contract, digests, publish plan or ranges.
func baselineHidden(p *app.DeclarePlan) *app.DeclarePlan {
	return &app.DeclarePlan{Service: p.Service, Version: p.Version, BundleRepo: p.BundleRepo, State: p.State, InferredFrom: p.InferredFrom,
		Cluster: p.Cluster, Signer: p.Signer, PolicyNote: p.PolicyNote, Coverage: p.Coverage, Hidden: p.Hidden, IntentDigest: p.IntentDigest,
		AcceptCLI: p.AcceptCLI, Unchanged: p.Unchanged, Ranges: []app.DeclaredRange{},
		Notes: []string{"the published baseline of " + p.Service + " is hidden: you may not read it in the fleet"}}
}
