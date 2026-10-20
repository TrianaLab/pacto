package dashboard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// DeliveryAPI is Pacto delivery as one caller sees it. A live host binds it to
// the caller's own cluster identity (the signed-in person's token, or the
// kubeconfig in local mode), so every read and every action is the caller's
// and the apiserver decides what they may do. This package never reaches a
// cluster itself.
type DeliveryAPI interface {
	WhoAmI(ctx context.Context) (delivery.Caller, error)
	// Stages and Operations list one namespace, or all of them for "".
	Stages(ctx context.Context, namespace string) ([]delivery.StageObject, error)
	Operations(ctx context.Context, namespace string) ([]delivery.OperationObject, error)
	Get(ctx context.Context, namespace, name string) (*delivery.OperationObject, error)
	Explain(ctx context.Context, namespace, name string) (*delivery.Explanation, error)
	History(ctx context.Context, namespace, stage string) (*delivery.History, error)
	Plan(ctx context.Context, req delivery.Request) (delivery.Plan, error)
	Propose(ctx context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error)
	Approve(ctx context.Context, namespace, name string, decision delivery.Decision, note string) (string, error)
	Cancel(ctx context.Context, namespace, name string) error
	// Describe reads what a Stage's artifact digest carries: its contract and
	// version. A digest never changes, so a host may cache the answer.
	Describe(ctx context.Context, stage *delivery.StageObject, digest string) delivery.Revision
}

// Auth is a live host's identity layer. Nil means no sign-in and no delivery
// decisions.
type Auth struct {
	// Local: the process acts as the kubeconfig user; decisions are never
	// served there.
	Local bool
	// Login: people sign in (shared mode with OpenID Connect).
	Login bool
	// Routes serves /auth/*.
	Routes http.Handler
	// Middleware wraps every request: it resolves the caller and refuses
	// what the mode does not allow.
	Middleware func(http.Handler) http.Handler
	// State reads the caller the middleware resolved.
	State func(*http.Request) AuthState
}

// AuthState is one request's caller as the identity layer sees it.
type AuthState struct {
	SignedIn bool
	// Fresh: the session came from a login the person had to repeat.
	Fresh bool
	CSRF  string
}

// DeliveryError is a delivery answer that says no: the HTTP status, a stable
// code the UI acts on (FreshLoginRequired sends the browser to a fresh login)
// and the reason, verbatim when the apiserver gave it.
type DeliveryError struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
	// Plan is the fresh plan of a PlanStale refusal.
	Plan *delivery.Plan `json:"plan,omitempty"`
}

func (e *DeliveryError) Error() string { return e.Code + ": " + e.Detail }

// GetStatus is the HTTP status huma answers with.
func (e *DeliveryError) GetStatus() int { return e.Status }

// Refuse builds a DeliveryError.
func Refuse(status int, code, detail string) *DeliveryError {
	return &DeliveryError{Status: status, Code: code, Detail: detail}
}

type requestKey struct{}

// requestFrom returns the HTTP request an operation runs for; huma hands
// handlers only its context.
func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return r
}

func withRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, r)))
	})
}

func (s *Server) deliveryInstalled(ctx context.Context) bool {
	return s.opts.DeliveryInstalled == nil || s.opts.DeliveryInstalled(ctx)
}

// deliveryFor is the caller's delivery API, or the refusal that says why
// there is none.
func (s *Server) deliveryFor(ctx context.Context) (DeliveryAPI, error) {
	if !s.deliveryInstalled(ctx) {
		return nil, Refuse(http.StatusNotFound, "DeliveryNotInstalled",
			"this cluster does not serve delivery.pacto.trianalab.io/v1alpha1; install the pacto-delivery chart")
	}
	return s.opts.Delivery(requestFrom(ctx))
}

func (s *Server) authState(ctx context.Context) AuthState {
	if s.opts.Auth == nil || s.opts.Auth.State == nil {
		return AuthState{}
	}
	return s.opts.Auth.State(requestFrom(ctx))
}

// ── session ────────────────────────────────────────────────────────────

type sessionOutput struct {
	Body struct {
		Mode     string `json:"mode" enum:"local,shared,none" doc:"local: the kubeconfig user, no decisions; shared: sign-in required for delivery; none: no identity layer"`
		Login    bool   `json:"login" doc:"People sign in with the identity provider"`
		SignedIn bool   `json:"signedIn" doc:"The caller has a live session (shared) or the launch cookie (local)"`
		Fresh    bool   `json:"fresh" doc:"The session came from a login the person had to repeat; decisions need one"`
		CSRF     string `json:"csrf,omitempty" doc:"Send it in the X-Pacto-CSRF header of every mutating request"`
		// Caller is who the apiserver says the caller is, when delivery is
		// installed and the caller is signed in.
		Caller      *delivery.Caller `json:"caller,omitempty"`
		CallerError string           `json:"callerError,omitempty"`
	}
}

func (s *Server) session(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
	out := &sessionOutput{}
	out.Body.Mode = "none"
	if a := s.opts.Auth; a != nil {
		out.Body.Mode, out.Body.Login = "shared", a.Login
		if a.Local {
			out.Body.Mode = "local"
		}
	}
	st := s.authState(ctx)
	out.Body.SignedIn, out.Body.Fresh, out.Body.CSRF = st.SignedIn, st.Fresh, st.CSRF
	if !st.SignedIn || s.opts.Delivery == nil || !s.deliveryInstalled(ctx) {
		return out, nil
	}
	api, err := s.opts.Delivery(requestFrom(ctx))
	if err == nil {
		var c delivery.Caller
		if c, err = api.WhoAmI(ctx); err == nil {
			out.Body.Caller = &c
		}
	}
	if err != nil {
		out.Body.CallerError = err.Error()
	}
	return out, nil
}

// ── registration ───────────────────────────────────────────────────────

func (s *Server) registerDeliveryOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "session", Method: http.MethodGet, Path: "/api/session", Tags: []string{"Delivery"},
		Summary:     "Who is signed in",
		Description: "The identity mode, whether the caller is signed in and with a fresh login, the CSRF token for mutating requests and who the apiserver says the caller is.",
	}, s.session)
	if s.opts.Delivery == nil {
		return
	}
	reg := func(op huma.Operation) huma.Operation {
		op.Tags = []string{"Delivery"}
		op.Errors = []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict}
		return op
	}
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-inbox", Method: http.MethodGet, Path: "/api/delivery/inbox",
		Summary: "What waits on me",
		Description: "Held operations whose question names one of the caller's teams, with the decision asked and the affected services; " +
			"frozen Stages and Errored operations of the caller's services."}), s.deliveryInbox)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-environments", Method: http.MethodGet, Path: "/api/delivery/environments",
		Summary:     "Every Stage",
		Description: "Service by environment: the digest and version each Stage runs and whether it is verified, failed, frozen, held or on auto-hold."}), s.deliveryEnvironments)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-published", Method: http.MethodGet, Path: "/api/delivery/published",
		Summary:     "Published versions",
		Description: "Per service the caller can see Stages of: the versions its Stages know, whether Flux verified their signature and which Stages run or verified each."}), s.deliveryPublished)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-history", Method: http.MethodGet, Path: "/api/delivery/history",
		Summary:     "What happened",
		Description: "The operations still in the cluster, per Stage, in stream order. Partial until signed records exist."}), s.deliveryHistory)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-operation", Method: http.MethodGet, Path: "/api/delivery/operations/{namespace}/{name}",
		Summary:     "One change",
		Description: "The explained operation (summary, verdicts with how to re-run each), the operation itself (plan, approvals, times) and the actions the caller may take."}), s.deliveryOperation)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-plan", Method: http.MethodPost, Path: "/api/delivery/plan",
		Summary: "Plan a change", Description: "What the promoter would do: verdicts, whether a person must decide, who and the question."}), s.deliveryPlan)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-propose", Method: http.MethodPost, Path: "/api/delivery/operations",
		DefaultStatus: http.StatusCreated, Summary: "Propose a change",
		Description: "Promote, roll back (with undoes) or unfreeze, as the caller. Pass the plan's intent digest: anything that moved since fails with PlanStale and the fresh plan. Override stays in the CLI, for break-glass."}), s.deliveryPropose)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-decide", Method: http.MethodPost, Path: "/api/delivery/operations/{namespace}/{name}/decision",
		DefaultStatus: http.StatusCreated, Summary: "Decide on a held change",
		Description: "Approve, reject or accept an unknown, as the signed-in person, with a login made fresh for it. Only the teams the question names may decide. Never served in local mode or to agents."}), s.deliveryDecide)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-cancel", Method: http.MethodDelete, Path: "/api/delivery/operations/{namespace}/{name}",
		Summary: "Cancel a change", Description: "Cancel an operation that has not moved the pointer yet."}), s.deliveryCancel)
}

// ── reads ──────────────────────────────────────────────────────────────

// Listing is how complete a list of delivery objects is: a namespace the
// caller may not list is left out and named.
type Listing struct {
	AsOf         string   `json:"asOf"`
	Completeness string   `json:"completeness" enum:"complete,partial"`
	Unreadable   []string `json:"unreadable,omitempty" doc:"Namespaces the caller may not list, or * when the namespaces could not be known"`
}

func forbidden(err error) bool {
	var de *DeliveryError
	return errors.As(err, &de) && de.Status == http.StatusForbidden
}

// lists reads every Stage and Operation the caller can see: cluster-wide when
// allowed, else per namespace of the fleet snapshot, dropping (and naming) a
// namespace that refuses.
// ponytail: a list per request with the caller's token, polled by the UI;
// add a per-session informer if list load shows on the apiserver.
func (s *Server) lists(ctx context.Context, api DeliveryAPI) ([]delivery.StageObject, []delivery.OperationObject, Listing, error) {
	l := Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}
	stages, err := api.Stages(ctx, "")
	var ops []delivery.OperationObject
	if err == nil {
		if ops, err = api.Operations(ctx, ""); err == nil {
			return stages, ops, l, nil
		}
	}
	if !forbidden(err) {
		return nil, nil, l, err
	}
	stages, ops, l.Completeness = nil, nil, "partial"
	namespaces := s.namespaces(ctx)
	if namespaces == nil {
		l.Unreadable = []string{"*"}
	}
	for _, ns := range namespaces {
		st, err := api.Stages(ctx, ns)
		var os []delivery.OperationObject
		if err == nil {
			os, err = api.Operations(ctx, ns)
		}
		switch {
		case forbidden(err):
			l.Unreadable = append(l.Unreadable, ns)
		case err != nil:
			return nil, nil, l, err
		default:
			stages, ops = append(stages, st...), append(ops, os...)
		}
	}
	if len(l.Unreadable) == 0 {
		l.Completeness = "complete"
	}
	return stages, ops, l, nil
}

// namespaces are the scopes of the fleet snapshot's targets; nil when there
// is no snapshot to ask.
func (s *Server) namespaces(ctx context.Context) []string {
	q := s.publishedQuery(ctx)
	if q == nil {
		return nil
	}
	out := []string{}
	for _, t := range q.SnapshotForSerialization().Targets {
		if t.Scope != "" && !slices.Contains(out, t.Scope) {
			out = append(out, t.Scope)
		}
	}
	slices.Sort(out)
	return out
}

// serviceOwners maps a service name to its declared owner, from the fleet
// snapshot.
func (s *Server) serviceOwners(ctx context.Context) map[string]string {
	out := map[string]string{}
	if q := s.publishedQuery(ctx); q != nil {
		for _, svc := range q.SnapshotForSerialization().Services {
			if o := svc.Owner.DisplayString(); o != "" {
				out[svc.Name] = o
			}
		}
	}
	return out
}

// InboxItem is one operation waiting on someone.
type InboxItem struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Stage     string            `json:"stage"`
	Type      string            `json:"type"`
	From      string            `json:"from,omitempty"`
	To        string            `json:"to,omitempty"`
	Phase     delivery.Phase    `json:"phase"`
	Outcome   delivery.Outcome  `json:"outcome,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Question  string            `json:"question,omitempty"`
	Decision  delivery.Decision `json:"decision,omitempty" doc:"The decision asked for: approve, or accept-unknown"`
	Owners    []string          `json:"owners,omitempty" doc:"The teams that may answer; empty means any person"`
	Consumers []string          `json:"consumers,omitempty" doc:"The services that made a person necessary"`
}

func inboxItem(o delivery.OperationObject) InboxItem {
	it := InboxItem{Namespace: o.Metadata.Namespace, Name: o.Metadata.Name, Stage: o.Spec.Stage, Type: o.Spec.Type,
		From: o.Spec.Params.From, To: o.Spec.Params.To, Phase: o.Status.Phase, Outcome: o.Status.Outcome, Reason: o.Status.Reason,
		Question: o.Status.Question}
	if p := o.Status.Plan; p != nil {
		it.Decision, it.Owners, it.Consumers = p.Floor.Decision, p.Floor.Owners, p.Floor.Consumers
	}
	if it.Phase == delivery.PhaseHeld && (it.Decision == "" || o.Status.Applied) {
		it.Decision = delivery.AcceptUnknown // held after it shipped: the verify gates stayed Unknown
	}
	return it
}

type inboxOutput struct {
	Body struct {
		Listing
		Held    []InboxItem      `json:"held" doc:"Held operations whose question names one of the caller's teams"`
		Frozen  []EnvironmentRow `json:"frozen" doc:"Frozen Stages of the caller's services"`
		Errored []InboxItem      `json:"errored" doc:"Errored operations of the caller's services"`
	}
}

func (s *Server) deliveryInbox(ctx context.Context, _ *struct{}) (*inboxOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	stages, ops, l, err := s.lists(ctx, api)
	if err != nil {
		return nil, err
	}
	owners := s.serviceOwners(ctx)
	// A service whose owner the snapshot does not know is shown: an inbox
	// that hides what it cannot attribute hides work.
	mine := func(service string) bool {
		o, known := owners[service]
		return !known || s.opts.DeliveryPolicy.InTeams(caller.Groups, []string{o})
	}
	service := map[string]string{}
	out := &inboxOutput{}
	out.Body.Listing, out.Body.Held, out.Body.Frozen, out.Body.Errored = l, []InboxItem{}, []EnvironmentRow{}, []InboxItem{}
	for _, st := range stages {
		service[st.Metadata.Namespace+"/"+st.Metadata.Name] = st.Spec.Service.Name
		if st.Status.Frozen && mine(st.Spec.Service.Name) {
			out.Body.Frozen = append(out.Body.Frozen, s.environmentRow(ctx, api, st, nil, caller))
		}
	}
	for _, o := range ops {
		it := inboxItem(o)
		switch {
		case it.Phase == delivery.PhaseHeld && s.opts.DeliveryPolicy.InTeams(caller.Groups, it.Owners):
			out.Body.Held = append(out.Body.Held, it)
		case it.Outcome == delivery.Errored && mine(service[it.Namespace+"/"+it.Stage]):
			out.Body.Errored = append(out.Body.Errored, it)
		}
	}
	return out, nil
}

// EnvironmentRow is one Stage: one service in one environment.
type EnvironmentRow struct {
	Namespace    string   `json:"namespace"`
	Stage        string   `json:"stage"`
	Service      string   `json:"service"`
	Environment  string   `json:"environment"`
	Pointer      string   `json:"pointer,omitempty" doc:"The digest the environment runs"`
	Version      string   `json:"version,omitempty" doc:"The version its contract declares, when the artifact could be read"`
	Verified     bool     `json:"verified" doc:"The running digest passed its verify gates here"`
	Failed       bool     `json:"failed" doc:"The running digest failed here"`
	Frozen       bool     `json:"frozen"`
	FrozenReason string   `json:"frozenReason,omitempty"`
	AutoHold     bool     `json:"autoHold"`
	Held         int      `json:"held" doc:"Operations on this Stage waiting for a person"`
	Candidate    string   `json:"candidate,omitempty" doc:"The newest eligible candidate"`
	Ready        string   `json:"ready,omitempty" doc:"The Ready condition: True, or False with the reason the Stage cannot promote"`
	Actions      []string `json:"actions" doc:"What the caller's class may ask for here: promote, unfreeze"`
}

func (s *Server) environmentRow(ctx context.Context, api DeliveryAPI, st delivery.StageObject, ops []delivery.OperationObject, caller delivery.Caller) EnvironmentRow {
	ss := st.Status
	row := EnvironmentRow{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Service: st.Spec.Service.Name, Environment: st.Spec.Environment,
		Pointer: ss.Pointer, Verified: slices.Contains(ss.Verified, ss.Pointer), Failed: slices.Contains(ss.FailedDigests, ss.Pointer),
		Frozen: ss.Frozen, FrozenReason: ss.FrozenReason, AutoHold: ss.AutoHold, Candidate: ss.Candidate, Actions: []string{}}
	for _, c := range ss.Conditions {
		if c.Type == "Ready" {
			row.Ready = strings.TrimSuffix(c.Status+": "+c.Reason, ": ")
		}
	}
	if ss.Pointer != "" {
		if c := api.Describe(ctx, &st, ss.Pointer).Bundle; c != nil {
			row.Version = c.Contract.Service.Version
		}
	}
	for _, o := range ops {
		if o.Metadata.Namespace == row.Namespace && o.Spec.Stage == row.Stage && o.Status.Phase == delivery.PhaseHeld {
			row.Held++
		}
	}
	if delivery.MayRaise(delivery.TypePromote, caller.Class, false) && ss.Candidate != "" && ss.Candidate != ss.Pointer {
		row.Actions = append(row.Actions, "promote")
	}
	if (ss.Frozen || ss.AutoHold) && delivery.MayRaise(delivery.TypeUnfreeze, caller.Class, false) {
		row.Actions = append(row.Actions, "unfreeze")
	}
	return row
}

type environmentsOutput struct {
	Body struct {
		Listing
		Environments []EnvironmentRow `json:"environments"`
	}
}

func (s *Server) deliveryEnvironments(ctx context.Context, _ *struct{}) (*environmentsOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	stages, ops, l, err := s.lists(ctx, api)
	if err != nil {
		return nil, err
	}
	out := &environmentsOutput{}
	out.Body.Listing, out.Body.Environments = l, []EnvironmentRow{}
	for _, st := range stages {
		out.Body.Environments = append(out.Body.Environments, s.environmentRow(ctx, api, st, ops, caller))
	}
	slices.SortFunc(out.Body.Environments, func(a, b EnvironmentRow) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Environment, b.Environment), cmp.Compare(a.Namespace, b.Namespace))
	})
	return out, nil
}

// PublishedVersion is one digest a service's Stages know about.
type PublishedVersion struct {
	Digest   string `json:"digest"`
	Version  string `json:"version,omitempty"`
	Contract string `json:"contract,omitempty" doc:"The digest-pinned contract the artifact names (pacto.dev/contract)"`
	// Signed: Flux verified its signature on a candidate source, so pacto
	// publish published it.
	Signed bool                `json:"signed"`
	Stages []PublishedOnStages `json:"stages"`
}

// PublishedOnStages is where one version stands on one Stage.
type PublishedOnStages struct {
	Namespace   string `json:"namespace"`
	Stage       string `json:"stage"`
	Environment string `json:"environment"`
	Running     bool   `json:"running"`
	Verified    bool   `json:"verified"`
	Failed      bool   `json:"failed"`
}

// PublishedService is the versions of one service.
type PublishedService struct {
	Service  string             `json:"service"`
	Versions []PublishedVersion `json:"versions"`
}

type publishedOutput struct {
	Body struct {
		Listing
		Services []PublishedService `json:"services"`
	}
}

// deliveryPublished reads the versions from what the Stages remember:
// ponytail: digests the caller's Stages know (published, offered, verified,
// failed, running), not every bundle tag in the registry; list registry tags
// when someone needs versions no Stage has seen.
func (s *Server) deliveryPublished(ctx context.Context, _ *struct{}) (*publishedOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	stages, _, l, err := s.lists(ctx, api)
	if err != nil {
		return nil, err
	}
	byService := map[string]map[string]*PublishedVersion{}
	for _, st := range stages {
		ss := st.Status
		svc := st.Spec.Service.Name
		if byService[svc] == nil {
			byService[svc] = map[string]*PublishedVersion{}
		}
		for _, d := range slices.Concat(ss.Published, ss.Offered, ss.Verified, ss.FailedDigests, []string{ss.Pointer}) {
			if d == "" {
				continue
			}
			v := byService[svc][d]
			if v == nil {
				r := api.Describe(ctx, &st, d)
				v = &PublishedVersion{Digest: d, Contract: r.ContractRef, Stages: []PublishedOnStages{}}
				if r.Bundle != nil {
					v.Version = r.Bundle.Contract.Service.Version
				}
				byService[svc][d] = v
			}
			v.Signed = v.Signed || slices.Contains(ss.Published, d)
			on := PublishedOnStages{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment,
				Running: ss.Pointer == d, Verified: slices.Contains(ss.Verified, d), Failed: slices.Contains(ss.FailedDigests, d)}
			if !slices.Contains(v.Stages, on) {
				v.Stages = append(v.Stages, on)
			}
		}
	}
	out := &publishedOutput{}
	out.Body.Listing, out.Body.Services = l, []PublishedService{}
	for svc, versions := range byService {
		ps := PublishedService{Service: svc, Versions: []PublishedVersion{}}
		for _, v := range versions {
			ps.Versions = append(ps.Versions, *v)
		}
		slices.SortFunc(ps.Versions, func(a, b PublishedVersion) int {
			return cmp.Or(cmp.Compare(b.Version, a.Version), cmp.Compare(a.Digest, b.Digest))
		})
		out.Body.Services = append(out.Body.Services, ps)
	}
	slices.SortFunc(out.Body.Services, func(a, b PublishedService) int { return cmp.Compare(a.Service, b.Service) })
	return out, nil
}

type historyInput struct {
	Namespace string `query:"namespace" doc:"Namespace of one Stage (with stage); empty lists every Stage the caller can see"`
	Stage     string `query:"stage" doc:"One Stage's name"`
}

type historyOutput struct {
	Body struct {
		Listing
		Histories []delivery.History `json:"histories"`
	}
}

func (s *Server) deliveryHistory(ctx context.Context, in *historyInput) (*historyOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	out := &historyOutput{}
	out.Body.Listing = Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}
	targets := [][2]string{{in.Namespace, in.Stage}}
	if in.Namespace == "" || in.Stage == "" {
		stages, _, l, err := s.lists(ctx, api)
		if err != nil {
			return nil, err
		}
		out.Body.Listing, targets = l, nil
		for _, st := range stages {
			targets = append(targets, [2]string{st.Metadata.Namespace, st.Metadata.Name})
		}
	}
	out.Body.Histories = []delivery.History{}
	for _, t := range targets {
		h, err := api.History(ctx, t[0], t[1])
		if err != nil {
			return nil, err
		}
		out.Body.Histories = append(out.Body.Histories, *h)
	}
	return out, nil
}

type operationInput struct {
	Namespace string `path:"namespace"`
	Name      string `path:"name"`
}

type operationOutput struct {
	Body struct {
		Explanation *delivery.Explanation     `json:"explanation"`
		Operation   *delivery.OperationObject `json:"operation"`
		Actions     []string                  `json:"actions" doc:"What the caller may ask for: approve, reject, accept-unknown, cancel, rollback"`
	}
}

func (s *Server) deliveryOperation(ctx context.Context, in *operationInput) (*operationOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	e, err := api.Explain(ctx, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	op, err := api.Get(ctx, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	out := &operationOutput{}
	out.Body.Explanation, out.Body.Operation = e, op
	out.Body.Actions = s.actions(ctx, op, e.Stage, caller)
	return out, nil
}

// actions are what the UI offers on an operation. They only hide what the
// caller cannot do; the apiserver and the promoter still decide.
func (s *Server) actions(ctx context.Context, op *delivery.OperationObject, stage delivery.StageNow, caller delivery.Caller) []string {
	acts := []string{}
	st := op.Status
	if st.Phase == delivery.PhaseHeld && s.decisionsServed(ctx) && s.opts.DeliveryPolicy.IsHuman(caller.Identity) &&
		s.opts.DeliveryPolicy.InTeams(caller.Groups, inboxItem(*op).Owners) {
		if inboxItem(*op).Decision == delivery.Approve {
			acts = append(acts, string(delivery.Approve))
		} else {
			acts = append(acts, string(delivery.AcceptUnknown))
		}
		acts = append(acts, string(delivery.Reject))
	}
	if st.Phase != delivery.PhaseConcluded && !st.Applied {
		acts = append(acts, "cancel")
	}
	if st.Phase == delivery.PhaseConcluded && st.Applied && st.Pointer.After != "" && st.Pointer.After == stage.Pointer &&
		delivery.MayRaise(delivery.TypeRollback, caller.Class, false) {
		acts = append(acts, "rollback")
	}
	return acts
}

// decisionsServed: decisions only in shared mode, from a signed-in browser.
func (s *Server) decisionsServed(ctx context.Context) bool {
	a := s.opts.Auth
	return a != nil && !a.Local && a.Login && s.authState(ctx).SignedIn
}

// ── actions ────────────────────────────────────────────────────────────

type planInput struct {
	Body delivery.Request
}

type planOutput struct {
	Body delivery.Plan
}

func (s *Server) deliveryPlan(ctx context.Context, in *planInput) (*planOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := api.Plan(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &planOutput{Body: p}, nil
}

type proposeOutput struct {
	Body struct {
		Operation *delivery.OperationObject `json:"operation"`
		Plan      delivery.Plan             `json:"plan"`
	}
}

func (s *Server) deliveryPropose(ctx context.Context, in *planInput) (*proposeOutput, error) {
	if in.Body.Type == delivery.TypeOverride {
		return nil, Refuse(http.StatusBadRequest, "OverrideFromCLI", "an override is break-glass: raise it with `pacto delivery propose --type override`")
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	op, p, err := api.Propose(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	out := &proposeOutput{}
	out.Body.Operation, out.Body.Plan = op, p
	return out, nil
}

type decideInput struct {
	Namespace string `path:"namespace"`
	Name      string `path:"name"`
	Body      struct {
		Decision delivery.Decision `json:"decision" enum:"approve,reject,accept-unknown"`
		Note     string            `json:"note,omitempty"`
	}
}

type decideOutput struct {
	Body struct {
		Approval string `json:"approval" doc:"The Approval created; the promoter consumes it"`
	}
}

// deliveryDecide is the fresh-login decision flow: local mode never decides,
// a decision needs a session from a login the person repeated with an
// auth-time under DecisionFreshness, agents are refused and only the teams
// the question names may answer. The promoter checks the 15 minutes and the
// team again.
func (s *Server) deliveryDecide(ctx context.Context, in *decideInput) (*decideOutput, error) {
	a := s.opts.Auth
	cli := fmt.Sprintf("pacto delivery approve -n %s %s --decision %s", in.Namespace, in.Name, in.Body.Decision)
	switch {
	case a == nil || a.Local:
		return nil, Refuse(http.StatusForbidden, "ApproveFromCLI",
			"a local dashboard never decides: any process on this machine could do it as you. Run: "+cli)
	case !a.Login:
		return nil, Refuse(http.StatusForbidden, "LoginNotConfigured", "this dashboard has no sign-in, so it serves no decisions")
	}
	st := s.authState(ctx)
	switch {
	case !st.SignedIn:
		return nil, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in to decide")
	case !st.Fresh:
		return nil, Refuse(http.StatusUnauthorized, "FreshLoginRequired", "deciding needs a fresh login: sign in again")
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	at, ok := delivery.ParseAuthTime(caller.AuthTime)
	switch {
	case !s.opts.DeliveryPolicy.IsHuman(caller.Identity):
		return nil, Refuse(http.StatusForbidden, "AgentsMayNotApprove", "the apiserver says "+caller.User+" is an agent; agents may not decide")
	case caller.AuthTime == "":
		return nil, Refuse(http.StatusForbidden, "AuthTimeUnavailable",
			"this cluster does not map auth_time to userInfo.extra[pacto.dev/auth-time]; nobody can approve here")
	case !ok || time.Since(at) > delivery.DecisionFreshness:
		return nil, Refuse(http.StatusUnauthorized, "FreshLoginRequired", "your login is older than ten minutes: sign in again")
	}
	op, err := api.Get(ctx, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	if owners := inboxItem(*op).Owners; !s.opts.DeliveryPolicy.InTeams(caller.Groups, owners) {
		return nil, Refuse(http.StatusForbidden, "NotYourTeam", fmt.Sprintf("only %v may answer this question", owners))
	}
	name, err := api.Approve(ctx, in.Namespace, in.Name, in.Body.Decision, in.Body.Note)
	if err != nil {
		return nil, err
	}
	out := &decideOutput{}
	out.Body.Approval = name
	return out, nil
}

type cancelOutput struct {
	Body struct {
		Cancelled string `json:"cancelled"`
	}
}

func (s *Server) deliveryCancel(ctx context.Context, in *operationInput) (*cancelOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := api.Cancel(ctx, in.Namespace, in.Name); err != nil {
		return nil, err
	}
	out := &cancelOutput{}
	out.Body.Cancelled = in.Namespace + "/" + in.Name
	return out, nil
}
