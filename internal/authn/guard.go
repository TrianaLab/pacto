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
// Shared: delivery needs an OIDC session, and every mutating request that
// carries the session cookie must come with the session's CSRF header and a
// same-origin Origin.
//
// Open: shared with no sign-in configured. Nobody is identified, so it serves
// no delivery data and no actions.
type Dashboard struct {
	oidc       *OIDC
	open       bool
	launch     string
	launchCSRF string
}

// NewOpen is a shared dashboard without sign-in.
func NewOpen() *Dashboard { return &Dashboard{open: true} }

// Login reports whether people sign in.
func (d *Dashboard) Login() bool { return d.oidc != nil }

// NewLocal is local mode with a fresh per-launch token.
func NewLocal() *Dashboard { return &Dashboard{launch: random(32), launchCSRF: random(32)} }

// NewShared is shared mode, signing people in through o.
func NewShared(o *OIDC) *Dashboard { return &Dashboard{oidc: o} }

// Local reports whether this is local mode.
func (d *Dashboard) Local() bool { return d.oidc == nil && !d.open }

// LaunchURL is the address to open in local mode: it hands the browser the
// per-launch token once, and the dashboard moves it into a cookie.
func (d *Dashboard) LaunchURL(base string) string { return base + "/?token=" + d.launch }

// Routes serves /auth/*: the OIDC endpoints in shared mode, nothing in local
// mode.
func (d *Dashboard) Routes() http.Handler {
	if !d.Login() {
		return http.NotFoundHandler()
	}
	return d.oidc.Routes()
}

// State is what a request's caller looks like to the dashboard.
type State struct {
	// SignedIn is a live OIDC session (shared) or the launch cookie (local).
	SignedIn bool
	// Fresh: the session came from a login the person repeated.
	Fresh bool
	// CSRF is the token to send in CSRFHeader; empty when not signed in.
	CSRF string
	// Token is the bearer for the caller's own cluster requests (shared only).
	Token string
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

// Middleware frames nothing, resolves the caller and refuses what the mode
// does not allow.
func (d *Dashboard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Approve button must not be clickjacked.
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		st, ok := State{}, true
		switch {
		case d.Local():
			st, ok = d.local(w, r)
		case d.Login():
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
			http.Error(w, "this is not the token this dashboard printed when it started", http.StatusForbidden)
			return State{}, false
		}
		http.SetCookie(w, &http.Cookie{Name: launchCookie, Value: d.launch, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
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
	switch {
	case !loopback(r.Host):
		http.Error(w, "a local dashboard answers delivery requests only on a loopback address", http.StatusForbidden)
	case isMutating(r.Method) && (!st.SignedIn || !equal(r.Header.Get(CSRFHeader), d.launchCSRF)):
		http.Error(w, "this action needs the dashboard's launch token and the "+CSRFHeader+" header; open the URL pacto dashboard printed", http.StatusForbidden)
	case isMutating(r.Method) && r.Header.Get("Origin") != "" && !sameOrigin(r):
		http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
	default:
		return st, true
	}
	return State{}, false
}

func (d *Dashboard) shared(w http.ResponseWriter, r *http.Request) (State, bool) {
	_, hasCookie := r.Cookie(SessionCookie)
	var st State
	if s := d.oidc.Session(r); s != nil {
		st = State{SignedIn: true, Fresh: s.Fresh, CSRF: s.CSRF, Token: s.IDToken()}
	}
	// With a session cookie the browser holds rights: a mutating request must
	// prove it comes from this dashboard's own page.
	if hasCookie == nil && isMutating(r.Method) && (strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/auth/logout") {
		switch {
		case r.Header.Get("Origin") == "" || !sameOrigin(r):
			http.Error(w, "a signed-in request must come from the dashboard's own origin", http.StatusForbidden)
			return State{}, false
		case st.SignedIn && !equal(r.Header.Get(CSRFHeader), st.CSRF):
			http.Error(w, "missing or wrong "+CSRFHeader+" header", http.StatusForbidden)
			return State{}, false
		}
	}
	return st, true
}
