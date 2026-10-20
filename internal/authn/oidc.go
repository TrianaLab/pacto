package authn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Cookie and header names. __Host- binds a cookie to this exact origin, over
// HTTPS, for the whole path.
const (
	SessionCookie = "__Host-pacto-session"
	loginCookie   = "__Host-pacto-login"
	// CSRFHeader carries the session's CSRF token on every mutating request.
	// A cross-origin page cannot set it without a preflight the dashboard
	// refuses.
	CSRFHeader = "X-Pacto-CSRF"
)

// Session lifetimes.
const (
	idleTimeout     = time.Hour
	absoluteTimeout = 12 * time.Hour
	loginTimeout    = 10 * time.Minute
	// refreshMargin: an ID token this close to expiry is refreshed first.
	refreshMargin = time.Minute
	// FreshLogin is how old a login may be when its owner decides, leaving a
	// margin under the promoter's 15 minutes.
	FreshLogin = 10 * time.Minute
	// skew is the clock difference tolerated on auth_time.
	skew = time.Minute
)

// Config is the dashboard's OpenID Connect client.
type Config struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	// RedirectURL is the dashboard's /auth/callback as the browser reaches it.
	RedirectURL string
	// ExtraScopes are added to openid, profile and groups (for example a
	// cross-client audience scope, so the ID token names the CLI client too).
	ExtraScopes []string
	// HTTPClient reaches the identity provider; nil is a default client.
	HTTPClient *http.Client
}

// Session is one signed-in browser. Its tokens never reach JavaScript and
// are never logged.
type Session struct {
	id      string
	idToken string
	refresh string
	expiry  time.Time
	created time.Time
	seen    time.Time
	// CSRF is the token mutating requests must carry in CSRFHeader.
	CSRF string
	// Fresh is true when the session was created by a login the identity
	// provider made the person repeat (prompt=login). Only such a session may
	// decide on an operation.
	Fresh bool
}

// LogValue keeps every token out of logs: only whether the session is fresh.
func (s *Session) LogValue() slog.Value { return slog.GroupValue(slog.Bool("fresh", s.Fresh)) }

// IDToken is the session's current ID token, the bearer of its requests.
func (s *Session) IDToken() string { return s.idToken }

type provider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	ends     endpoints
}

// OIDC signs people in to the dashboard and keeps their sessions in memory.
// ponytail: one replica's memory, so a restart signs everyone out; move to
// encrypted cookies or a shared store when the dashboard runs more than one.
type OIDC struct {
	cfg    Config
	client *http.Client
	now    func() time.Time
	// key signs the short-lived login cookie.
	key []byte

	mu       sync.Mutex
	prov     *provider
	sessions map[string]*Session
}

// NewOIDC builds the client. The identity provider is read on the first
// login, so a dashboard starts (and serves its fleet views) while it is down.
func NewOIDC(cfg Config) (*OIDC, error) {
	if cfg.IssuerURL == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("OIDC login needs an issuer URL, a client id and a redirect URL")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OIDC{cfg: cfg, client: client, now: time.Now, key: []byte(random(32)), sessions: map[string]*Session{}}, nil
}

func (o *OIDC) provider() (*provider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.prov != nil {
		return o.prov, nil
	}
	p, ends, err := discover(o.client, o.cfg.IssuerURL)
	if err != nil {
		return nil, err
	}
	o.prov = &provider{
		oauth: oauth2.Config{ClientID: o.cfg.ClientID, ClientSecret: o.cfg.ClientSecret, Endpoint: p.Endpoint(),
			RedirectURL: o.cfg.RedirectURL, Scopes: append([]string{oidc.ScopeOpenID, "profile", "groups"}, o.cfg.ExtraScopes...)},
		verifier: p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID, Now: o.now}),
		ends:     ends,
	}
	return o.prov, nil
}

func (o *OIDC) ctx(r *http.Request) context.Context {
	return context.WithValue(r.Context(), oauth2.HTTPClient, o.client)
}

// loginState travels in the signed login cookie between /auth/login and
// /auth/callback.
type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Return   string `json:"r"`
	Fresh    bool   `json:"f,omitempty"`
	Started  int64  `json:"t"`
}

func (o *OIDC) sign(b []byte) string {
	m := hmac.New(sha256.New, o.key)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// SafeReturn reports whether ret is a path inside this app, never a URL that
// could send the browser elsewhere.
func SafeReturn(ret string) bool {
	if !strings.HasPrefix(ret, "/") || strings.HasPrefix(ret, "//") || strings.ContainsAny(ret, "\\\r\n") {
		return false
	}
	u, err := url.Parse(ret)
	return err == nil && u.Scheme == "" && u.Host == ""
}

// Login starts a sign-in: GET /auth/login?return=<path>[&fresh=1]. fresh asks
// the identity provider to make the person log in again (max_age=0,
// prompt=login), which a decision on an operation needs.
func (o *OIDC) Login(w http.ResponseWriter, r *http.Request) {
	ret := r.URL.Query().Get("return")
	if ret == "" {
		ret = "/"
	}
	if !SafeReturn(ret) {
		http.Error(w, "return must be a path inside the dashboard", http.StatusBadRequest)
		return
	}
	p, err := o.provider()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	st := loginState{State: random(16), Nonce: random(16), Verifier: oauth2.GenerateVerifier(), Return: ret,
		Fresh: r.URL.Query().Get("fresh") == "1", Started: o.now().Unix()}
	b, _ := json.Marshal(st)
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: base64.RawURLEncoding.EncodeToString(b) + "." + o.sign(b),
		Path: "/", MaxAge: int(loginTimeout.Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	// max_age makes auth_time mandatory in the ID token; no offline_access, so
	// a refresh token lives no longer than the identity provider's session.
	maxAge := strconv.Itoa(int(absoluteTimeout.Seconds()))
	opts := []oauth2.AuthCodeOption{oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)}
	if st.Fresh {
		maxAge = "0"
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"))
	}
	opts = append(opts, oauth2.SetAuthURLParam("max_age", maxAge))
	http.Redirect(w, r, p.oauth.AuthCodeURL(st.State, opts...), http.StatusFound)
}

// readLogin returns the login cookie's state if its signature holds and it is
// not older than loginTimeout.
func (o *OIDC) readLogin(r *http.Request) (loginState, bool) {
	var st loginState
	c, err := r.Cookie(loginCookie)
	if err != nil {
		return st, false
	}
	payload, sig, _ := strings.Cut(c.Value, ".")
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || !hmac.Equal([]byte(sig), []byte(o.sign(b))) || json.Unmarshal(b, &st) != nil {
		return st, false
	}
	return st, o.now().Sub(time.Unix(st.Started, 0)) <= loginTimeout
}

// claims are the ID token claims the dashboard needs beyond go-oidc's.
type claims struct {
	AuthTime json.Number `json:"auth_time"`
}

func authTime(id *oidc.IDToken) (time.Time, bool) {
	var c claims
	_ = id.Claims(&c) // Verify already decoded these claims
	secs, err := c.AuthTime.Int64()
	return time.Unix(secs, 0), err == nil
}

// Callback finishes a sign-in: GET /auth/callback. It checks the state
// against the login cookie, exchanges the code with the PKCE verifier,
// verifies the ID token (signature, issuer, audience, expiry, nonce and
// auth_time) and creates the session. A fresh login also rotates the session
// id: the old session is gone and a new cookie is set.
func (o *OIDC) Callback(w http.ResponseWriter, r *http.Request) {
	st, ok := o.readLogin(r)
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	switch {
	case !ok || q.Get("state") != st.State:
		http.Error(w, "this sign-in was not started here or took too long; sign in again", http.StatusBadRequest)
		return
	case q.Get("error") != "":
		http.Error(w, "the identity provider refused the sign-in: "+q.Get("error")+" "+q.Get("error_description"), http.StatusUnauthorized)
		return
	}
	p, err := o.provider()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	tok, err := p.oauth.Exchange(o.ctx(r), q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		http.Error(w, "exchanging the sign-in code failed: "+err.Error(), http.StatusUnauthorized)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.verifier.Verify(r.Context(), raw)
	if err != nil {
		http.Error(w, "the identity provider's ID token does not verify: "+err.Error(), http.StatusUnauthorized)
		return
	}
	at, hasAuthTime := authTime(id)
	switch {
	case id.Nonce != st.Nonce:
		http.Error(w, "the ID token is not for this sign-in", http.StatusUnauthorized)
		return
	case !hasAuthTime:
		http.Error(w, "the ID token carries no auth_time, which the dashboard needs to tell a fresh login", http.StatusUnauthorized)
		return
	case st.Fresh && at.Before(time.Unix(st.Started, 0).Add(-skew)):
		http.Error(w, "the identity provider did not ask you to log in again; a decision needs a fresh login", http.StatusUnauthorized)
		return
	}
	if old, err := r.Cookie(SessionCookie); err == nil {
		o.end(r.Context(), old.Value)
	}
	now := o.now()
	s := &Session{id: random(32), idToken: raw, refresh: tok.RefreshToken, expiry: id.Expiry, created: now, seen: now,
		CSRF: random(32), Fresh: st.Fresh}
	o.mu.Lock()
	o.sessions[s.id] = s
	o.mu.Unlock()
	o.sweep(r.Context())
	http.SetCookie(w, sessionCookie(s.id, int(absoluteTimeout.Seconds())))
	http.Redirect(w, r, st.Return, http.StatusFound)
}

func sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: SessionCookie, Value: value, Path: "/", MaxAge: maxAge, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
}

// Logout ends the session: POST /auth/logout. The refresh token is revoked
// and, when the identity provider supports it, the answer names its logout
// page for the browser to visit.
func (o *OIDC) Logout(w http.ResponseWriter, r *http.Request) {
	out := struct {
		LogoutURL string `json:"logoutURL,omitempty"`
	}{}
	if c, err := r.Cookie(SessionCookie); err == nil {
		if s, p := o.end(r.Context(), c.Value); s != nil && p.ends.EndSession != "" {
			out.LogoutURL = p.ends.EndSession + "?" + url.Values{"id_token_hint": {s.idToken}, "client_id": {o.cfg.ClientID}}.Encode()
		}
	}
	http.SetCookie(w, sessionCookie("", -1))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// end removes a session and revokes its refresh token (RFC 7009). A session
// only exists after a login read the provider, so p is set whenever s is.
func (o *OIDC) end(ctx context.Context, id string) (*Session, *provider) {
	o.mu.Lock()
	s := o.sessions[id]
	delete(o.sessions, id)
	p := o.prov
	o.mu.Unlock()
	if s != nil && s.refresh != "" && p.ends.Revocation != "" {
		err := postForm(ctx, o.client, p.ends.Revocation, o.cfg.ClientID, o.cfg.ClientSecret,
			url.Values{"token": {s.refresh}, "token_type_hint": {"refresh_token"}}, nil)
		if err != nil {
			slog.WarnContext(ctx, "revoking a refresh token failed", "error", err)
		}
	}
	return s, p
}

// expired reports whether a session passed its idle or absolute timeout.
func (o *OIDC) expired(s *Session, now time.Time) bool {
	return now.Sub(s.created) > absoluteTimeout || now.Sub(s.seen) > idleTimeout
}

// sweep ends every expired session, so their refresh tokens are revoked even
// when the browser never comes back.
func (o *OIDC) sweep(ctx context.Context) {
	now := o.now()
	o.mu.Lock()
	var gone []string
	for id, s := range o.sessions {
		if o.expired(s, now) {
			gone = append(gone, id)
		}
	}
	o.mu.Unlock()
	for _, id := range gone {
		o.end(ctx, id)
	}
}

// Session returns a copy of the request's live session, refreshing its ID
// token when it is about to expire. A refresh never changes auth_time, so it
// never makes a login fresh. nil means not signed in.
func (o *OIDC) Session(r *http.Request) *Session {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return nil
	}
	now := o.now()
	o.mu.Lock()
	s, ok := o.sessions[c.Value]
	var cp Session
	if ok {
		cp = *s
	}
	o.mu.Unlock()
	switch {
	case !ok:
		return nil
	case o.expired(&cp, now):
		o.end(r.Context(), cp.id)
		return nil
	case cp.expiry.Sub(now) < refreshMargin:
		if err := o.refreshSession(r, &cp); err != nil {
			if now.After(cp.expiry) {
				o.end(r.Context(), cp.id)
				return nil
			}
			slog.WarnContext(r.Context(), "refreshing a session failed", "error", err)
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	cp.seen = now
	s.seen, s.idToken, s.refresh, s.expiry = now, cp.idToken, cp.refresh, cp.expiry
	return &cp
}

// refreshSession renews s's ID token with its refresh token.
func (o *OIDC) refreshSession(r *http.Request, s *Session) error {
	if s.refresh == "" {
		return errors.New("the session has no refresh token")
	}
	p, err := o.provider()
	if err != nil {
		return err
	}
	tok, err := p.oauth.TokenSource(o.ctx(r), &oauth2.Token{RefreshToken: s.refresh, Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		return err
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.verifier.Verify(r.Context(), raw)
	if err != nil {
		return fmt.Errorf("the refreshed ID token does not verify: %w", err)
	}
	s.idToken, s.expiry = raw, id.Expiry
	if tok.RefreshToken != "" {
		s.refresh = tok.RefreshToken
	}
	return nil
}

// Routes serves /auth/login, /auth/callback and /auth/logout.
func (o *OIDC) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", o.Login)
	mux.HandleFunc("GET /auth/callback", o.Callback)
	mux.HandleFunc("POST /auth/logout", o.Logout)
	return mux
}
