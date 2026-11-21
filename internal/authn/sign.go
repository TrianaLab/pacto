package authn

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// signCookie carries a signing login's state between /auth/sign and
// /auth/sign/callback, apart from the session login's cookie.
const signCookie = "__Host-pacto-sign"

// Bounds of the signing login: a ticket and a signing token each live
// signTTL, and at most signCap of each are kept.
const (
	signTTL = 5 * time.Minute
	signCap = 1000
)

// SignConfig is the dashboard's second OpenID Connect client, the one whose
// ID tokens Fulcio accepts. Its client id differs from the session client's,
// so a token the API server accepts is never sent to Fulcio.
type SignConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	// RedirectURL is the dashboard's /auth/sign/callback as the browser
	// reaches it.
	RedirectURL string
	// HTTPClient reaches the identity provider; nil is the session client's.
	HTTPClient *http.Client
}

// signKey names one signing login: the session it belongs to and the digest
// of the intent it signs.
type signKey struct{ session, intent string }

// signTicket lets one session start one signing login for one intent.
type signTicket struct {
	key     signKey
	accept  []string
	expires time.Time
}

// signEntry is a signing login in flight (raw empty) or done: the ID token
// of the sign client, used once.
type signEntry struct {
	raw     string
	accept  []string
	expires time.Time
}

// Signer runs the signing login: a fresh login at the identity provider with
// the sign client, for one person and one intent. The ID token it gets is
// kept in memory for one use and never becomes a session.
// ponytail: one replica's memory, as the sessions; a login started on
// another replica fails and is asked again.
type Signer struct {
	cfg    SignConfig
	o      *OIDC
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	prov    *provider
	tickets map[string]signTicket
	entries map[signKey]signEntry
}

// NewSigner builds the signing login on top of the session login o. The sign
// client must differ from the session client.
func NewSigner(cfg SignConfig, o *OIDC) (*Signer, error) {
	switch {
	case o == nil:
		return nil, errors.New("signing in the dashboard needs OIDC sign-in")
	case cfg.ClientID == "" || cfg.RedirectURL == "":
		return nil, errors.New("the signing login needs a client id and a redirect URL")
	case cfg.ClientID == o.cfg.ClientID:
		return nil, errors.New("the signing client must differ from the sign-in client, so a token the API server accepts never reaches Fulcio")
	}
	if cfg.IssuerURL == "" {
		cfg.IssuerURL = o.cfg.IssuerURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = o.client
	}
	return &Signer{cfg: cfg, o: o, client: client, now: o.now, tickets: map[string]signTicket{}, entries: map[signKey]signEntry{}}, nil
}

func (s *Signer) provider() (*provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prov != nil {
		return s.prov, nil
	}
	p, ends, err := discover(s.client, s.cfg.IssuerURL)
	if err != nil {
		return nil, err
	}
	s.prov = &provider{
		oauth: oauth2.Config{ClientID: s.cfg.ClientID, ClientSecret: s.cfg.ClientSecret, Endpoint: p.Endpoint(),
			RedirectURL: s.cfg.RedirectURL, Scopes: []string{oidc.ScopeOpenID, "email", "profile"}},
		verifier: p.Verifier(&oidc.Config{ClientID: s.cfg.ClientID, Now: s.now}),
		ends:     ends,
	}
	return s.prov, nil
}

// bound drops what expired from m and, when it is still full, the entry
// closest to expiring, so m never holds more than signCap.
// ponytail: one bound for everyone, so a flood from one session ends others'
// logins early; bound per session if that ever matters.
func bound[K comparable, V any](m map[K]V, now time.Time, expires func(V) time.Time) {
	var oldest K
	var first time.Time
	for k, v := range m {
		switch e := expires(v); {
		case !now.Before(e):
			delete(m, k)
		case first.IsZero() || e.Before(first):
			oldest, first = k, e
		}
	}
	if len(m) >= signCap {
		delete(m, oldest)
	}
}

// Start begins a signing login: GET /auth/sign?ticket=&return=. It needs an
// OIDC session and a live ticket that session was given; the ticket is used
// up. The identity provider is asked for a fresh login (prompt=login,
// max_age=0).
func (s *Signer) Start(w http.ResponseWriter, r *http.Request) {
	sess := s.o.Session(r)
	q := r.URL.Query()
	now := s.now()
	s.mu.Lock()
	t, ok := s.tickets[q.Get("ticket")]
	delete(s.tickets, q.Get("ticket"))
	s.mu.Unlock()
	if sess == nil || !ok || t.key.session != sess.id || !now.Before(t.expires) {
		Refused(r.Context(), r, "dashboard refused a signing login", "reason", "no OIDC session or no live ticket of this session")
		http.Error(w, "signing needs a sign-in with the identity provider and a signing request from this page", http.StatusForbidden)
		return
	}
	ret := q.Get("return")
	if !SafeReturn(ret) {
		ret = "/"
	}
	p, err := s.provider()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.mu.Lock()
	bound(s.entries, now, func(e signEntry) time.Time { return e.expires })
	s.entries[t.key] = signEntry{accept: t.accept, expires: now.Add(signTTL)}
	s.mu.Unlock()
	st := loginState{State: random(16), Nonce: random(16), Verifier: oauth2.GenerateVerifier(), Return: ret,
		Fresh: true, Intent: t.key.intent, Started: now.Unix()}
	b, _ := json.Marshal(st)
	http.SetCookie(w, &http.Cookie{Name: signCookie, Value: base64.RawURLEncoding.EncodeToString(b) + "." + s.o.sign(b),
		Path: "/", MaxAge: int(loginTimeout.Seconds()), Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, p.oauth.AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier),
		oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0")), http.StatusFound)
}

// Callback finishes a signing login: GET /auth/sign/callback. It exchanges
// the code with the sign client's secret and the PKCE verifier, verifies the
// ID token with the sign client's audience, requires a login after Start and
// the same person as the session, and keeps the token for one use. It never
// creates, rotates or ends a session.
func (s *Signer) Callback(w http.ResponseWriter, r *http.Request) {
	refuse := func(code int, msg string) {
		Refused(r.Context(), r, "dashboard refused a signing login", "issuer", s.cfg.IssuerURL, "client", s.cfg.ClientID, "reason", msg)
		http.Error(w, msg, code)
	}
	st, ok := s.o.readLogin(r, signCookie)
	http.SetCookie(w, &http.Cookie{Name: signCookie, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	sess := s.o.Session(r)
	switch {
	case !ok || q.Get("state") != st.State || st.Intent == "":
		refuse(http.StatusBadRequest, "this signing login was not started here or took too long; sign again")
		return
	case q.Get("error") != "":
		refuse(http.StatusUnauthorized, "the identity provider refused the login: "+q.Get("error")+" "+q.Get("error_description"))
		return
	case sess == nil:
		refuse(http.StatusForbidden, "your sign-in ended; sign in and sign again")
		return
	}
	raw, msg := s.verify(r, st, q.Get("code"), sess)
	if msg != "" {
		refuse(http.StatusUnauthorized, msg)
		return
	}
	key := signKey{session: sess.id, intent: st.Intent}
	s.mu.Lock()
	e, live := s.entries[key]
	if live = live && s.now().Before(e.expires); live {
		e.raw = raw
		s.entries[key] = e
	}
	s.mu.Unlock()
	if !live {
		refuse(http.StatusBadRequest, "this signing login was not started here or took too long; sign again")
		return
	}
	http.Redirect(w, r, st.Return, http.StatusFound)
}

// verify exchanges code and checks the ID token: the sign client's, this
// login's, fresh, and naming the session's person. It returns the raw token,
// or why it is refused.
func (s *Signer) verify(r *http.Request, st loginState, code string, sess *Session) (string, string) {
	p, err := s.provider()
	if err != nil {
		return "", err.Error()
	}
	tok, err := p.oauth.Exchange(s.o.ctx(r), code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return "", "exchanging the signing code failed: " + err.Error()
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.verifier.Verify(r.Context(), raw)
	if err != nil {
		return "", "the identity provider's ID token does not verify: " + err.Error()
	}
	if err := fresh(id, st.Nonce, time.Unix(st.Started, 0)); err != nil {
		return "", err.Error()
	}
	if !samePerson(s.o.cfg.Claims, raw, sess.idToken) {
		return "", "signed in as a different person; sign in to the identity provider as yourself and sign again"
	}
	return raw, ""
}

// samePerson reports whether two ID tokens name one person: the same user
// under the cluster's claim mapping (sub only when no username claim is set,
// since pairwise subjects differ per client) and the same email when both
// carry one, because the email is what the signing certificate names.
func samePerson(c Claims, signRaw, sessionRaw string) bool {
	a, b := c.Person(signRaw), c.Person(sessionRaw)
	if a.User == Anonymous.User || a.User != b.User {
		return false
	}
	email := func(raw string) string {
		var v string
		_ = json.Unmarshal(jwtClaims(raw)["email"], &v) // a claim that is no string is no email
		return v
	}
	ea, eb := email(signRaw), email(sessionRaw)
	return ea == "" || eb == "" || ea == eb
}

// Ticket lets the OIDC session of r start one signing login for intent with
// the accept set accept: the plan route hands it to the browser after the
// mutating-request guard passed, so no third-party link can start one. It is
// "" without an OIDC session.
func (s *Signer) Ticket(r *http.Request, intent string, accept []string) string {
	sess := s.o.Session(r)
	if sess == nil || intent == "" {
		return ""
	}
	id, now := random(16), s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	bound(s.tickets, now, func(t signTicket) time.Time { return t.expires })
	s.tickets[id] = signTicket{key: signKey{session: sess.id, intent: intent}, accept: slices.Clone(accept), expires: now.Add(signTTL)}
	return id
}

// entry is the live signing login of r's session for intent.
func (s *Signer) entry(r *http.Request, intent string, take bool) (signEntry, bool) {
	sess := s.o.Session(r)
	if sess == nil {
		return signEntry{}, false
	}
	key := signKey{session: sess.id, intent: intent}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	ok = ok && s.now().Before(e.expires)
	if take && ok && e.raw != "" {
		delete(s.entries, key)
	}
	return e, ok
}

// Take hands over, once, the sign client's ID token the session of r got for
// intent; false when there is none (never signed, expired or taken). The
// session's own token is never returned.
func (s *Signer) Take(r *http.Request, intent string) (string, bool) {
	e, ok := s.entry(r, intent, true)
	return e.raw, ok && e.raw != ""
}

// Accepted is the accept set the ticket of the session's signing login for
// intent carried, so the page returning from the login shows the plan that
// was signed for, whatever its URL says; nil without one.
func (s *Signer) Accepted(r *http.Request, intent string) []string {
	e, ok := s.entry(r, intent, false)
	if !ok {
		return nil
	}
	return slices.Clone(e.accept)
}
