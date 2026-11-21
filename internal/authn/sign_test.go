package authn

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// signFixture is a signed-in OIDC session and a signer on the same issuer.
type signFixture struct {
	f      *fakeIdP
	o      *OIDC
	s      *Signer
	cookie *http.Cookie // the session cookie
}

func newSignFixture(t *testing.T, sessionSub string, claims Claims) *signFixture {
	t.Helper()
	f := newIdP(t)
	o, err := NewOIDC(Config{IssuerURL: f.srv.URL, ClientID: "pacto-dashboard", ClientSecret: "s",
		RedirectURL: "https://dash.example/auth/callback", Claims: claims})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	code, q := f.authorize(t, rec.Header().Get("Location"), sessionSub)
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code="+code+"&state="+q.Get("state"), nil)
	req.AddCookie(cookieNamed(t, rec, loginCookie))
	cb := httptest.NewRecorder()
	o.Callback(cb, req)
	s, err := NewSigner(SignConfig{ClientID: "pacto-sign", ClientSecret: "ss", RedirectURL: "https://dash.example/auth/sign/callback"}, o)
	if err != nil {
		t.Fatal(err)
	}
	return &signFixture{f: f, o: o, s: s, cookie: cookieNamed(t, cb, SessionCookie)}
}

// session is the fixture's live session.
func (x *signFixture) session(t *testing.T) *Session {
	t.Helper()
	sess := x.o.Session(withCookie(x.cookie))
	if sess == nil {
		t.Fatal("no session")
	}
	return sess
}

// ticket gives the fixture's session a ticket for intent, as the plan route
// does.
func (x *signFixture) ticket(t *testing.T, intent string, accept ...string) string {
	t.Helper()
	id := x.s.Ticket(withCookie(x.cookie), intent, accept)
	if id == "" {
		t.Fatal("no ticket for a live session")
	}
	return id
}

// start runs /auth/sign with the session cookie.
func (x *signFixture) start(ticket, ret string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/sign?"+url.Values{"ticket": {ticket}, "return": {ret}}.Encode(), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	x.s.Start(rec, req)
	return rec
}

// sign runs the whole signing login as sub and returns the callback's answer
// and the identity provider's authorize query.
func (x *signFixture) sign(t *testing.T, intent, sub string) (*httptest.ResponseRecorder, url.Values) {
	t.Helper()
	rec := x.start(x.ticket(t, intent, "owner.team"), "/#/service/x", x.cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	code, q := x.f.authorize(t, rec.Header().Get("Location"), sub)
	return x.callback(code, q.Get("state"), cookieNamed(t, rec, signCookie), x.cookie), q
}

func (x *signFixture) callback(code, state string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/sign/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	x.s.Callback(rec, req)
	return rec
}

// stored is the signing token kept for the fixture's session and intent.
func (x *signFixture) stored(t *testing.T, intent string) signEntry {
	t.Helper()
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	return x.s.entries[signKey{session: x.session(t).id, intent: intent}]
}

func TestSignLoginStoresOneShotToken(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	cb, q := x.sign(t, "intent-1", "ana")
	expect(t, cb.Code == http.StatusFound && cb.Header().Get("Location") == "/#/service/x", "callback %d %s", cb.Code, cb.Body)
	expect(t, q.Get("client_id") == "pacto-sign" && q.Get("prompt") == "login" && q.Get("max_age") == "0" &&
		q.Get("code_challenge_method") == "S256" && q.Get("redirect_uri") == "https://dash.example/auth/sign/callback",
		"the signing login asks for a fresh login with the sign client: %v", q)
	e := x.stored(t, "intent-1")
	var aud struct {
		Aud string `json:"aud"`
	}
	_ = json.Unmarshal(mustPayload(t, e.raw), &aud)
	expect(t, aud.Aud == "pacto-sign", "the stored token is the sign client's: %q", aud.Aud)
	expect(t, e.raw != x.session(t).idToken, "the stored token is never the session's")
	expect(t, len(e.accept) == 1 && e.accept[0] == "owner.team", "the ticket's accept set travels: %v", e.accept)
	c := cookieNamed(t, cb, signCookie)
	expect(t, c.MaxAge < 0, "the signing login cookie is cleared: %+v", c)
}

func mustPayload(t *testing.T, raw string) []byte {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", raw)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSignTokenBoundToIntentAndSession(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	rec := x.start(x.ticket(t, "intent-1"), "/", x.cookie)
	code, q := x.f.authorize(t, rec.Header().Get("Location"), "ana")
	// Another session of the same person finishes the login started here.
	other := x.newSession(t)
	expect(t, x.o.Session(withCookie(other)) != nil && len(x.o.sessions) == 2, "two live sessions")
	cb := x.callback(code, q.Get("state"), cookieNamed(t, rec, signCookie), other)
	expect(t, cb.Code == http.StatusBadRequest, "a login started by one session never lands in another: %d", cb.Code)
	expect(t, x.stored(t, "intent-1").raw == "", "nothing is kept for the session that started it either")
	_, stored := x.s.entries[signKey{session: x.session(t).id, intent: "intent-2"}]
	expect(t, !stored, "a login for one intent is kept for that intent only")
}

func TestSignTokenExpires(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	rec := x.start(x.ticket(t, "intent-1"), "/", x.cookie)
	code, q := x.f.authorize(t, rec.Header().Get("Location"), "ana")
	base := x.s.now()
	x.s.now = func() time.Time { return base.Add(signTTL + time.Second) }
	x.o.now = x.s.now
	cb := x.callback(code, q.Get("state"), cookieNamed(t, rec, signCookie), x.cookie)
	expect(t, cb.Code == http.StatusBadRequest, "a login finished after the signing window is refused: %d %s", cb.Code, cb.Body)
	expect(t, x.stored(t, "intent-1").raw == "", "an expired login keeps no token")

	x.s.now, x.o.now = time.Now, time.Now
	id := x.ticket(t, "intent-1")
	x.s.now = func() time.Time { return time.Now().Add(signTTL) }
	expect(t, x.start(id, "/", x.cookie).Code == http.StatusForbidden, "an expired ticket starts nothing")
}

func TestSignTokensBounded(t *testing.T) {
	now := time.Now()
	m := map[int]time.Time{-1: now.Add(-time.Second)} // expired
	for i := 0; i < signCap; i++ {
		m[i] = now.Add(time.Duration(i+1) * time.Second)
	}
	bound(m, now, func(e time.Time) time.Time { return e })
	_, expired := m[-1]
	_, oldest := m[0]
	expect(t, !expired && !oldest && len(m) == signCap-1, "expired and then the oldest go: %d left", len(m))

	x := newSignFixture(t, "ana", Claims{})
	for i := 0; i < signCap+5; i++ {
		x.start(x.ticket(t, string(rune('a'+i%26))+random(4)), "/", x.cookie)
	}
	expect(t, len(x.s.entries) <= signCap, "at most %d signing logins are kept: %d", signCap, len(x.s.entries))
}

func TestSignCallbackRefusesOtherPerson(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	cb, _ := x.sign(t, "intent-1", "bob")
	expect(t, cb.Code == http.StatusUnauthorized && strings.Contains(cb.Body.String(), "signed in as a different person"),
		"another subject is another person: %d %s", cb.Code, cb.Body)
	expect(t, x.stored(t, "intent-1").raw == "", "nothing is kept")

	y := newSignFixture(t, "ana", Claims{UsernameClaim: "preferred_username"})
	y.f.claims = map[string]map[string]any{
		"ana":   {"preferred_username": "ana", "email": "ana@acme.com"},
		"ana-2": {"preferred_username": "ana", "email": "mallory@acme.com"},
		"bob":   {"preferred_username": "bob", "email": "ana@acme.com"},
	}
	y.refreshSessionClaims(t)
	for _, sub := range []string{"ana-2", "bob"} {
		cb, _ := y.sign(t, "intent-"+sub, sub)
		expect(t, cb.Code == http.StatusUnauthorized, "%s: another username or email is another person: %d %s", sub, cb.Code, cb.Body)
	}
}

// refreshSessionClaims signs the fixture's person in again, so the session's
// ID token carries the claims set after the fixture was built.
func (x *signFixture) refreshSessionClaims(t *testing.T) { x.cookie = x.newSession(t) }

// newSession signs the fixture's person in once more, in another browser,
// and returns that session's cookie.
func (x *signFixture) newSession(t *testing.T) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	x.o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	code, q := x.f.authorize(t, rec.Header().Get("Location"), "ana")
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code="+code+"&state="+q.Get("state"), nil)
	req.AddCookie(cookieNamed(t, rec, loginCookie))
	cb := httptest.NewRecorder()
	x.o.Callback(cb, req)
	return cookieNamed(t, cb, SessionCookie)
}

func TestSignCallbackPairwiseSubComparesUsernameClaim(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{UsernameClaim: "preferred_username"})
	x.f.claims = map[string]map[string]any{
		"ana":          {"preferred_username": "ana", "email": "ana@acme.com"},
		"pairwise-sub": {"preferred_username": "ana", "email": "ana@acme.com"},
	}
	x.refreshSessionClaims(t)
	cb, _ := x.sign(t, "intent-1", "pairwise-sub")
	expect(t, cb.Code == http.StatusFound, "a pairwise subject with the same username and email is the same person: %d %s", cb.Code, cb.Body)
	expect(t, x.stored(t, "intent-1").raw != "", "the token is kept")
}

func TestSamePersonNeedsAUsername(t *testing.T) {
	f := newIdP(t)
	none := f.jwt(map[string]any{"iss": f.issuer()})
	expect(t, !samePerson(Claims{}, none, none), "two tokens naming nobody are no person")
	ana := f.jwt(map[string]any{"iss": f.issuer(), "sub": "ana"})
	withEmail := f.jwt(map[string]any{"iss": f.issuer(), "sub": "ana", "email": "ana@acme.com"})
	expect(t, samePerson(Claims{}, ana, withEmail), "an email on one token only is no conflict")
}

func TestSignCallbackConfidentialClient(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	x.f.secrets = map[string]string{"pacto-sign": "ss"}
	cb, _ := x.sign(t, "intent-1", "ana")
	expect(t, cb.Code == http.StatusFound, "the sign client authenticates with its secret: %d %s", cb.Code, cb.Body)

	x.s.cfg.ClientSecret = "wrong"
	x.s.prov = nil
	cb, _ = x.sign(t, "intent-2", "ana")
	expect(t, cb.Code == http.StatusUnauthorized && strings.Contains(cb.Body.String(), "exchanging the signing code failed"),
		"a wrong secret is refused: %d %s", cb.Code, cb.Body)
}

func TestSignCallbackNeverCreatesSession(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	before := x.session(t)
	cb, _ := x.sign(t, "intent-1", "ana")
	expect(t, cb.Code == http.StatusFound, "callback %d %s", cb.Code, cb.Body)
	for _, c := range cb.Result().Cookies() {
		expect(t, c.Name != SessionCookie, "the signing login never sets a session cookie: %+v", c)
	}
	after := x.session(t)
	expect(t, len(x.o.sessions) == 1 && after.id == before.id && after.idToken == before.idToken && after.authTime == before.authTime,
		"the session is unchanged")
}

func TestSignCallbackRefusesStaleAuthTime(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	x.f.authTime = time.Now().Add(-time.Hour)
	cb, _ := x.sign(t, "intent-1", "ana")
	expect(t, cb.Code == http.StatusUnauthorized && strings.Contains(cb.Body.String(), "fresh login"),
		"a login from before the signing request is refused: %d %s", cb.Code, cb.Body)
}

func TestSignCallbackRefusals(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	rec := x.start(x.ticket(t, "intent-1"), "https://evil.example", x.cookie)
	expect(t, rec.Code == http.StatusFound, "start %d", rec.Code)
	code, q := x.f.authorize(t, rec.Header().Get("Location"), "ana")
	login := cookieNamed(t, rec, signCookie)

	expect(t, x.callback(code, "other", login, x.cookie).Code == http.StatusBadRequest, "another state is refused")
	expect(t, x.callback(code, q.Get("state"), x.cookie).Code == http.StatusBadRequest, "no login cookie is refused")
	expect(t, x.callback(code, q.Get("state"), login).Code == http.StatusForbidden, "no session is refused")
	req := httptest.NewRequest(http.MethodGet, "/auth/sign/callback?error=access_denied&state="+q.Get("state"), nil)
	req.AddCookie(login)
	req.AddCookie(x.cookie)
	cb := httptest.NewRecorder()
	x.s.Callback(cb, req)
	expect(t, cb.Code == http.StatusUnauthorized, "the identity provider's refusal is passed on: %d", cb.Code)

	x.f.codeID = "not-a-token"
	cb = x.callback(code, q.Get("state"), login, x.cookie)
	expect(t, cb.Code == http.StatusUnauthorized && strings.Contains(cb.Body.String(), "does not verify"), "an unverifiable token is refused: %s", cb.Body)
	x.f.codeID = ""

	cb = x.callback(code, q.Get("state"), login, x.cookie)
	expect(t, cb.Code == http.StatusFound && cb.Header().Get("Location") == "/", "an unsafe return goes home: %d %q", cb.Code, cb.Header().Get("Location"))

	// A session login cookie never finishes a signing login: it has no intent.
	sl := httptest.NewRecorder()
	x.o.Login(sl, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	sc := cookieNamed(t, sl, loginCookie)
	sc.Name = signCookie
	_, sq := x.f.authorize(t, sl.Header().Get("Location"), "ana")
	expect(t, x.callback("c", sq.Get("state"), sc, x.cookie).Code == http.StatusBadRequest, "a sign-in state is no signing state")
}

func TestSignProviderDown(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	down, err := NewSigner(SignConfig{IssuerURL: "http://127.0.0.1:1", ClientID: "pacto-sign", RedirectURL: "https://d/cb"}, x.o)
	if err != nil {
		t.Fatal(err)
	}
	x.s, down = down, x.s
	rec := x.start(x.ticket(t, "intent-1"), "/", x.cookie)
	expect(t, rec.Code == http.StatusBadGateway, "an unreachable identity provider is a 502: %d", rec.Code)

	// A login cookie this dashboard signed reaches a callback whose provider is down.
	st := loginState{State: "st", Nonce: "n", Verifier: "v", Return: "/", Fresh: true, Intent: "intent-1", Started: time.Now().Unix()}
	b, _ := json.Marshal(st)
	c := &http.Cookie{Name: signCookie, Value: base64.RawURLEncoding.EncodeToString(b) + "." + x.o.sign(b)}
	cb := x.callback("code", "st", c, x.cookie)
	expect(t, cb.Code == http.StatusUnauthorized, "a provider that cannot be read refuses the callback: %d %s", cb.Code, cb.Body)
	_ = down
}

func TestSignStartNeedsOIDCSession(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	id := x.ticket(t, "intent-1")
	expect(t, x.start(id, "/").Code == http.StatusForbidden, "without an OIDC session nothing starts")
	tok := &http.Cookie{Name: SessionCookie, Value: "a-token-session-id"}
	expect(t, x.start(x.ticket(t, "intent-1"), "/", tok).Code == http.StatusForbidden, "a token session is no OIDC session")
}

func TestSignStartRequiresTicket(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	expect(t, x.start("", "/", x.cookie).Code == http.StatusForbidden, "no ticket")
	theirs := x.ticket(t, "intent-1")
	x.cookie = x.newSession(t) // another browser of the same person
	expect(t, x.start(theirs, "/", x.cookie).Code == http.StatusForbidden, "another session's ticket")
	mine := x.ticket(t, "intent-1")
	expect(t, x.start(mine, "/", x.cookie).Code == http.StatusFound, "a live ticket of this session starts")
	expect(t, x.start(mine, "/", x.cookie).Code == http.StatusForbidden, "a used ticket")
}

func TestSignClientMustDifferFromSessionClient(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	for name, cfg := range map[string]SignConfig{
		"same client": {ClientID: "pacto-dashboard", RedirectURL: "https://d/cb"},
		"no client":   {RedirectURL: "https://d/cb"},
		"no redirect": {ClientID: "pacto-sign"},
	} {
		if _, err := NewSigner(cfg, o); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if _, err := NewSigner(SignConfig{ClientID: "pacto-sign", RedirectURL: "https://d/cb"}, nil); err == nil {
		t.Error("a signer needs OIDC sign-in")
	}
	s, err := NewSigner(SignConfig{ClientID: "pacto-sign", RedirectURL: "https://d/cb"}, o)
	expect(t, err == nil && s.cfg.IssuerURL == f.srv.URL && s.client == o.client, "the session's issuer and client are the default: %v", err)
}

func TestRoutesServeTheSigningLogin(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	with := NewShared(x.o, nil).WithSigner(x.s).Routes()
	rec := httptest.NewRecorder()
	with.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/sign?ticket=x", nil))
	expect(t, rec.Code == http.StatusForbidden, "/auth/sign is served: %d", rec.Code)
	rec = httptest.NewRecorder()
	with.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/sign/callback", nil))
	expect(t, rec.Code == http.StatusBadRequest, "/auth/sign/callback is served: %d", rec.Code)
	rec = httptest.NewRecorder()
	NewShared(x.o, nil).Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/sign", nil))
	expect(t, rec.Code == http.StatusNotFound, "without a signer there is no signing login: %d", rec.Code)
}

func TestSignTokenSingleUse(t *testing.T) {
	x := newSignFixture(t, "ana", Claims{})
	req := withCookie(x.cookie)
	_, ok := x.s.Take(req, "intent-1")
	expect(t, !ok, "nothing to take before the signing login")
	cb, _ := x.sign(t, "intent-1", "ana")
	expect(t, cb.Code == http.StatusFound, "callback %d %s", cb.Code, cb.Body)
	expect(t, slices.Equal(x.s.Accepted(req, "intent-1"), []string{"owner.team"}), "the ticket's accept set: %v", x.s.Accepted(req, "intent-1"))
	_, ok = x.s.Take(req, "intent-2")
	expect(t, !ok, "a token is taken only for its own intent")
	raw, ok := x.s.Take(req, "intent-1")
	expect(t, ok && raw != "" && raw != x.session(t).idToken, "the sign client's token is taken once")
	_, ok = x.s.Take(req, "intent-1")
	expect(t, !ok, "a token is never taken twice")
	expect(t, x.s.Accepted(req, "intent-1") == nil, "a taken login carries no accept set")
	other := httptest.NewRequest(http.MethodGet, "/", nil)
	expect(t, x.s.Ticket(other, "intent-1", nil) == "", "no ticket without a session")
	_, ok = x.s.Take(other, "intent-1")
	expect(t, !ok, "nothing to take without a session")
	expect(t, x.s.Ticket(req, "", nil) == "", "no ticket without an intent")
	first, second := x.ticket(t, "intent-3"), x.ticket(t, "intent-3")
	expect(t, first != second, "every ticket is new")
}
