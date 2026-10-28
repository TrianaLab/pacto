package dashboard

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// Auth is a live host's identity layer. Nil means no sign-in and no delivery
// decisions.
type Auth struct {
	// Local: the process acts as the kubeconfig user; decisions are never
	// served there.
	Local bool
	// Login: people sign in with OpenID Connect.
	Login bool
	// LoginName is the identity provider's name on the sign-in button; empty
	// says OIDC.
	LoginName string
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
	CSRF     string
	// Name is the person's name from their ID token (name, else email).
	Name string
	// User and Groups are the person as the cluster names them: the identity
	// the fleet views ask Kubernetes RBAC about.
	User   string
	Groups []string
	// Method is how the caller signed in: "oidc" or "token".
	Method string
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
		LoginName  string   `json:"loginName,omitempty" doc:"The identity provider's name on the sign-in button; empty says OIDC"`
		TokenLogin bool     `json:"tokenLogin,omitempty" doc:"People may sign in by pasting a Kubernetes bearer token"`
		SignedIn   bool     `json:"signedIn" doc:"The caller has a live session (shared) or the launch cookie (local)"`
		Method     string   `json:"method,omitempty" enum:"oidc,token" doc:"How the caller signed in: with the identity provider or with a pasted token, which never decides"`
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
		out.Body.Mode, out.Body.Login, out.Body.LoginName, out.Body.TokenLogin = "shared", a.Login, a.LoginName, a.TokenLogin
		if a.Local {
			out.Body.Mode = "local"
		}
	}
	st := s.authState(ctx)
	out.Body.SignedIn, out.Body.CSRF, out.Body.Name = st.SignedIn, st.CSRF, st.Name
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

// registerAuthOperations registers the session endpoint, served on every host.
func (s *Server) registerAuthOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "session", Method: http.MethodGet, Path: "/api/session", Tags: []string{"Auth"},
		Summary:     "Who is signed in",
		Description: "The identity mode, whether the caller is signed in, the CSRF token for mutating requests and who the apiserver says the caller is.",
	}, s.session)
}
