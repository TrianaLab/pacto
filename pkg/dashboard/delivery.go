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

	"github.com/Masterminds/semver/v3"
	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/logging"
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
	Stage(ctx context.Context, namespace, name string) (*delivery.StageObject, error)
	Explain(ctx context.Context, namespace, name string) (*delivery.Explanation, error)
	History(ctx context.Context, namespace, stage string) (*delivery.History, error)
	Plan(ctx context.Context, req delivery.Request) (delivery.Plan, error)
	Propose(ctx context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error)
	Approve(ctx context.Context, namespace, name string, decision delivery.Decision, note string) (string, error)
	Cancel(ctx context.Context, namespace, name string) error
	// Describe reads what a Stage's artifact digest carries: its contract and
	// version. A digest never changes, so a host may cache the answer.
	Describe(ctx context.Context, stage *delivery.StageObject, digest string) delivery.Revision
	// Blame says who put digest (by default the one running) in place on a
	// Stage; Undo plans how to undo an operation and proposes nothing. stage
	// finds an operation that is gone from the cluster in the records.
	Blame(ctx context.Context, namespace, stage, digest string) (*delivery.Blame, error)
	Undo(ctx context.Context, namespace, name, stage string) (*delivery.Undo, error)
	// PublishRefusals are the publishes Pacto refused for services with Stages
	// in namespace ("" for all). Nil with no error means this host keeps no
	// record of refused publishes.
	PublishRefusals(ctx context.Context, namespace string) (*PublishRefusals, error)
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
	// Name is the person's name from their ID token (name, else email).
	Name string
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

func (s *Server) deliveryInstalled(ctx context.Context) (bool, error) {
	if s.opts.DeliveryInstalled == nil {
		return true, nil
	}
	return s.opts.DeliveryInstalled(ctx)
}

// deliveryFor is the caller's delivery API, or the refusal that says why
// there is none.
func (s *Server) deliveryFor(ctx context.Context) (DeliveryAPI, error) {
	switch installed, err := s.deliveryInstalled(ctx); {
	case err != nil:
		return nil, Refuse(http.StatusServiceUnavailable, "ClusterUnreachable",
			"cannot reach the cluster to tell whether it serves delivery: "+err.Error())
	case !installed:
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
		Name     string `json:"name,omitempty" doc:"The signed-in person's name from their ID token (name, else email)"`
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
	out.Body.SignedIn, out.Body.Fresh, out.Body.CSRF, out.Body.Name = st.SignedIn, st.Fresh, st.CSRF, st.Name
	if !st.SignedIn || s.opts.Delivery == nil {
		return out, nil
	}
	api, err := s.deliveryFor(ctx)
	if de := (*DeliveryError)(nil); errors.As(err, &de) && de.Code == "DeliveryNotInstalled" {
		return out, nil
	}
	if err == nil {
		var c delivery.Caller
		if c, err = api.WhoAmI(ctx); err == nil {
			out.Body.Caller = &c
			s.remember(c.User, st.Name)
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
			"frozen Stages of the caller's services and the Errored operations on them."}), s.deliveryInbox)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-environments", Method: http.MethodGet, Path: "/api/delivery/environments",
		Summary:     "Every Stage",
		Description: "Service by environment: the digest and version each Stage runs and whether it is verified, failed, frozen, held or on auto-hold."}), s.deliveryEnvironments)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-published", Method: http.MethodGet, Path: "/api/delivery/published",
		Summary:     "Published versions",
		Description: "Per service the caller can see Stages of: the versions its Stages know, whether Flux verified their signature and which Stages run or verified each."}), s.deliveryPublished)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-history", Method: http.MethodGet, Path: "/api/delivery/history",
		Summary: "What happened",
		Description: "What happened on each Stage: read from its signed records when the recorder runs (source records), plus the operations not " +
			"recorded yet, otherwise from the operations still in the cluster (source status, always partial). Each history carries its own " +
			"completeness, problems and what could not be read."}), s.deliveryHistory)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-blame", Method: http.MethodGet, Path: "/api/delivery/blame",
		Summary: "Who put this version here",
		Description: "The newest change that moved a Stage to a digest (by default the one it runs): who asked, for whom, who approved and what " +
			"every check said, read from its signed record when there is one. No change with a partial completeness means unknown, never nobody."}), s.deliveryBlame)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-undo", Method: http.MethodGet, Path: "/api/delivery/undo",
		Summary: "How to undo a change",
		Description: "A plan nobody has proposed: a rollback bound to the change when the Stage still runs it, a promote back to the version it " +
			"replaced when something changed since, or nothing. It never acts; propose the plan to carry it out."}), s.deliveryUndo)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-refusals", Method: http.MethodGet, Path: "/api/delivery/refusals",
		Summary: "Publishes Pacto refused",
		Description: "Recent publishes Pacto refused, newest first, with the check that refused each and the version bump the change needs. " +
			"recorded false means this cluster keeps no record of refused publishes, so an empty list proves nothing."}), s.deliveryRefusals)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-operation", Method: http.MethodGet, Path: "/api/delivery/operations/{namespace}/{name}",
		Summary:     "One change",
		Description: "The explained operation (summary, verdicts with how to re-run each), the operation itself (plan, approvals, times) and the actions the caller may take."}), s.deliveryOperation)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-plan", Method: http.MethodPost, Path: "/api/delivery/plan",
		Summary: "Plan a change", Description: "What the promoter would do: verdicts, whether a person must decide, who and the question."}), s.deliveryPlan)
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-propose", Method: http.MethodPost, Path: "/api/delivery/operations",
		DefaultStatus: http.StatusCreated, Summary: "Propose a change",
		Description: "Promote, roll back (with undoes), freeze or unfreeze, as the caller. Pass the plan's intent digest: anything that moved since fails with PlanStale and the fresh plan. Override stays in the CLI, for break-glass."}), s.deliveryPropose)
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

// lists reads every Stage and Operation the caller can see: in the
// dashboard's namespace when it has one; else cluster-wide when allowed, else,
// partially, per namespace of the fleet snapshot. A namespace that refuses is
// dropped and named.
// ponytail: a list per request with the caller's token, polled by the UI;
// add a per-session informer if list load shows on the apiserver.
func (s *Server) lists(ctx context.Context, api DeliveryAPI) ([]delivery.StageObject, []delivery.OperationObject, Listing, error) {
	l := Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}
	namespaces := []string{s.opts.DeliveryNamespace}
	if namespaces[0] == "" {
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
		// The namespaces asked are a guess (where the fleet's workloads run,
		// not necessarily where Stages live), so the answer is never
		// complete: "*" says namespaces outside the guess were not read.
		l.Completeness, l.Unreadable, namespaces = "partial", []string{"*"}, s.namespaces(ctx)
	}
	return listEach(ctx, api, namespaces, l)
}

// listEach lists each namespace, dropping (and naming) one that refuses.
func listEach(ctx context.Context, api DeliveryAPI, namespaces []string, l Listing) ([]delivery.StageObject, []delivery.OperationObject, Listing, error) {
	var stages []delivery.StageObject
	var ops []delivery.OperationObject
	for _, ns := range namespaces {
		st, err := api.Stages(ctx, ns)
		var os []delivery.OperationObject
		if err == nil {
			os, err = api.Operations(ctx, ns)
		}
		switch {
		case forbidden(err):
			l.Completeness, l.Unreadable = "partial", append(l.Unreadable, ns)
		case err != nil:
			return nil, nil, l, err
		default:
			stages, ops = append(stages, st...), append(ops, os...)
		}
	}
	return stages, ops, l, nil
}

// inScope refuses a namespace outside the dashboard's own, as if it were
// not there.
func (s *Server) inScope(ns string) error {
	if s.opts.DeliveryNamespace != "" && ns != s.opts.DeliveryNamespace {
		return Refuse(http.StatusNotFound, "OutOfScope", "this dashboard shows only namespace "+s.opts.DeliveryNamespace)
	}
	return nil
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
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Stage     string `json:"stage"`
	Type      string `json:"type"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	// FromVersion and ToVersion are the versions the digests carry, when
	// their artifacts could be read.
	FromVersion string            `json:"fromVersion,omitempty"`
	ToVersion   string            `json:"toVersion,omitempty"`
	Phase       delivery.Phase    `json:"phase"`
	Outcome     delivery.Outcome  `json:"outcome,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Question    string            `json:"question,omitempty"`
	Decision    delivery.Decision `json:"decision,omitempty" doc:"The decision asked for: approve, or accept-unknown"`
	Owners      []string          `json:"owners,omitempty" doc:"The teams that may answer; empty means any person"`
	Consumers   []string          `json:"consumers,omitempty" doc:"The services that made a person necessary"`
	Order       *delivery.Order   `json:"order,omitempty" doc:"What must move first: the consumers whose declared range does not accept the new version, their Stages and any verified version upstream that can go first"`
}

func inboxItem(o delivery.OperationObject) InboxItem {
	it := InboxItem{Namespace: o.Metadata.Namespace, Name: o.Metadata.Name, Stage: o.Spec.Stage, Type: o.Spec.Type,
		From: o.Spec.Params.From, To: o.Spec.Params.To, Phase: o.Status.Phase, Outcome: o.Status.Outcome, Reason: o.Status.Reason,
		Question: o.Status.Question}
	if p := o.Status.Plan; p != nil {
		it.Decision, it.Owners, it.Consumers = p.Floor.Decision, p.Floor.Owners, p.Floor.Consumers
		if !o.Status.Applied { // once it shipped, what had to move first is history
			it.Order = p.Order
		}
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
		Errored []InboxItem      `json:"errored" doc:"Errored operations of the caller's services whose Stage is still frozen"`
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
		return !known || caller.Policy.InTeams(caller.Groups, []string{o})
	}
	// frozen holds the caller's Stages that are paused now. Concluded
	// operations are never deleted, so an Errored one waits on someone only
	// while the pause it caused lasts; after that it is history.
	frozen := map[string]bool{}
	out := &inboxOutput{}
	out.Body.Listing, out.Body.Held, out.Body.Frozen, out.Body.Errored = l, []InboxItem{}, []EnvironmentRow{}, []InboxItem{}
	for _, st := range stages {
		if st.Status.Frozen && mine(st.Spec.Service.Name) {
			frozen[st.Metadata.Namespace+"/"+st.Metadata.Name] = true
			out.Body.Frozen = append(out.Body.Frozen, s.environmentRow(ctx, api, st, nil, caller))
		}
	}
	// Held waits on a person who may answer: not an agent, never its asker.
	decider := caller.Policy.IsHuman(caller.Identity)
	byStage := stagesByName(stages)
	for _, o := range ops {
		it := inboxItem(o)
		switch {
		case it.Phase == delivery.PhaseHeld && decider && !ownChange(&o, caller.User) &&
			caller.Policy.InTeams(caller.Groups, it.Owners):
			out.Body.Held = append(out.Body.Held, withVersions(ctx, api, byStage[it.Namespace+"/"+it.Stage], it))
		case it.Outcome == delivery.Errored && frozen[it.Namespace+"/"+it.Stage]:
			out.Body.Errored = append(out.Body.Errored, it)
		}
	}
	return out, nil
}

// stagesByName indexes Stages by namespace/name.
func stagesByName(stages []delivery.StageObject) map[string]*delivery.StageObject {
	out := map[string]*delivery.StageObject{}
	for i := range stages {
		out[stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name] = &stages[i]
	}
	return out
}

// withVersions names the versions an item moves between, read from its
// Stage's artifacts; nil when the caller cannot see the Stage.
func withVersions(ctx context.Context, api DeliveryAPI, st *delivery.StageObject, it InboxItem) InboxItem {
	if st != nil {
		v := versions(ctx, api, st, nil, it.From, it.To)
		it.FromVersion, it.ToVersion = v[it.From], v[it.To]
	}
	return it
}

// EnvironmentRow is one Stage: one service in one environment.
type EnvironmentRow struct {
	Namespace        string   `json:"namespace"`
	Stage            string   `json:"stage"`
	Service          string   `json:"service"`
	Environment      string   `json:"environment"`
	Pointer          string   `json:"pointer,omitempty" doc:"The digest the environment runs"`
	Version          string   `json:"version,omitempty" doc:"The version its contract declares, when the artifact could be read"`
	Verified         bool     `json:"verified" doc:"The running digest passed its verify gates here"`
	Failed           bool     `json:"failed" doc:"The running digest failed here"`
	Frozen           bool     `json:"frozen"`
	FrozenReason     string   `json:"frozenReason,omitempty"`
	AutoHold         bool     `json:"autoHold"`
	Held             int      `json:"held" doc:"Operations on this Stage waiting for a person"`
	Candidate        string   `json:"candidate,omitempty" doc:"The newest eligible candidate"`
	CandidateVersion string   `json:"candidateVersion,omitempty" doc:"The version the candidate's contract declares, when the artifact could be read"`
	Ready            string   `json:"ready,omitempty" doc:"The Ready condition: True, or False with the reason the Stage cannot promote"`
	Actions          []string `json:"actions" doc:"What the caller's class may ask for here: promote, unfreeze"`
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
	v := versions(ctx, api, &st, nil, ss.Pointer, ss.Candidate)
	row.Version, row.CandidateVersion = v[ss.Pointer], v[ss.Candidate]
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
				v.Version = version(ctx, r)
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
			return cmp.Or(compareVersions(b.Version, a.Version), cmp.Compare(a.Digest, b.Digest))
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

// StageHistory is one Stage's history and the namespace the Stage lives in,
// which the history itself does not carry.
type StageHistory struct {
	Namespace string `json:"namespace"`
	delivery.History
}

type historyOutput struct {
	Body struct {
		Listing
		Histories []StageHistory `json:"histories"`
		Labels
	}
}

// Labels say in words what the identifiers in an answer stand for.
type Labels struct {
	Versions map[string]string `json:"versions,omitempty" doc:"The version each digest named here carries, when its artifact could be read"`
	People   map[string]string `json:"people,omitempty" doc:"The names of the people named here who signed in to this dashboard, by username"`
}

func (s *Server) deliveryHistory(ctx context.Context, in *historyInput) (*historyOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	out := &historyOutput{}
	out.Body.Listing = Listing{AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: "complete"}
	var targets []delivery.StageObject
	// describe: the Stages were read, so their artifacts can be asked for
	// versions. A Stage that cannot be read still has a history to ask for.
	describe := true
	if in.Namespace != "" && in.Stage != "" {
		if err := s.inScope(in.Namespace); err != nil {
			return nil, err
		}
		st, err := api.Stage(ctx, in.Namespace, in.Stage)
		if err != nil {
			st, describe = &delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: in.Namespace, Name: in.Stage}}, false
		}
		targets = []delivery.StageObject{*st}
	} else {
		stages, _, l, err := s.lists(ctx, api)
		if err != nil {
			return nil, err
		}
		out.Body.Listing, targets = l, stages
	}
	out.Body.Histories = []StageHistory{}
	out.Body.Versions = map[string]string{}
	var actors []string
	for _, t := range targets {
		h, err := api.History(ctx, t.Metadata.Namespace, t.Metadata.Name)
		if err != nil {
			return nil, err
		}
		digests := []string{h.Stage.Pointer}
		for _, e := range h.Entries {
			digests, actors = append(digests, e.From, e.To), append(actors, e.Actor)
		}
		if describe {
			versions(ctx, api, &t, out.Body.Versions, digests...)
		}
		out.Body.Histories = append(out.Body.Histories, StageHistory{Namespace: t.Metadata.Namespace, History: *h})
	}
	out.Body.People = s.named(actors...)
	return out, nil
}

// versions adds to into (a new map when nil) the version each digest's
// contract declares, leaving out what cannot be read.
// ponytail: a digest that cannot be read is asked again each time it is
// named; the host caches only what it read.
func versions(ctx context.Context, api DeliveryAPI, stage *delivery.StageObject, into map[string]string, digests ...string) map[string]string {
	if into == nil {
		into = map[string]string{}
	}
	for _, d := range digests {
		if _, known := into[d]; known || d == "" {
			continue
		}
		if v := version(ctx, api.Describe(ctx, stage, d)); v != "" {
			into[d] = v
		}
	}
	return into
}

// version is the version a revision's contract declares; "" when it has
// none, with the reason logged when its artifact could not be read. An
// artifact that names no contract is not a failure and logs nothing.
func version(ctx context.Context, r delivery.Revision) string {
	if r.Bundle == nil {
		if r.Err != "" && !r.NoContract {
			logging.LoggerFromContext(ctx).Warn("reading the version of a digest", "digest", r.Digest, "error", r.Err)
		}
		return ""
	}
	return r.Bundle.Contract.Service.Version
}

// stageVersions is versions for the digests of a change on a Stage read by
// name; nil, with the reason logged, when the Stage cannot be read.
func stageVersions(ctx context.Context, api DeliveryAPI, namespace, stage string, digests ...string) map[string]string {
	st, err := api.Stage(ctx, namespace, stage)
	if err != nil {
		logging.LoggerFromContext(ctx).Warn("reading a Stage to name versions", "namespace", namespace, "stage", stage, "error", err)
		return nil
	}
	return versions(ctx, api, st, nil, digests...)
}

// maxPeople bounds the names a dashboard remembers.
// ponytail: the names of the people who signed in to this process, forgotten
// when full or on restart; ask the identity provider's directory if teams
// need the names of people who never opened the dashboard.
const maxPeople = 1024

// remember keeps the name of a person who signed in.
func (s *Server) remember(user, name string) {
	if user == "" || name == "" {
		return
	}
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	if s.people == nil || len(s.people) >= maxPeople {
		s.people = map[string]string{}
	}
	s.people[user] = name
}

// named are the names this dashboard knows of users.
func (s *Server) named(users ...string) map[string]string {
	s.peopleMu.Lock()
	defer s.peopleMu.Unlock()
	out := map[string]string{}
	for _, u := range users {
		if n, ok := s.people[u]; ok {
			out[u] = n
		}
	}
	return out
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
		Labels
	}
}

func (s *Server) deliveryOperation(ctx context.Context, in *operationInput) (*operationOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
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
	out.Body.Versions = stageVersions(ctx, api, in.Namespace, op.Spec.Stage, op.Spec.Params.From, op.Spec.Params.To,
		op.Status.Pointer.Before, op.Status.Pointer.After, e.Stage.Pointer)
	people := []string{op.Spec.Actor.User}
	if op.Spec.OnBehalfOf != nil {
		people = append(people, op.Spec.OnBehalfOf.User)
	}
	for _, a := range op.Status.Approvals {
		people = append(people, a.By)
	}
	out.Body.People = s.named(people...)
	return out, nil
}

// ── blame, undo and refused publishes ──────────────────────────────────

type blameInput struct {
	Namespace string `query:"namespace" required:"true" doc:"The Stage's namespace"`
	Stage     string `query:"stage" required:"true" doc:"The Stage's name"`
	Digest    string `query:"digest" doc:"The digest to blame; empty means the one the Stage runs"`
}

type blameOutput struct {
	Body struct {
		delivery.Blame
		Labels
	}
}

func (s *Server) deliveryBlame(ctx context.Context, in *blameInput) (*blameOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	b, err := api.Blame(ctx, in.Namespace, in.Stage, in.Digest)
	if err != nil {
		return nil, err
	}
	out := &blameOutput{}
	out.Body.Blame = *b
	digests, people := []string{b.Digest, b.Stage.Pointer}, []string{b.OnBehalfOf}
	if c := b.Change; c != nil {
		digests, people = append(digests, c.From, c.To), append(people, c.Actor)
	}
	for _, a := range b.Approvals {
		people = append(people, a.By)
	}
	out.Body.Versions = stageVersions(ctx, api, in.Namespace, in.Stage, digests...)
	out.Body.People = s.named(people...)
	return out, nil
}

type undoInput struct {
	Namespace string `query:"namespace" required:"true" doc:"The operation's namespace"`
	Name      string `query:"name" required:"true" doc:"The operation to undo"`
	Stage     string `query:"stage" doc:"Its Stage, to find it in the records when it is gone from the cluster"`
}

type undoOutput struct {
	Body struct {
		delivery.Undo
		Labels
	}
}

func (s *Server) deliveryUndo(ctx context.Context, in *undoInput) (*undoOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	u, err := api.Undo(ctx, in.Namespace, in.Name, in.Stage)
	if err != nil {
		return nil, err
	}
	out := &undoOutput{}
	out.Body.Undo = *u
	if p := u.Plan; p != nil && in.Stage != "" {
		out.Body.Versions = stageVersions(ctx, api, in.Namespace, in.Stage, p.From, p.To)
	}
	return out, nil
}

// PublishRefusal is one publish Pacto refused.
type PublishRefusal struct {
	Service string `json:"service"`
	Version string `json:"version" doc:"The version the publish asked for"`
	// Check is the check that refused it: version (the version rule), sign,
	// lock, validation or conflict.
	Check  string `json:"check" doc:"The check that refused it: version, sign, lock, validation or conflict"`
	Reason string `json:"reason" doc:"Why, in one sentence"`
	// Baseline and RequiredBump are set when the version rule refused it.
	Baseline     string `json:"baseline,omitempty" doc:"The highest published version below it, the one the change was compared with"`
	RequiredBump string `json:"requiredBump,omitempty" enum:"major,minor,patch" doc:"The smallest bump the change needs"`
	Actor        string `json:"actor,omitempty" doc:"Who ran the publish, as the cluster or the record names them"`
	At           string `json:"at" doc:"When it was refused (RFC 3339)"`
	// Verified: a signed record of the refusal was checked against the trust
	// config.
	Verified bool `json:"verified" doc:"A signed record of the refusal was verified"`
}

// PublishRefusals is what a host knows of refused publishes.
type PublishRefusals struct {
	Completeness string           `json:"completeness" enum:"complete,partial"`
	Note         string           `json:"note,omitempty" doc:"Where the refusals were read and what may be missing"`
	Unreadable   []string         `json:"unreadable,omitempty" doc:"What could not be read, and why"`
	Refusals     []PublishRefusal `json:"refusals"`
}

type refusalsOutput struct {
	Body struct {
		AsOf string `json:"asOf"`
		// Recorded: this host keeps a record of refused publishes.
		Recorded bool `json:"recorded" doc:"This cluster keeps a record of refused publishes; false means an empty list proves nothing"`
		PublishRefusals
	}
}

// recentRefusals is how many refusals the dashboard shows.
const recentRefusals = 20

func (s *Server) deliveryRefusals(ctx context.Context, _ *struct{}) (*refusalsOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := api.PublishRefusals(ctx, s.opts.DeliveryNamespace)
	if err != nil {
		return nil, err
	}
	out := &refusalsOutput{}
	out.Body.AsOf = time.Now().UTC().Format(time.RFC3339)
	if r == nil {
		out.Body.Completeness, out.Body.Refusals = "partial", []PublishRefusal{}
		out.Body.Note = "this cluster keeps no record of refused publishes: a refusal is shown only to whoever ran pacto publish"
		return out, nil
	}
	out.Body.Recorded, out.Body.PublishRefusals = true, *r
	slices.SortFunc(out.Body.Refusals, func(a, b PublishRefusal) int { return refusedAt(b.At).Compare(refusedAt(a.At)) })
	out.Body.Refusals = out.Body.Refusals[:min(len(out.Body.Refusals), recentRefusals)]
	if out.Body.Refusals == nil {
		out.Body.Refusals = []PublishRefusal{}
	}
	return out, nil
}

// refusedAt reads a refusal time. Strings do not sort as times once offsets
// or fractional seconds differ, and an unreadable time sorts as the oldest.
func refusedAt(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// actions are what the UI offers on an operation. They only hide what the
// caller cannot do; the apiserver and the promoter still decide.
func (s *Server) actions(ctx context.Context, op *delivery.OperationObject, stage delivery.StageNow, caller delivery.Caller) []string {
	acts := []string{}
	st := op.Status
	if st.Phase == delivery.PhaseHeld && s.decisionsServed(ctx) && caller.Policy.IsHuman(caller.Identity) &&
		!ownChange(op, caller.User) && caller.Policy.InTeams(caller.Groups, inboxItem(*op).Owners) {
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

// ownChange: user asked for op, directly or as the one it acts for. The
// promoter ignores their decision on it, so the dashboard does not take it.
func ownChange(op *delivery.OperationObject, user string) bool {
	return user == op.Spec.Actor.User || op.Spec.OnBehalfOf != nil && user == op.Spec.OnBehalfOf.User
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
	if err := s.inScope(in.Body.Namespace); err != nil {
		return nil, err
	}
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
	if err := s.inScope(in.Body.Namespace); err != nil {
		return nil, err
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
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.mayDecide(caller); err != nil {
		return nil, err
	}
	op, err := api.Get(ctx, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	if ownChange(op, caller.User) {
		return nil, Refuse(http.StatusForbidden, "OwnChange", "you asked for this change, so someone else must decide it")
	}
	if owners := inboxItem(*op).Owners; !caller.Policy.InTeams(caller.Groups, owners) {
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

// mayDecide refuses a caller the apiserver names an agent, or whose login is
// unknown or older than DecisionFreshness.
func (s *Server) mayDecide(caller delivery.Caller) error {
	at, ok := delivery.ParseAuthTime(caller.AuthTime)
	switch {
	case !caller.Policy.IsHuman(caller.Identity):
		return Refuse(http.StatusForbidden, "AgentsMayNotApprove", "the apiserver says "+caller.User+" is an agent; agents may not decide")
	case caller.AuthTime == "":
		return Refuse(http.StatusForbidden, "AuthTimeUnavailable",
			"this cluster does not map auth_time to userInfo.extra[pacto.dev/auth-time]; nobody can approve here")
	case !ok || time.Since(at) > delivery.DecisionFreshness:
		return Refuse(http.StatusUnauthorized, "FreshLoginRequired", "your login is older than ten minutes: sign in again")
	}
	return nil
}

type cancelOutput struct {
	Body struct {
		Cancelled string `json:"cancelled"`
	}
}

func (s *Server) deliveryCancel(ctx context.Context, in *operationInput) (*cancelOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
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

// compareVersions orders semantic versions as versions (1.10.0 after 1.9.0).
// A version that does not parse sorts before every one that does, then by
// text, so in a newest-first list it comes last.
func compareVersions(a, b string) int {
	va, ea := semver.NewVersion(a)
	vb, eb := semver.NewVersion(b)
	switch {
	case ea == nil && eb == nil:
		return va.Compare(vb)
	case ea == nil:
		return 1
	case eb == nil:
		return -1
	}
	return cmp.Compare(a, b)
}
