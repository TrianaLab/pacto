package cli

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// createAppearsWithin is how soon the fleet shows a created service: the
// dashboard's whole-index read is memoized for a minute.
const createAppearsWithin = "a minute"

// dashboardCreator creates services from declared golden paths for the
// person behind a dashboard request. It shares the declarer's registry, push
// credential, signing login and cluster clients: the person is reviewed in
// the target namespace first, a version is signed only by their fresh login,
// and writes go through push scopes that hold the created repository and the
// organization index alone.
type dashboardCreator struct {
	*dashboardDeclarer
}

// newDashboardCreator is the creator of a dashboard with a declarer; nil
// without one (no registry: no Create service).
func newDashboardCreator(d dashboard.Declarer) dashboard.ServiceCreator {
	if dd, ok := d.(*dashboardDeclarer); ok && dd != nil {
		return &dashboardCreator{dd}
	}
	return nil
}

// portalCreateURL is where the configured Backstage creates a service, ""
// without a configured URL: a detected connection names an in-cluster
// address a browser cannot open.
func portalCreateURL(v *viper.Viper) string {
	if base := v.GetString("dashboard.backstage-url"); base != "" {
		return backstage.ScaffolderRoot(base)
	}
	return ""
}

// caller is who the person behind r is: the signed-in person, or on a local
// host the kubeconfig's identity when the cluster says it.
func (c *dashboardCreator) caller(r *http.Request) string {
	who := c.id.state(r.Context()).Person.User
	if who == "" && c.id.auth.Local() {
		p, _ := c.id.localSelf(r.Context())
		who = p.User
	}
	return who
}

// mayCreate asks, on a shared host, whether the person may create the
// service in the namespace (services.pacto.trianalab.io, named by the
// service), a 403 when not or when nobody can tell. A local host acts with the
// person's own kubeconfig, which the API server authorizes.
func (c *dashboardCreator) mayCreate(ctx context.Context, in dashboard.CreateServiceInput) error {
	if c.id.auth.Local() {
		return nil
	}
	if c.id.viewer == nil {
		return dashboard.Refuse(http.StatusForbidden, "NotAllowed", "this dashboard reviews nobody, so it creates no service: run pacto init --from")
	}
	who := c.id.state(ctx).Person
	ok, err := c.id.viewer.MayCreateService(ctx, who, in.Cluster, in.Namespace, in.Name)
	switch {
	case errors.Is(err, authn.ErrClusterNotReviewable):
		return dashboard.Refuse(http.StatusForbidden, "ClusterNotReviewable", "Pacto cannot review you in the cluster of "+in.Namespace+", so it creates nothing there")
	case err != nil:
		return dashboard.Refuse(http.StatusForbidden, "ReviewFailed", "asking whether you may create services in "+in.Namespace+" failed: "+err.Error())
	case !ok:
		return dashboard.Refuse(http.StatusForbidden, "NotAllowed", who.User+" may not create services in "+in.Namespace)
	}
	return nil
}

// request is the create request of in for the person behind r: the golden
// path the route resolved in their view, the offerings that view lets them
// consume, the trust loaded at start and the push-scoped index opener.
func (c *dashboardCreator) request(r *http.Request, p dashboard.CreateServicePath, in dashboard.CreateServiceInput) app.DeclareRequest {
	req := c.base
	req.Service, req.Registry = in.Name, c.registry
	if in.FromDraft != nil {
		req.FromDraft = in.FromDraft.Tag
	}
	consumable, partial, asOf := consumableOfferings(p.View)
	req.Create = &app.CreateFrom{Path: p.Ref, Owner: in.Owner, Namespace: in.Namespace, Repository: in.Repository, Revision: in.Revision,
		Caller: c.caller(r), Offerings: picksOf(in.Offerings), Consumable: consumable, ConsumablePartial: partial, ConsumableAsOf: asOf,
		Trust: c.id.trust, ReviewURL: "#/fleet/services/" + url.PathEscape(string(p.Key))}
	if c.push != nil {
		req.Create.OpenIndex = pushRepo(c.push, oci.InstallationRepository(strings.TrimSuffix(c.registry, "/"), oci.IndexRepository))
	}
	// The one cluster, when there is one, gives the installation signer and
	// owner policies.
	if cfg := c.clusterConfig(r.Context(), in.Cluster); cfg != nil {
		req.Dynamic, _ = c.client(r, cfg)
	}
	return req
}

// picksOf is the offering values of a create input.
func picksOf(in []dashboard.CreateServiceOffering) []app.OfferingPick {
	out := make([]app.OfferingPick, 0, len(in))
	for _, o := range in {
		out = append(out, app.OfferingPick(o))
	}
	return out
}

// consumableOfferings is the caller's consumable-offerings read on their
// restricted view q, by offering, with every consumable provider's
// repository; partial when the read is not complete; and when it was taken.
func consumableOfferings(q *fleet.Query) (map[string]app.CreateOffering, bool, string) {
	list, providers := consumableRead(q)
	out := map[string]app.CreateOffering{}
	for _, e := range list.Entities {
		p := providers[e.Key]
		o := app.CreateOffering{Repository: p.Snippet.Ref, Snippet: p.Snippet}
		for _, other := range e.Consumable.Providers {
			o.Repositories = append(o.Repositories, other.Snippet.Ref)
		}
		out[e.Key] = o
	}
	partial := list.Meta.Completeness != fleet.CompletenessComplete || list.Truncated
	return out, partial, list.Meta.AsOf.UTC().Format(time.RFC3339)
}

// consumableRead is the consumable offerings list of q and, per offering,
// the provider a pick appends: the first by service key. A configuration
// may reference any consumable provider (CreateOffering.Repositories).
func consumableRead(q *fleet.Query) (*fleet.EntityList, map[string]fleet.ConsumableProvider) {
	list, _ := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true, Limit: fleet.MaxEntityLimit}) // ponytail: a fixed valid filter
	out := map[string]fleet.ConsumableProvider{}
	for _, e := range list.Entities {
		out[e.Key] = e.Consumable.Providers[0]
	}
	return list, out
}

// offeringViews are the offerings the caller may consume, the ones the
// golden path requests first, each with its classes and fields.
func offeringViews(q *fleet.Query, requested []string) []dashboard.CreateServiceOfferingView {
	_, providers := consumableRead(q)
	var first, rest []dashboard.CreateServiceOfferingView
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		p := providers[name]
		v := dashboard.CreateServiceOfferingView{Offering: name, Version: p.Version, Requested: slices.Contains(requested, name),
			Classes: p.Classes, Parameters: p.Parameters, Guarantees: p.Guarantees}
		if v.Requested {
			first = append(first, v)
		} else {
			rest = append(rest, v)
		}
	}
	return append(append([]dashboard.CreateServiceOfferingView{}, first...), rest...)
}

// createRefused is err as a refusal and every refused input it carries
// when err is a declare refusal; any other error is returned.
func createRefused(err error) (*dashboard.DeclareRefusal, []dashboard.CreateServiceRefusal, error) {
	var de *app.DeclareError
	if !errors.As(err, &de) {
		return nil, nil, err
	}
	var out []dashboard.CreateServiceRefusal
	for _, r := range de.Refusals {
		out = append(out, dashboard.CreateServiceRefusal(r))
	}
	return refused(de.Code, de.Message, de.CLI), out, nil
}

// Plan implements dashboard.ServiceCreator: the review in the namespace,
// then the plan with the person's login planned, or a draft on their behalf
// when they cannot sign here.
func (c *dashboardCreator) Plan(r *http.Request, p dashboard.CreateServicePath, in dashboard.CreateServiceInput) (*dashboard.CreateServiceAnswer, error) {
	if err := c.mayCreate(r.Context(), in); err != nil {
		return nil, err
	}
	ans := &dashboard.CreateServiceAnswer{Signing: c.mode(r), Write: dashboard.DeclareWrite{Available: c.CanWrite()}, Offerings: offeringViews(p.View, p.Requests)}
	if !ans.Write.Available {
		ans.Write.Reason = noPushCredential
	}
	fresh := ans.Signing.Mode == signModeFreshLogin
	ans.SignedIn = fresh && c.signedIn(r, in.IntentDigest)
	req := c.request(r, p, in)
	if fresh || in.FromDraft != nil {
		req.Login = plannedLogin
	} else {
		req.Draft, req.Agent = true, true
	}
	plan, decl, err := c.svc.PlanDeclare(r.Context(), req)
	if ans.Refusal, ans.Refusals, err = createRefused(err); err != nil || ans.Refusal != nil {
		return ans, err
	}
	ans.Plan = createPlanView(p, plan, decl)
	ans.Drafts, ans.DraftsTotal = draftViews(plan.Drafts), plan.DraftsTotal
	if fresh {
		ans.SignTicket = c.ticket(r, plan.IntentDigest, nil)
	}
	return ans, nil
}

// createPlanView is a create plan as the dashboard answers it.
func createPlanView(p dashboard.CreateServicePath, plan *app.DeclarePlan, decl declarationTexts) *dashboard.CreateServicePlan {
	cp := plan.Create
	out := &dashboard.CreateServicePlan{Service: plan.Service, Version: plan.Version, BundleRepo: plan.BundleRepo, State: plan.State, Owner: plan.Owner,
		MayAccept: plan.MayAccept, Signer: dashboard.DeclareSigner(plan.Signer), PolicyNote: plan.PolicyNote, IntentDigest: plan.IntentDigest,
		Notes: plan.Notes, Path: cp.Path, PathName: p.Name, PathVersion: p.Version, Signature: cp.Signature, Namespace: cp.Namespace,
		Revision: cp.Revision, InitCLI: cp.InitCLI, ConsumableAsOf: cp.ConsumableAsOf, ConsumableCompleteness: cp.ConsumableCompleteness,
		Contract: dashboard.NewDeclareText(decl.Contract()), Record: dashboard.NewDeclareText(decl.Record())}
	if cp.ValuesFile != "" {
		out.ValuesFile = &dashboard.CreateServiceFile{Path: app.CreateValuesFile, DeclareText: dashboard.NewDeclareText([]byte(cp.ValuesFile))}
	}
	return out
}

// Accept implements dashboard.ServiceCreator: the review in the namespace,
// then the plan the person reviewed (its intent digest), then the write.
func (c *dashboardCreator) Accept(r *http.Request, p dashboard.CreateServicePath, in dashboard.CreateServiceAcceptInput) (*dashboard.CreateServiceAcceptAnswer, error) {
	ctx := r.Context()
	if err := c.mayCreate(ctx, in.CreateServiceInput); err != nil {
		return nil, err
	}
	req := c.request(r, p, in.CreateServiceInput)
	req.IntentDigest = in.IntentDigest
	who := c.caller(r)
	if refusal := c.createSigner(r, &req, in, who); refusal != nil {
		return &dashboard.CreateServiceAcceptAnswer{Refusal: refusal}, nil
	}
	plan, decl, err := c.svc.PlanDeclare(ctx, req)
	ans := &dashboard.CreateServiceAcceptAnswer{}
	if ans.Refusal, ans.Refusals, err = createRefused(err); err != nil || ans.Refusal != nil {
		return ans, err
	}
	if plan.IntentDigest != in.IntentDigest {
		in.IntentDigest = ""
		fresh, err := c.Plan(r, p, in.CreateServiceInput)
		return &dashboard.CreateServiceAcceptAnswer{Plan: fresh, Refusal: refused("PlanStale", "the plan changed since intent "+req.IntentDigest+
			" was reviewed; it is now "+plan.IntentDigest+": review the new plan", req.Command(""))}, err
	}
	req.Store = pushWriter(c.push, plan.BundleRepo)
	if !req.Draft {
		if refusal := c.login(r, &req); refusal != nil {
			return &dashboard.CreateServiceAcceptAnswer{Refusal: refusal}, nil
		}
	} else if tag := sameDraft(ctx, req, plan, decl.Contract(), who); tag != "" {
		return &dashboard.CreateServiceAcceptAnswer{Tag: tag, Draft: true, AcceptURL: req.AcceptURL(plan.BundleRepo, tag)}, nil
	}
	return c.write(ctx, req, who)
}

// createSigner sets how req signs for in's mode: the person's fresh login
// for sign, nobody on their behalf for draft. A draft is never accepted with
// a draft.
func (c *dashboardCreator) createSigner(r *http.Request, req *app.DeclareRequest, in dashboard.CreateServiceAcceptInput, who string) *dashboard.DeclareRefusal {
	if in.Mode == "draft" {
		if in.FromDraft != nil {
			return refused("NotAllowed", "a draft is accepted only with a person's signature", "")
		}
		req.Draft, req.Agent, req.OnBehalfOf = true, true, who
		return nil
	}
	if m := c.mode(r); m.Mode != signModeFreshLogin {
		return refused("NotAllowed", "you cannot sign here: "+m.Reason, req.Command(""))
	}
	req.Login = plannedLogin
	return nil
}

// write carries out req, which wrote nothing yet, logs one audit line and
// answers the person's handover: the files, the commands and when the fleet
// shows the service.
func (c *dashboardCreator) write(ctx context.Context, req app.DeclareRequest, who string) (*dashboard.CreateServiceAcceptAnswer, error) {
	res, err := c.svc.Declare(ctx, req)
	ans := &dashboard.CreateServiceAcceptAnswer{}
	if ans.Refusal, ans.Refusals, err = createRefused(err); err != nil || ans.Refusal != nil {
		return ans, err
	}
	auditDeclare(ctx, "dashboard", who, req, res.Plan.Service, res.Tag, res.Published)
	ans.Published, ans.Tag, ans.Draft, ans.Signed, ans.AcceptURL = res.Published, res.Tag, res.Draft, res.Signed, res.Plan.AcceptURL
	ans.Files, ans.FilesTotal = createFiles(res.Files)
	if cr := res.Created; cr != nil {
		ans.InitCLI, ans.GitCLI, ans.PullCLI, ans.Index = res.Plan.Create.InitCLI, cr.GitCLI, cr.PullCLI, cr.Index
		ans.AppearsWithin = createAppearsWithin
	}
	return ans, nil
}

// createFiles is files by path, at most MaxCreateServiceFiles, each cut at
// MaxDeclareText, and how many there are.
func createFiles(files map[string][]byte) ([]dashboard.CreateServiceFile, int) {
	names := slices.Sorted(maps.Keys(files))
	out := make([]dashboard.CreateServiceFile, 0, min(len(names), dashboard.MaxCreateServiceFiles))
	for _, name := range names[:min(len(names), dashboard.MaxCreateServiceFiles)] {
		out = append(out, dashboard.CreateServiceFile{Path: name, DeclareText: dashboard.NewDeclareText(files[name])})
	}
	return out, len(names)
}
