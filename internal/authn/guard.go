package authn

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// launchCookie holds the local mode's per-launch token.
const launchCookie = "pacto-launch"

// Dashboard is the dashboard's request policy in one of two modes.
//
// Local: bound to loopback with no OIDC, the process acts as the kubeconfig
// user, like `pacto delivery`. Decisions are never served (any local process
// could otherwise approve as that user). Other delivery actions need the
// per-launch token printed in the URL `pacto dashboard` opens, kept in an
// HttpOnly cookie, plus the CSRF header, from a loopback Host.
//
// Shared: people sign in, with OIDC or by pasting a Kubernetes bearer token
// (TokenLogin), before the dashboard answers anything about the fleet. Every API endpoint and /metrics need a session; the static UI,
// /health, /api/session and /auth/* stay open, so the page can load, show its
// sign-in screen and sign the person in. Every mutating request that carries
// the session cookie must come with the session's CSRF header and a
// same-origin Origin.
//
// Open: shared with no sign-in configured. Nobody is identified, so it serves
// no delivery data and no actions.
type Dashboard struct {
	oidc       *OIDC
	tokens     *TokenLogin
	open       bool
	launch     string
	launchCSRF string
}

// NewOpen is a shared dashboard without sign-in.
func NewOpen() *Dashboard { return &Dashboard{open: true} }

// Login reports whether people sign in with OIDC.
func (d *Dashboard) Login() bool { return d.oidc != nil }

// TokenLogin reports whether people may sign in with a pasted token.
func (d *Dashboard) TokenLogin() bool { return d.tokens != nil }

// SignsIn reports whether people sign in at all: shared mode.
func (d *Dashboard) SignsIn() bool { return d.oidc != nil || d.tokens != nil }

// NewLocal is local mode with a fresh per-launch token.
func NewLocal() *Dashboard { return &Dashboard{launch: random(32), launchCSRF: random(32)} }

// NewShared is shared mode, signing people in through o, t or both (either
// may be nil, not both).
func NewShared(o *OIDC, t *TokenLogin) *Dashboard { return &Dashboard{oidc: o, tokens: t} }

// Local reports whether this is local mode.
func (d *Dashboard) Local() bool { return !d.SignsIn() && !d.open }

// LaunchURL is the address to open in local mode: it hands the browser the
// per-launch token once, and the dashboard moves it into a cookie.
func (d *Dashboard) LaunchURL(base string) string { return base + "/?token=" + d.launch }

// Routes serves /auth/*: in shared mode the OIDC sign-in, the token sign-in
// and logout, which ends either kind of session; nothing in local mode.
func (d *Dashboard) Routes() http.Handler {
	mux := http.NewServeMux()
	if d.oidc != nil {
		mux.HandleFunc("GET /auth/login", d.oidc.Login)
		mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
			d.endToken(r) // an identity provider sign-in replaces a token session
			d.oidc.Callback(w, r)
		})
	}
	if d.tokens != nil {
		mux.HandleFunc("POST /auth/token", d.tokens.Login)
	}
	if d.SignsIn() {
		mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
			d.endToken(r)
			if d.oidc != nil {
				d.oidc.Logout(w, r)
				return
			}
			http.SetCookie(w, sessionCookie("", -1))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}\n"))
		})
	}
	return mux
}

func (d *Dashboard) endToken(r *http.Request) {
	if d.tokens != nil {
		d.tokens.end(r)
	}
}

// State is what a request's caller looks like to the dashboard.
type State struct {
	// SignedIn is a live OIDC session (shared) or the launch cookie (local).
	SignedIn bool
	// CSRF is the token to send in CSRFHeader; empty when not signed in.
	CSRF string
	// Token is the bearer for the caller's own cluster requests (shared only).
	Token string
	// Person is who the cluster says the caller is: what the API server
	// answered for the session's token, or for the kubeconfig in local mode.
	// Only an OIDC session the API server cannot be asked about is named
	// with the claim mapping.
	Person Person
	// Method is how the caller signed in: "oidc" or "token" (shared only).
	Method string
	// AuthTime is when the caller last logged in at the identity provider,
	// as Unix seconds or RFC 3339, when known.
	AuthTime string
}

type stateKey struct{}

// StateFrom returns the State the middleware put on the request context.
func StateFrom(ctx context.Context) State {
	s, _ := ctx.Value(stateKey{}).(State)
	return s
}

func isMutating(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

// loopback reports whether a Host header names this machine, which a DNS
// rebinding page cannot fake.
func loopback(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && u.Host != "" && u.Host == r.Host
}

func equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// Middleware resolves the caller and refuses what the mode does not allow.
// The dashboard server sets the framing and content policy headers.
func (d *Dashboard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, ok := State{}, true
		switch {
		case d.Local():
			st, ok = d.local(w, r)
		case d.SignsIn():
			st, ok = d.shared(w, r)
		}
		if ok {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, st)))
		}
	})
}

// delivery reports whether a path is a delivery or session endpoint, the ones
// that act or read as the caller.
func delivery(path string) bool {
	return strings.HasPrefix(path, "/api/delivery/") || path == "/api/session"
}

func (d *Dashboard) local(w http.ResponseWriter, r *http.Request) (State, bool) {
	if r.Method == http.MethodGet && r.URL.Query().Get("token") != "" {
		if !equal(r.URL.Query().Get("token"), d.launch) {
			Refused(r.Context(), r, "local dashboard refused a launch token", "reason", "not the token printed at start")
			http.Error(w, "this is not the token this dashboard printed when it started", http.StatusForbidden)
			return State{}, false
		}
		// Secure everywhere but plain HTTP on loopback, the one place a browser
		// would otherwise drop the cookie and the token never leaves the machine.
		http.SetCookie(w, &http.Cookie{Name: launchCookie, Value: d.launch, Path: "/", HttpOnly: true,
			Secure: r.TLS != nil || !loopback(r.Host), SameSite: http.SameSiteStrictMode})
		q := r.URL.Query()
		q.Del("token")
		loc := r.URL.Path
		if len(q) > 0 {
			loc += "?" + q.Encode()
		}
		http.Redirect(w, r, loc, http.StatusSeeOther)
		return State{}, false
	}
	var st State
	if c, err := r.Cookie(launchCookie); err == nil && equal(c.Value, d.launch) {
		st = State{SignedIn: true, CSRF: d.launchCSRF}
	}
	if !delivery(r.URL.Path) {
		return st, true
	}
	if why := d.refuseLocal(r, st); why != "" {
		Refused(r.Context(), r, "local dashboard refused a delivery request", "reason", why)
		http.Error(w, why, http.StatusForbidden)
		return State{}, false
	}
	return st, true
}

// refuseLocal says why a local dashboard refuses a delivery request, or "".
func (d *Dashboard) refuseLocal(r *http.Request, st State) string {
	switch {
	case !loopback(r.Host):
		return "a local dashboard answers delivery requests only on a loopback address"
	case !st.SignedIn && r.URL.Path != "/api/session":
		// Reads too: they go to the cluster as the kubeconfig user, which only
		// whoever saw the launch token may borrow. /api/session stays open so
		// the UI can tell it is signed out.
		return "delivery needs the dashboard's launch token; open the URL pacto dashboard printed"
	case isMutating(r.Method) && (!st.SignedIn || !equal(r.Header.Get(CSRFHeader), d.launchCSRF)):
		return "this action needs the dashboard's launch token and the " + CSRFHeader + " header; open the URL pacto dashboard printed"
	case isMutating(r.Method) && r.Header.Get("Origin") != "" && !sameOrigin(r):
		return "cross-origin request forbidden"
	}
	return ""
}

// needsSession: the paths shared mode answers only to a signed-in caller, every
// API endpoint and /metrics. /api/session stays open so the page can tell it
// is signed out; the static UI, /health and /auth/* hold no fleet data.
func needsSession(path string) bool {
	return path == "/metrics" || (strings.HasPrefix(path, "/api/") && path != "/api/session")
}

// refuseShared says why a shared dashboard refuses a request, or "". With a
// session cookie the browser holds rights: a mutating request must prove it
// comes from this dashboard's own page.
func refuseShared(r *http.Request, st State, hasCookie bool) string {
	if !hasCookie || !isMutating(r.Method) || (!strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/auth/logout") {
		return ""
	}
	switch {
	case r.Header.Get("Origin") == "" || !sameOrigin(r):
		return "a signed-in request must come from the dashboard's own origin"
	case st.SignedIn && !equal(r.Header.Get(CSRFHeader), st.CSRF):
		return "missing or wrong " + CSRFHeader + " header"
	}
	return ""
}

// sessionState is the request's live session: an OIDC one, else a token one.
func (d *Dashboard) sessionState(r *http.Request) State {
	if d.oidc != nil {
		if s := d.oidc.Session(r); s != nil {
			return State{SignedIn: true, CSRF: s.CSRF, Token: s.IDToken(), Person: s.Person(), Method: "oidc", AuthTime: s.AuthTime()}
		}
	}
	if d.tokens != nil {
		if s, ok := d.tokens.session(r); ok {
			return State{SignedIn: true, CSRF: s.csrf, Token: s.token, Person: s.person, Method: "token"}
		}
	}
	return State{}
}

// loginRequired answers 401 in the dashboard's refusal shape (status, code,
// detail), so every view reads the same LoginRequired.
func loginRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"status":401,"code":"LoginRequired","detail":"sign in to use this dashboard"}` + "\n"))
}

func (d *Dashboard) shared(w http.ResponseWriter, r *http.Request) (State, bool) {
	_, hasCookie := r.Cookie(SessionCookie)
	st := d.sessionState(r)
	if !st.SignedIn && needsSession(r.URL.Path) {
		loginRequired(w)
		return State{}, false
	}
	if why := refuseShared(r, st, hasCookie == nil); why != "" {
		Refused(r.Context(), r, "dashboard refused a signed-in request", "reason", why, "origin", r.Header.Get("Origin"))
		http.Error(w, why, http.StatusForbidden)
		return State{}, false
	}
	return st, true
}
