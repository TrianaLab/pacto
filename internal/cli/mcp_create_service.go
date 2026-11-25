package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// remoteCreator is pacto_create_service over HTTP: it resolves the golden
// path and the consumable offerings in the caller's restricted fleet view,
// reviews the caller with a SelfSubjectAccessReview on their own exchanged
// credentials, plans with them and writes only an unsigned draft on their
// behalf, through the installation's push credential scoped to the created
// repository. A person reviews and signs the draft in the dashboard; no
// version is ever published here, and nothing the caller names is pulled.
type remoteCreator struct {
	svc   *app.Service
	fleet *pactomcp.FleetMode
	// registry and push are where and with what drafts are written; a
	// creator exists only with a registry, and writes only with push.
	registry string
	push     gcrauthn.Keychain
	// trust holds the template keys the golden path's signature is checked
	// with, loaded once at start.
	trust *delivery.TrustConfig
}

// newRemoteCreator is the creator of an HTTP server with registry and a
// fleet view; nil without either (pacto_create_service sits beside the fleet
// tools it resolves golden paths and offerings with).
func newRemoteCreator(svc *app.Service, fleet *pactomcp.FleetMode, registry string, push gcrauthn.Keychain, trust *delivery.TrustConfig) pactomcp.CreateServicer {
	if registry == "" || fleet == nil {
		return nil
	}
	return remoteCreator{svc: svc, fleet: fleet, registry: registry, push: push, trust: trust}
}

// writes is whether the server writes drafts.
func (r remoteCreator) writes() bool { return r.push != nil }

// Plan implements pactomcp.CreateServicer: the path in the caller's view,
// their review in the namespace, then the plan of a draft on their behalf.
func (r remoteCreator) Plan(ctx context.Context, a pactomcp.CreateServiceArgs) (json.RawMessage, *pactomcp.Refused, error) {
	req, refused, err := r.request(ctx, a)
	if err != nil || refused != nil {
		return nil, refused, err
	}
	p, _, err := r.svc.PlanDeclare(ctx, req)
	return createAnswer(p, err)
}

// Execute implements pactomcp.CreateServicer: Plan again, the reviewed
// intent, then the draft through the push credential.
func (r remoteCreator) Execute(ctx context.Context, a pactomcp.CreateServiceArgs) (json.RawMessage, *pactomcp.Refused, error) {
	who := reviewedFrom(ctx)
	if a.OnBehalfOf != "" && a.OnBehalfOf != who.person.User {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "this server writes a draft only on behalf of the caller it reviewed, " +
			who.person.User + ", never " + a.OnBehalfOf}, nil
	}
	req, refused, err := r.request(ctx, a)
	if err != nil || refused != nil {
		return nil, refused, err
	}
	req.OnBehalfOf, req.IntentDigest = who.person.User, a.IntentDigest
	p, decl, err := r.svc.PlanDeclare(ctx, req)
	if err != nil {
		return createAnswer(p, err)
	}
	if refused := pactomcp.RequireIntent(p.IntentDigest, a.IntentDigest); refused != nil {
		return nil, refused, nil
	}
	if !r.writes() {
		return nil, &pactomcp.Refused{Code: pactomcp.RefusalExecuteNeedsStdio, Message: "this HTTP server has no push credential, so it writes no draft: " +
			"create the service on your machine with this pacto init --from, then publish it with pacto publish", CLI: p.Create.InitCLI}, nil
	}
	req.Store = pushWriter(r.push, p.BundleRepo)
	ans, err := writeServerDraft(ctx, r.svc, req, p, decl, who.person.User, "mcp")
	if err != nil {
		return nil, nil, err
	}
	if rf := ans.Refusal; rf != nil {
		return nil, &pactomcp.Refused{Code: pactomcp.WriteRefusal(rf.Code), Message: rf.Message, CLI: rf.Command}, nil
	}
	raw, _ := json.Marshal(map[string]any{"executed": ans.Published != "", "tag": ans.Tag, "published": ans.Published, "draft": ans.Draft,
		"signed": ans.Signed, "acceptURL": req.AcceptURL(p.BundleRepo, ans.Tag)}) // ponytail: a map of strings and bools always marshals
	return raw, nil, nil
}

// request is the agent's create request for a: the golden path a's key
// names in the caller's restricted view, after the caller passed the review
// in the namespace, with the offerings that view lets them consume. A path
// the view hides answers as a missing one.
func (r remoteCreator) request(ctx context.Context, a pactomcp.CreateServiceArgs) (app.DeclareRequest, *pactomcp.Refused, error) {
	who := reviewedFrom(ctx)
	if who.own == nil {
		return app.DeclareRequest{}, nil, errNobodyReviewed
	}
	q := remoteDeclarer{fleet: r.fleet}.view(ctx)
	if q == nil {
		return app.DeclareRequest{}, &pactomcp.Refused{Code: pactomcp.RefusalRegistryUnreadable,
			Message: "this server could not read your view of the fleet, so it resolves no golden path: try again, or run pacto init --from on your machine"}, nil
	}
	p, why := q.CreatablePath(fleet.ServiceKey(a.Path))
	if why == fleet.UnconfirmedCreatablePath {
		return app.DeclareRequest{}, &pactomcp.Refused{Code: pactomcp.RefusalRegistryUnreadable, Message: why +
			": pass the key of a golden path pacto_fleet_entities lists with template=only"}, nil
	}
	if why != "" {
		return app.DeclareRequest{}, &pactomcp.Refused{Code: pactomcp.RefusalGoldenPathNotDeclared, Message: why +
			": pass the key of a golden path pacto_fleet_entities lists with template=only"}, nil
	}
	if refused := selfMayCreateService(ctx, who.own.Dynamic, a.Namespace, a.Name); refused != nil {
		return app.DeclareRequest{}, refused, nil
	}
	consumable, partial, asOf := consumableOfferings(q)
	req := app.DeclareRequest{Service: a.Name, Registry: r.registry, Dynamic: who.own.Dynamic, Draft: true, Agent: true}
	req.Create = &app.CreateFrom{Path: p.Ref, Owner: a.Owner, Namespace: a.Namespace, Repository: a.Repository, Revision: a.Revision,
		Caller: who.person.User, Offerings: mcpPicks(a.Offerings), Consumable: consumable, ConsumablePartial: partial, ConsumableAsOf: asOf,
		Trust: r.trust, ReviewURL: r.reviewURL(p.Key)}
	return req, nil, nil
}

// reviewURL is the dashboard page a person reviews the golden path key's
// created drafts at, "" when the server links no dashboard.
func (r remoteCreator) reviewURL(key fleet.ServiceKey) string {
	if r.fleet == nil || r.fleet.Opts.DashboardURL == "" {
		return ""
	}
	return strings.TrimRight(r.fleet.Opts.DashboardURL, "/") + "/#/fleet/services/" + url.PathEscape(string(key))
}

// mcpPicks is the offering values of pacto_create_service's arguments.
func mcpPicks(in []pactomcp.CreateServiceOffering) []app.OfferingPick {
	out := make([]app.OfferingPick, 0, len(in))
	for _, o := range in {
		out = append(out, app.OfferingPick(o))
	}
	return out
}

// createAnswer is a create plan as pacto_create_service's JSON, a refused
// create as its code, message and command with every refused input under
// refusals.
func createAnswer(p *app.DeclarePlan, err error) (json.RawMessage, *pactomcp.Refused, error) {
	var de *app.DeclareError
	if errors.As(err, &de) && len(de.Refusals) > 0 {
		raw, _ := json.Marshal(map[string]any{"refusals": de.Refusals}) // ponytail: refusals hold JSON values
		return raw, &pactomcp.Refused{Code: pactomcp.WriteRefusal(de.Code), Message: de.Message, CLI: de.CLI}, nil
	}
	return declareAnswer(p, err)
}

// selfMayCreateService refuses a caller the apiserver does not allow to
// create services.pacto.trianalab.io named service in ns: a
// SelfSubjectAccessReview with the caller's own client dyn. A review that
// failed or could not be evaluated refuses too.
func selfMayCreateService(ctx context.Context, dyn dynamic.Interface, ns, service string) *pactomcp.Refused {
	res := authn.ServiceCreateResource
	review := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": map[string]any{"namespace": ns, "verb": res.Verb, "group": res.Group, "resource": res.Resource, "name": service}}}}
	out, err := dyn.Resource(gvrSelfAccess).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return &pactomcp.Refused{Code: pactomcp.RefusalReviewFailed, Message: "asking whether you may create services in " + ns + " failed: " + err.Error()}
	}
	allowed, _, _ := unstructured.NestedBool(out.Object, "status", "allowed")
	evalErr, _, _ := unstructured.NestedString(out.Object, "status", "evaluationError")
	switch {
	case !allowed && evalErr != "":
		return &pactomcp.Refused{Code: pactomcp.RefusalReviewFailed, Message: "asking whether you may create services in " + ns + " could not be evaluated: " + evalErr}
	case !allowed:
		return &pactomcp.Refused{Code: pactomcp.RefusalNotAllowed, Message: "you may not create services in " + ns}
	}
	return nil
}
