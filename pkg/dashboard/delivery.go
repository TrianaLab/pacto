package dashboard

import (
	"context"
	"errors"
	"net/http"

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
	// Operations may leave out those that concluded Succeeded: no view reads
	// them, and on a cluster that has run delivery a while they are nearly
	// all of them.
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
	// Login: people sign in with OpenID Connect.
	Login bool
	// TokenLogin: people may sign in by pasting a Kubernetes bearer token.
	TokenLogin bool
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
	// User and Groups are the person as the cluster names them: the identity
	// the fleet views ask Kubernetes RBAC about.
	User   string
	Groups []string
	// Method is how the caller signed in: "oidc" or "token".
	Method string
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
			"this cluster does not serve delivery.pacto.trianalab.io/v1alpha1. Install pacto-operator with delivery.enabled=true")
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
		Mode       string   `json:"mode" enum:"local,shared,none" doc:"local: the kubeconfig user, no decisions; shared: people sign in before the dashboard answers; none: no identity layer"`
		Login      bool     `json:"login" doc:"People sign in with the identity provider"`
		TokenLogin bool     `json:"tokenLogin,omitempty" doc:"People may sign in by pasting a Kubernetes bearer token"`
		SignedIn   bool     `json:"signedIn" doc:"The caller has a live session (shared) or the launch cookie (local)"`
		Method     string   `json:"method,omitempty" enum:"oidc,token" doc:"How the caller signed in: with the identity provider or with a pasted token, which never decides"`
		Fresh      bool     `json:"fresh" doc:"The session came from a login the person had to repeat; decisions need one"`
		Name       string   `json:"name,omitempty" doc:"The signed-in person's name from their ID token (name, else email)"`
		User       string   `json:"user,omitempty" doc:"The signed-in person's username as the cluster names them; the fleet views show what its Kubernetes RBAC lets this user read"`
		Groups     []string `json:"groups,omitempty" doc:"The signed-in person's groups as the cluster names them"`
		CSRF       string   `json:"csrf,omitempty" doc:"Send it in the X-Pacto-CSRF header of every mutating request"`
		// Caller is who the apiserver says the caller is, when delivery is
		// installed and the caller is signed in.
		Caller      *delivery.Caller `json:"caller,omitempty"`
		CallerError string           `json:"callerError,omitempty"`
		// RequireOwnerTeam is the published policy's rule for a question
		// that names no team: nobody may answer it, only break-glass
		// overrides. Known only with the caller.
		RequireOwnerTeam bool `json:"requireOwnerTeam,omitempty" doc:"A question that names no team has nobody who may answer it; only break-glass can override"`
	}
}

func (s *Server) session(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
	out := &sessionOutput{}
	out.Body.Mode = "none"
	if a := s.opts.Auth; a != nil {
		out.Body.Mode, out.Body.Login, out.Body.TokenLogin = "shared", a.Login, a.TokenLogin
		if a.Local {
			out.Body.Mode = "local"
		}
	}
	st := s.authState(ctx)
	out.Body.SignedIn, out.Body.Fresh, out.Body.CSRF, out.Body.Name = st.SignedIn, st.Fresh, st.CSRF, st.Name
	out.Body.User, out.Body.Groups, out.Body.Method = st.User, st.Groups, st.Method
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
			out.Body.Caller, out.Body.RequireOwnerTeam = &c, c.Policy.RequireOwnerTeam
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
	huma.Register(api, reg(huma.Operation{OperationID: "delivery-autonomy", Method: http.MethodGet, Path: "/api/delivery/autonomy",
		Summary: "The path to autonomy",
		Description: "How many of the services Pacto has seen have a contract, how many promotions in each Stage's window no person asked for " +
			"or approved, each Stage's level with the next level Pacto proposes and its newest would-have decision, and the blockers with a fix " +
			"to copy. It only reads: a level moves through a proposed and approved operation."}), s.deliveryAutonomy)
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
