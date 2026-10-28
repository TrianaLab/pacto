package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
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
	// AuthTime is when the caller last logged in at the identity provider,
	// as Unix seconds or RFC 3339, when known.
	AuthTime string
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

// authSummary is how this dashboard identifies callers and whether this one
// is signed in. Mode is local (the kubeconfig user), shared (people sign in),
// open (shared without sign-in: nobody is identified) or none (no identity
// layer).
type authSummary struct {
	Mode       string
	Login      bool
	LoginName  string
	TokenLogin bool
	State      AuthState
}

func (s *Server) authSummary(ctx context.Context) authSummary {
	a := s.opts.Auth
	switch {
	case a == nil:
		return authSummary{Mode: "none"}
	case a.Local:
		return authSummary{Mode: "local", State: s.authState(ctx)}
	case !a.Login && !a.TokenLogin:
		return authSummary{Mode: "open", State: s.authState(ctx)}
	}
	return authSummary{Mode: "shared", Login: a.Login, LoginName: a.LoginName, TokenLogin: a.TokenLogin, State: s.authState(ctx)}
}

type sessionOutput struct {
	Body struct {
		Mode       string   `json:"mode" enum:"local,shared,open,none" doc:"local: the kubeconfig user, no decisions; shared: people sign in before the dashboard answers; open: shared without sign-in, so nobody is identified; none: no identity layer"`
		Login      bool     `json:"login" doc:"People sign in with the identity provider"`
		LoginName  string   `json:"loginName,omitempty" doc:"The identity provider's name on the sign-in button; empty says OIDC"`
		TokenLogin bool     `json:"tokenLogin,omitempty" doc:"People may sign in by pasting a Kubernetes bearer token"`
		SignedIn   bool     `json:"signedIn" doc:"The caller has a live session (shared) or the launch cookie (local)"`
		Method     string   `json:"method,omitempty" enum:"oidc,token" doc:"How the caller signed in: with the identity provider or with a pasted token, which never decides"`
		Name       string   `json:"name,omitempty" doc:"The signed-in person's name from their ID token (name, else email)"`
		User       string   `json:"user,omitempty" doc:"The signed-in person's username as the API server names them; every view shows what its Kubernetes RBAC lets this user read"`
		Groups     []string `json:"groups,omitempty" doc:"The signed-in person's groups as the API server names them"`
		AuthTime   string   `json:"authTime,omitempty" doc:"When the signed-in person last logged in at the identity provider, as Unix seconds or RFC 3339, when known"`
		CSRF       string   `json:"csrf,omitempty" doc:"Send it in the X-Pacto-CSRF header of every mutating request"`
	}
}

func (s *Server) session(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
	out := &sessionOutput{}
	a := s.authSummary(ctx)
	st := a.State
	out.Body.Mode, out.Body.Login, out.Body.LoginName, out.Body.TokenLogin = a.Mode, a.Login, a.LoginName, a.TokenLogin
	out.Body.SignedIn, out.Body.CSRF, out.Body.Name, out.Body.Method = st.SignedIn, st.CSRF, st.Name, st.Method
	out.Body.User, out.Body.Groups, out.Body.AuthTime = st.User, st.Groups, st.AuthTime
	if st.SignedIn {
		s.remember(st.User, st.Name)
	}
	return out, nil
}

// registerAuthOperations registers the session endpoint, served on every host.
func (s *Server) registerAuthOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "session", Method: http.MethodGet, Path: "/api/session", Tags: []string{"Auth"},
		Summary:     "Who is signed in",
		Description: "The identity mode, whether the caller is signed in, who the API server says they are, when they last logged in and the CSRF token for mutating requests.",
	}, s.session)
}
