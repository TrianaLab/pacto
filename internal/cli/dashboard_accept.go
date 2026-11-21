package cli

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// acceptPlanRefusals are the pacto accept refusals the plan answers as
// "nothing to accept" rather than as an error.
var acceptPlanRefusals = []string{"AcceptNothingProposed", "NotPublished", "DraftNotFound", "PlanStale", "SignerUnverified"}

// dashboardAcceptPlan plans "Accept all proposed ranges" for the dashboard:
// pacto accept --proposed --dry-run over the revision's bundle repository
// (or --draft for an acceptance draft), read with the dashboard's own
// registry credentials. It returns the plan and the command a person runs to
// sign it; it never signs and never writes. Signing goes through the
// declarer's AcceptRanges, with the person's own fresh login.
func dashboardAcceptPlan(svc *app.Service, trust func(context.Context) (app.Installation, error)) func(context.Context, fleet.ServiceKey, *fleet.ContractRevision, string) (dashboard.AcceptPlanAnswer, error) {
	return func(ctx context.Context, _ fleet.ServiceKey, rev *fleet.ContractRevision, fromDraft string) (dashboard.AcceptPlanAnswer, error) {
		repo := revisionRepo(rev)
		if repo == "" {
			return dashboard.AcceptPlanAnswer{Refusal: &dashboard.AcceptRefusal{Code: "NotPublished",
				Message: rev.Contract.Service.Name + " names no bundle repository: declare publish.bundle and publish it first", Command: "pacto ci --publish ."}}, nil
		}
		opts := app.AcceptOptions{Ref: repo, Proposed: fromDraft == "", Trust: trust}
		if fromDraft != "" {
			opts.Draft = repo + ":" + fromDraft
		}
		p, err := svc.AcceptPlan(ctx, opts)
		var de *app.DeclareError
		if errors.As(err, &de) && slices.Contains(acceptPlanRefusals, de.Code) {
			return dashboard.AcceptPlanAnswer{Refusal: &dashboard.AcceptRefusal{Code: de.Code, Message: de.Message, Command: de.CLI}}, nil
		}
		if err != nil {
			return dashboard.AcceptPlanAnswer{}, err
		}
		return dashboard.AcceptPlanAnswer{Plan: acceptPlanView(p), Command: acceptCommandOf(repo, fromDraft, p.IntentDigest)}, nil
	}
}

// acceptCommandOf is the pacto accept a person runs to sign the plan of repo
// (of its acceptance draft fromDraft) with intent.
func acceptCommandOf(repo, fromDraft, intent string) string {
	if fromDraft != "" {
		return "pacto accept " + contract.OCIScheme + repo + " --draft " + repo + ":" + fromDraft + " --intent " + intent
	}
	return "pacto accept " + contract.OCIScheme + repo + " --proposed --intent " + intent
}

// acceptPlanView is p as the dashboard answers it, with its drafts.
func acceptPlanView(p *app.AcceptPlan) *dashboard.AcceptPlanView {
	v := &dashboard.AcceptPlanView{Service: p.Service, Version: p.Version, Subject: p.Subject, Ranges: p.Ranges,
		WhoMayAccept: p.WhoMayAccept, Existing: p.Existing, Notes: p.Notes, IntentDigest: p.IntentDigest,
		Draft: draftTag(p.Draft), OnBehalfOf: p.OnBehalfOf, DraftsTotal: p.DraftsTotal}
	for _, d := range p.Drafts {
		v.Drafts = append(v.Drafts, dashboard.AcceptDraftView{Tag: draftTag(d.Ref), State: d.State, Reason: d.Reason, Ranges: d.Ranges,
			Break: d.Break, OnBehalfOf: d.OnBehalfOf, Asker: d.Asker.Subject, Signed: d.Signed})
	}
	return v
}

// draftTag is the tag of a <repository>:<tag> reference, "" for "".
func draftTag(ref string) string {
	return ref[strings.LastIndex(ref, ":")+1:]
}

// revisionRepo is the bundle repository a revision is published to: the
// oci:// reference the fleet read it from, else its publish.bundle (a
// folder source); "" for neither. The contract only declares publish.bundle,
// so the plan is checked to be of this service before it is shown.
func revisionRepo(rev *fleet.ContractRevision) string {
	if ref := cmp.Or(rev.ResolvedRef, rev.RequestedRef); strings.HasPrefix(ref, contract.OCIScheme) {
		return repoOf(ref)
	}
	if p := rev.Contract.Publish; p != nil && p.Bundle != "" {
		return repoOf(p.Bundle)
	}
	return ""
}

// AcceptOffer implements dashboard.Declarer: the person signs the accept plan
// whose intent digest is intent with a fresh login, as they sign a
// declaration, and saves a draft when the host can write.
func (d *dashboardDeclarer) AcceptOffer(r *http.Request, intent string) dashboard.AcceptExecuteOffer {
	o := dashboard.AcceptExecuteOffer{Signing: d.mode(r), Write: dashboard.DeclareWrite{Available: d.CanWrite()}}
	switch {
	case !o.Write.Available:
		o.Write.Reason = noPushCredential
	case o.Signing.Mode == signModeFreshLogin:
		o.SignTicket = d.ticket(r, intent, nil)
	}
	return o
}

// AcceptRanges implements dashboard.Declarer: identity (the route), then
// update on a workload (else the Pacto resource) of the service, then the
// plan the person reviewed by its intent digest, then the write: signed by
// the person's fresh login, or an unsigned acceptance draft on their behalf.
// Every write goes into the service's bundle repository alone, with the push
// credential.
func (d *dashboardDeclarer) AcceptRanges(r *http.Request, t dashboard.AcceptTarget, in dashboard.AcceptExecuteInput) (*dashboard.AcceptExecuteAnswer, error) {
	ctx := r.Context()
	name := t.Rev.Contract.Service.Name
	if refusal := d.mayUpdateService(ctx, name, t.Runs); refusal != nil {
		return &dashboard.AcceptExecuteAnswer{Refusal: refusal}, nil
	}
	repo := revisionRepo(t.Rev)
	if repo == "" {
		return &dashboard.AcceptExecuteAnswer{Refusal: refused("NotPublished", name+" names no bundle repository: declare publish.bundle and publish it first", "pacto ci --publish .")}, nil
	}
	cmd := acceptCommandOf(repo, in.FromDraft, in.IntentDigest)
	who := d.id.state(ctx).Person.User
	opts := app.AcceptOptions{Ref: repo, Proposed: in.FromDraft == "", Intent: in.IntentDigest, Trust: d.trust, Signing: d.base.Signing, Write: pushRepo(d.push, repo)}
	if in.FromDraft != "" {
		opts.Draft = repo + ":" + in.FromDraft
	}
	switch {
	case in.Mode == "draft" && in.FromDraft != "":
		return &dashboard.AcceptExecuteAnswer{Refusal: refused("NotAllowed", "a draft is accepted only with a person's signature", cmd)}, nil
	case in.Mode == "draft":
		opts.Agent, opts.OnBehalfOf, opts.Signing.Draft = true, who, true
	default:
		if m := d.mode(r); m.Mode != signModeFreshLogin {
			return &dashboard.AcceptExecuteAnswer{Refusal: refused("NotAllowed", "you cannot sign here: "+m.Reason, cmd)}, nil
		}
		// Taken only once the plan was checked, so a refused accept keeps
		// the login.
		opts.Signing.Login = func(context.Context) (string, error) {
			if tok, ok := d.take(r, in.IntentDigest); ok {
				return tok, nil
			}
			return "", &app.DeclareError{Code: "SignerNotAllowed", Message: "sign in again to sign: the signing login for this plan is missing, used or expired"}
		}
	}
	res, err := d.svc.Accept(ctx, opts)
	if refusal, err := dashRefusal(err); err != nil || refusal != nil {
		return &dashboard.AcceptExecuteAnswer{Refusal: refusal}, err
	}
	logging.LoggerFromContext(ctx).Info("acceptance written", slog.String("via", "dashboard"), slog.String("actor", who),
		slog.String("service", res.Plan.Service), slog.String("record", res.Record), slog.String("draft", res.Draft))
	return &dashboard.AcceptExecuteAnswer{Record: res.Record, State: res.State, Reason: res.Reason, Draft: res.Draft, Command: res.AcceptCLI}, nil
}

// mayUpdateService asks whether the person may update one workload (else
// one Pacto resource) the service runs as: never a read verb. A service Pacto
// sees running nowhere has nothing to review against, so nothing is written.
func (d *dashboardDeclarer) mayUpdateService(ctx context.Context, name string, runs []dashboard.DeclareWorkload) *dashboard.DeclareRefusal {
	if d.id.auth.Local() {
		return nil
	}
	refusal := refused("NotAllowed", "Pacto sees "+name+" running nowhere it can ask whether you may update it, so it accepts nothing here: run the command", "")
	for _, w := range runs {
		if refusal = d.mayUpdate(ctx, w, "accepting ranges for "+name); refusal == nil {
			return nil
		}
	}
	return refusal
}

// pushRepo opens repositories with the push credential through a push
// scope that holds repo alone, so a write can tag nothing else.
func pushRepo(push gcrauthn.Keychain, repo string) func(string) (*remote.Repository, error) {
	scope := oci.NewPushScope("", repo)
	return func(path string) (*remote.Repository, error) {
		return ocireferrer.Open(path, ocireferrer.Options{Keychain: push, Insecure: oci.EnvInsecureRegistries(), Scope: scope})
	}
}
